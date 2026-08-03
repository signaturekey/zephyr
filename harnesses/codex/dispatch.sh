#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage:
  dispatch.sh reviewer --role ROLE --packet FILE --output FILE [--format-retry]
  dispatch.sh evidence --prechecked FILE --evidence FILE --output FILE

Runs one isolated Codex process. Exact immutable inputs are streamed through
stdin and never materialized through the parent agent's tool output.
EOF
}

if [ "$#" -lt 1 ]; then
  usage
  exit 2
fi

kind=$1
shift
role=
packet=
prechecked=
evidence=
output=
format_retry=no

while [ "$#" -gt 0 ]; do
  case "$1" in
    --role)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      role=$2
      shift 2
      ;;
    --packet)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      packet=$2
      shift 2
      ;;
    --prechecked)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      prechecked=$2
      shift 2
      ;;
    --evidence)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      evidence=$2
      shift 2
      ;;
    --output)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      output=$2
      shift 2
      ;;
    --format-retry)
      format_retry=yes
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -f "$script_dir/../../roles/reviewer-protocol.md" ]; then
  package_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
  asset_root=$package_root
  asset_manifest=$package_root/harnesses/assets.sha256
elif [ -f "$script_dir/../references/roles/reviewer-protocol.md" ]; then
  skill_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
  asset_root=$skill_root/references
  asset_manifest=$asset_root/assets.sha256
else
  echo "zephyr codex dispatch: trusted assets not found" >&2
  exit 1
fi

hash_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  echo "zephyr codex dispatch: shasum or sha256sum is required" >&2
  exit 1
}

require_regular_absolute() {
  checked=$1
  label=$2
  case "$checked" in
    /*) ;;
    *)
      echo "zephyr codex dispatch: $label path must be absolute" >&2
      exit 1
      ;;
  esac
  if [ ! -f "$checked" ] || [ -L "$checked" ]; then
    echo "zephyr codex dispatch: $label must be a regular non-symlink file" >&2
    exit 1
  fi
}

verify_asset() {
  actual=$1
  relative=$2
  require_regular_absolute "$actual" "$relative"
  expected=$(awk -v target="$relative" '$2 == target { print $1 }' "$asset_manifest")
  if [ -z "$expected" ]; then
    echo "zephyr codex dispatch: asset is absent from checksum manifest: $relative" >&2
    exit 1
  fi
  actual_hash=$(hash_file "$actual")
  if [ "$actual_hash" != "$expected" ]; then
    echo "zephyr codex dispatch: checksum mismatch: $relative" >&2
    exit 1
  fi
}

case "$kind" in
  reviewer)
    case "$role" in
      code-reviewer|architect-reviewer|golang-expert|typescript-expert|frontend-expert|skill-authoring-expert|security-auditor|sql-expert|contract-reviewer|qa-expert|code-simplifier) ;;
      *)
        echo "zephyr codex dispatch: unknown reviewer role" >&2
        exit 2
        ;;
    esac
    [ -n "$packet" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] || { usage; exit 2; }
    protocol_path=$asset_root/roles/reviewer-protocol.md
    role_path=$asset_root/roles/$role.md
    schema_path=$asset_root/schemas/candidate-findings.codex.schema.json
    verify_asset "$protocol_path" roles/reviewer-protocol.md
    verify_asset "$role_path" roles/$role.md
    verify_asset "$schema_path" schemas/candidate-findings.codex.schema.json
    require_regular_absolute "$packet" packet
    effort=high
    ;;
  evidence)
    [ -z "$role" ] && [ -z "$packet" ] && [ -n "$prechecked" ] && [ -n "$evidence" ] && [ "$format_retry" = no ] || { usage; exit 2; }
    role=evidence-gate
    role_path=$asset_root/roles/evidence-gate.md
    schema_path=$asset_root/schemas/evidence-verdict.codex.schema.json
    verify_asset "$role_path" roles/evidence-gate.md
    verify_asset "$schema_path" schemas/evidence-verdict.codex.schema.json
    require_regular_absolute "$prechecked" prechecked
    require_regular_absolute "$evidence" evidence
    effort=xhigh
    ;;
  *)
    usage
    exit 2
    ;;
esac

case "$output" in
  /*) ;;
  *)
    echo "zephyr codex dispatch: output path must be absolute" >&2
    exit 2
    ;;
esac
if [ -e "$output" ] || [ -L "$output" ]; then
  echo "zephyr codex dispatch: refusing to overwrite output" >&2
  exit 1
fi
output_parent=$(dirname -- "$output")
if [ ! -d "$output_parent" ] || [ -L "$output_parent" ]; then
  echo "zephyr codex dispatch: output parent must be an existing non-symlink directory" >&2
  exit 1
fi

work_dir=$(mktemp -d "$output_parent/.zephyr-codex-dispatch.XXXXXX")
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM
prompt_file=$work_dir/prompt.txt
last_message=$work_dir/last-message.json
events_file=$work_dir/events.jsonl
stderr_file=$work_dir/stderr.log
empty_workspace=$work_dir/empty-workspace
isolated_codex_home=$work_dir/codex-home
retry_file=$work_dir/retry-directive.txt
mkdir -p "$empty_workspace" "$isolated_codex_home"
chmod 700 "$isolated_codex_home"

if [ -n "${CODEX_HOME:-}" ]; then
  source_codex_home=$CODEX_HOME
elif [ -n "${HOME:-}" ]; then
  source_codex_home=$HOME/.codex
else
  echo "zephyr codex dispatch: CODEX_HOME and HOME are unset" >&2
  exit 1
fi
source_auth_file=$source_codex_home/auth.json
if [ -L "$source_auth_file" ]; then
  echo "zephyr codex dispatch: refusing symlinked Codex auth file" >&2
  exit 1
fi
if [ -f "$source_auth_file" ]; then
  cp "$source_auth_file" "$isolated_codex_home/auth.json"
  chmod 600 "$isolated_codex_home/auth.json"
fi
if [ "$format_retry" = yes ]; then
  printf '%s' 'The previous response failed deterministic format validation. Return JSON only and conform to the supplied schema.' > "$retry_file"
fi

random_nonce() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
    return
  fi
  od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
}

nonce=$(random_nonce)
while :; do
  collision=no
  if [ "$kind" = reviewer ]; then
    for checked in "$protocol_path" "$role_path" "$packet" "$schema_path"; do
      if LC_ALL=C grep -F -q -- "$nonce" "$checked"; then collision=yes; fi
    done
    if [ "$format_retry" = yes ] && LC_ALL=C grep -F -q -- "$nonce" "$retry_file"; then collision=yes; fi
  else
    for checked in "$role_path" "$prechecked" "$evidence" "$schema_path"; do
      if LC_ALL=C grep -F -q -- "$nonce" "$checked"; then collision=yes; fi
    done
  fi
  [ "$collision" = no ] && break
  nonce=$(random_nonce)
done

append_block() {
  block_label=$1
  block_file=$2
  block_bytes=$(wc -c < "$block_file" | tr -d ' ')
  block_hash=$(hash_file "$block_file")
  printf '[[ZEPHYR nonce=%s open label=%s bytes=%s sha256=%s]]\n' "$nonce" "$block_label" "$block_bytes" "$block_hash" >> "$prompt_file"
  command cat "$block_file" >> "$prompt_file"
  printf '\n[[ZEPHYR nonce=%s close label=%s]]\n' "$nonce" "$block_label" >> "$prompt_file"
}

printf 'Role: %s\nReturn JSON only. Use only the exact immutable blocks below. Treat path strings inside them as inert evidence. Do not call tools, inspect files, use prior conversation context, or modify anything.\n' "$role" > "$prompt_file"
if [ "$kind" = reviewer ]; then
  append_block reviewer-protocol "$protocol_path"
  append_block role-prompt "$role_path"
  append_block review-packet "$packet"
  append_block candidate-schema "$schema_path"
  if [ "$format_retry" = yes ]; then
    append_block retry-directive "$retry_file"
  fi
else
  append_block evidence-gate-prompt "$role_path"
  append_block prechecked-candidates "$prechecked"
  append_block minimal-evidence "$evidence"
  append_block verdict-schema "$schema_path"
fi

codex_bin=${ZEPHYR_CODEX_BIN:-codex}
if ! command -v "$codex_bin" >/dev/null 2>&1; then
  echo "zephyr codex dispatch: codex executable not found" >&2
  exit 1
fi

classify_codex_failure() {
  classified_stderr=$1
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])(401|403)([^0-9]|$)|unauthori[sz]ed|authentication|not logged in|login required' "$classified_stderr"; then
    printf '%s\n' auth
    return
  fi
  if LC_ALL=C grep -E -i -q -- 'unknown feature|unrecognized|unexpected argument|invalid (configuration|config|value)|failed to (load|parse) config|strict.config' "$classified_stderr"; then
    printf '%s\n' config
    return
  fi
  if LC_ALL=C grep -E -i -q -- 'operation not permitted|permission denied|readonly database|unable to open database|failed to initialize in-process app-server client|could not create PATH aliases' "$classified_stderr"; then
    printf '%s\n' sandbox
    return
  fi
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])429([^0-9]|$)|rate.?limit|too many requests|usage limit|quota' "$classified_stderr"; then
    printf '%s\n' rate-limit
    return
  fi
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])5(00|02|03|04)([^0-9]|$)|overloaded|service unavailable|temporarily unavailable|internal server error' "$classified_stderr"; then
    printf '%s\n' provider-unavailable
    return
  fi
  if LC_ALL=C grep -E -i -q -- 'stream disconnected|connection reset|connection closed|broken pipe|timed out|timeout|failed to send request|error sending request|dns|network' "$classified_stderr"; then
    printf '%s\n' transport
    return
  fi
  printf '%s\n' unknown
}

retryable_failure() {
  case "$1" in
    rate-limit|provider-unavailable|transport|unknown) return 0 ;;
    *) return 1 ;;
  esac
}

invoke_codex() {
  CODEX_HOME="$isolated_codex_home" "$codex_bin" exec \
    --strict-config \
    --ignore-user-config \
    --ignore-rules \
    --ephemeral \
    --skip-git-repo-check \
    --sandbox read-only \
    --cd "$empty_workspace" \
    --color never \
    --json \
    --output-schema "$schema_path" \
    --output-last-message "$last_message" \
    --config 'approval_policy="never"' \
    --config 'web_search="disabled"' \
    --config 'include_apps_instructions=false' \
    --config 'include_environment_context=false' \
    --config 'allow_login_shell=false' \
    --config 'mcp_servers={}' \
    --config 'apps={ _default = { enabled = false, destructive_enabled = false, open_world_enabled = false } }' \
    --config 'memories={ use_memories = false, generate_memories = false, dedicated_tools = false }' \
    --config 'developer_instructions="Act only as an isolated Zephyr reviewer. Use only the exact blocks in the user prompt. Never call a tool, open a path, use memory, or modify anything. Return JSON only."' \
    --config "model_reasoning_effort=\"$effort\"" \
    --disable apps \
    --disable artifact \
    --disable auth_elicitation \
    --disable browser_use \
    --disable browser_use_external \
    --disable browser_use_full_cdp_access \
    --disable code_mode \
    --disable code_mode_buffered_exec \
    --disable code_mode_host \
    --disable code_mode_only \
    --disable computer_use \
    --disable goals \
    --disable hooks \
    --disable image_generation \
    --disable in_app_browser \
    --disable memories \
    --disable multi_agent \
    --disable plugins \
    --disable plugin_sharing \
    --disable remote_plugin \
    --disable request_permissions_tool \
    --disable shell_snapshot \
    --disable shell_tool \
    --disable skill_mcp_dependency_install \
    --disable skill_search \
    --disable standalone_web_search \
    --disable tool_call_mcp_elicitation \
    --disable tool_suggest \
    --disable unified_exec \
    --disable workspace_dependencies \
    - < "$prompt_file" > "$events_file" 2> "$stderr_file"
}

attempt=1
first_failure=
while :; do
  if [ -e "$last_message" ] || [ -L "$last_message" ]; then
    rm -f -- "$last_message"
  fi
  if invoke_codex; then
    codex_status=0
  else
    codex_status=$?
  fi
  if [ "$codex_status" -eq 0 ]; then
    break
  fi

  failure_category=$(classify_codex_failure "$stderr_file")
  failure_hash=$(hash_file "$stderr_file")
  failure_bytes=$(wc -c < "$stderr_file" | tr -d ' ')
  failure_diagnostic="category=$failure_category status=$codex_status stderr_sha256=$failure_hash stderr_bytes=$failure_bytes"

  if [ "$attempt" -eq 1 ] && retryable_failure "$failure_category"; then
    first_failure=$failure_diagnostic
    retry_delay=$(printf '%s' "$role" | cksum | awk '{ print ($1 % 4) + 1 }')
    sleep "$retry_delay"
    attempt=2
    continue
  fi

  if [ -n "$first_failure" ]; then
    echo "zephyr codex dispatch: isolated Codex process failed after $attempt attempts; first={$first_failure}; last={$failure_diagnostic}" >&2
  else
    echo "zephyr codex dispatch: isolated Codex process failed without retry; $failure_diagnostic" >&2
  fi
  exit 1
done

if [ ! -s "$last_message" ]; then
  echo "zephyr codex dispatch: isolated Codex process returned an empty result" >&2
  exit 1
fi
chmod 600 "$last_message"
mv "$last_message" "$output"
printf '{"kind":"%s","role":"%s","output":"%s"}\n' "$kind" "$role" "$output"

#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage:
  dispatch.sh routing --packet FILE --request FILE --output FILE
  dispatch.sh reviewer --role ROLE --packet FILE --output FILE [--format-retry]
  dispatch.sh evidence --prechecked FILE --evidence FILE --output FILE

Runs one isolated OpenCode process with exact immutable inputs streamed through stdin.
EOF
}

[ "$#" -ge 1 ] || { usage; exit 2; }
kind=$1
shift
role=
packet=
request=
prechecked=
evidence=
output=
format_retry=no

while [ "$#" -gt 0 ]; do
  case "$1" in
    --role) [ "$#" -ge 2 ] || { usage; exit 2; }; role=$2; shift 2 ;;
    --packet) [ "$#" -ge 2 ] || { usage; exit 2; }; packet=$2; shift 2 ;;
    --request) [ "$#" -ge 2 ] || { usage; exit 2; }; request=$2; shift 2 ;;
    --prechecked) [ "$#" -ge 2 ] || { usage; exit 2; }; prechecked=$2; shift 2 ;;
    --evidence) [ "$#" -ge 2 ] || { usage; exit 2; }; evidence=$2; shift 2 ;;
    --output) [ "$#" -ge 2 ] || { usage; exit 2; }; output=$2; shift 2 ;;
    --format-retry) format_retry=yes; shift ;;
    --help|-h) usage; exit 0 ;;
    *) usage; exit 2 ;;
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
  echo "zephyr opencode dispatch: trusted assets not found" >&2
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
  echo "zephyr opencode dispatch: shasum or sha256sum is required" >&2
  exit 1
}

require_regular_absolute() {
  checked=$1
  label=$2
  case "$checked" in /*) ;; *) echo "zephyr opencode dispatch: $label path must be absolute" >&2; exit 1 ;; esac
  if [ ! -f "$checked" ] || [ -L "$checked" ]; then
    echo "zephyr opencode dispatch: $label must be a regular non-symlink file" >&2
    exit 1
  fi
}

verify_asset() {
  actual=$1
  relative=$2
  require_regular_absolute "$actual" "$relative"
  expected=$(awk -v target="$relative" '$2 == target { count++; value = $1 } END { if (count == 1) print value }' "$asset_manifest")
  if [ -z "$expected" ]; then
    echo "zephyr opencode dispatch: asset is absent from checksum manifest: $relative" >&2
    exit 1
  fi
  actual_hash=$(hash_file "$actual")
  if [ "$actual_hash" != "$expected" ]; then
    echo "zephyr opencode dispatch: checksum mismatch: $relative" >&2
    exit 1
  fi
}

case "$kind" in
  routing)
    [ -z "$role" ] && [ -n "$packet" ] && [ -n "$request" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] && [ "$format_retry" = no ] || { usage; exit 2; }
    role=semantic-router
    role_path=$asset_root/roles/semantic-router.md
    schema_path=$asset_root/schemas/semantic-routing.schema.json
    verify_asset "$role_path" roles/semantic-router.md
    verify_asset "$schema_path" schemas/semantic-routing.schema.json
    require_regular_absolute "$packet" packet
    require_regular_absolute "$request" request
    ;;
  reviewer)
    case "$role" in
      code-reviewer|architect-reviewer|golang-expert|typescript-expert|frontend-expert|skill-authoring-expert|reliability-expert|messaging-expert|infrastructure-expert|storage-expert|security-auditor|sql-expert|contract-reviewer|qa-expert|code-simplifier) ;;
      *) echo "zephyr opencode dispatch: unknown reviewer role" >&2; exit 2 ;;
    esac
    [ -n "$packet" ] && [ -z "$request" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] || { usage; exit 2; }
    protocol_path=$asset_root/roles/reviewer-protocol.md
    role_path=$asset_root/roles/$role.md
    schema_path=$asset_root/schemas/candidate-findings.schema.json
    verify_asset "$protocol_path" roles/reviewer-protocol.md
    verify_asset "$role_path" roles/$role.md
    verify_asset "$schema_path" schemas/candidate-findings.schema.json
    require_regular_absolute "$packet" packet
    ;;
  evidence)
    [ -z "$role" ] && [ -z "$packet" ] && [ -z "$request" ] && [ -n "$prechecked" ] && [ -n "$evidence" ] && [ "$format_retry" = no ] || { usage; exit 2; }
    role=evidence-gate
    role_path=$asset_root/roles/evidence-gate.md
    schema_path=$asset_root/schemas/evidence-verdict.schema.json
    verify_asset "$role_path" roles/evidence-gate.md
    verify_asset "$schema_path" schemas/evidence-verdict.schema.json
    require_regular_absolute "$prechecked" prechecked
    require_regular_absolute "$evidence" evidence
    ;;
  *) usage; exit 2 ;;
esac

case "$output" in /*) ;; *) echo "zephyr opencode dispatch: output path must be absolute" >&2; exit 2 ;; esac
if [ -e "$output" ] || [ -L "$output" ]; then
  echo "zephyr opencode dispatch: refusing to overwrite output" >&2
  exit 1
fi
output_parent=$(dirname -- "$output")
if [ ! -d "$output_parent" ] || [ -L "$output_parent" ]; then
  echo "zephyr opencode dispatch: output parent must be an existing non-symlink directory" >&2
  exit 1
fi

work_dir=$(mktemp -d "$output_parent/.zephyr-opencode-dispatch.XXXXXX")
cleanup() { rm -rf -- "$work_dir"; }
trap cleanup EXIT HUP INT TERM
prompt_file=$work_dir/prompt.txt
result_file=$work_dir/result.json
stderr_file=$work_dir/stderr.log
retry_file=$work_dir/retry-directive.txt
empty_workspace=$work_dir/empty-workspace
isolated_home=$work_dir/home
isolated_config=$work_dir/config
isolated_data=$work_dir/data
isolated_cache=$work_dir/cache
isolated_state=$work_dir/state
mkdir -p "$empty_workspace" "$isolated_home" "$isolated_config/opencode/agents" "$isolated_data/opencode" "$isolated_cache" "$isolated_state"

cat >"$isolated_config/opencode/opencode.json" <<'EOF'
{
  "$schema": "https://opencode.ai/config.json",
  "share": "disabled",
  "instructions": [],
  "mcp": {},
  "permission": { "*": "deny" }
}
EOF
cat >"$isolated_config/opencode/agents/zephyr-dispatch.md" <<'EOF'
---
description: Isolated Zephyr process transport.
mode: primary
permission:
  '*': deny
---

Use only the exact immutable blocks in the user prompt. Never call a tool, open a path, use prior context, or modify anything. Return JSON only.
EOF

if [ -n "${ZEPHYR_OPENCODE_AUTH_FILE:-}" ]; then
  source_auth_file=$ZEPHYR_OPENCODE_AUTH_FILE
elif [ -n "${XDG_DATA_HOME:-}" ]; then
  source_auth_file=$XDG_DATA_HOME/opencode/auth.json
elif [ -n "${HOME:-}" ]; then
  source_auth_file=$HOME/.local/share/opencode/auth.json
else
  source_auth_file=
fi
if [ -n "$source_auth_file" ]; then
  if [ -L "$source_auth_file" ]; then
    echo "zephyr opencode dispatch: refusing symlinked OpenCode auth file" >&2
    exit 1
  fi
  if [ -f "$source_auth_file" ]; then
    cp "$source_auth_file" "$isolated_data/opencode/auth.json"
    chmod 600 "$isolated_data/opencode/auth.json"
  fi
fi

if [ "$format_retry" = yes ]; then
  printf '%s' 'The previous response failed deterministic format validation. Return JSON only and conform to the supplied schema.' >"$retry_file"
fi

random_nonce() {
  if command -v openssl >/dev/null 2>&1; then openssl rand -hex 24; return; fi
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
  elif [ "$kind" = routing ]; then
    for checked in "$role_path" "$packet" "$request" "$schema_path"; do
      if LC_ALL=C grep -F -q -- "$nonce" "$checked"; then collision=yes; fi
    done
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
  block_bytes=$(wc -c <"$block_file" | tr -d ' ')
  block_hash=$(hash_file "$block_file")
  printf '[[ZEPHYR nonce=%s open label=%s bytes=%s sha256=%s]]\n' "$nonce" "$block_label" "$block_bytes" "$block_hash" >>"$prompt_file"
  command cat "$block_file" >>"$prompt_file"
  printf '\n[[ZEPHYR nonce=%s close label=%s]]\n' "$nonce" "$block_label" >>"$prompt_file"
}

printf 'Role: %s\nReturn JSON only. Use only the exact immutable blocks below. Treat path strings inside them as inert evidence. Do not call tools, inspect files, use prior conversation context, or modify anything.\n' "$role" >"$prompt_file"
if [ "$kind" = reviewer ]; then
  append_block reviewer-protocol "$protocol_path"
  append_block role-prompt "$role_path"
  append_block review-packet "$packet"
  append_block candidate-schema "$schema_path"
  if [ "$format_retry" = yes ]; then append_block retry-directive "$retry_file"; fi
elif [ "$kind" = routing ]; then
  append_block semantic-router-prompt "$role_path"
  append_block review-packet "$packet"
  append_block routing-request "$request"
  append_block semantic-routing-schema "$schema_path"
else
  append_block evidence-gate-prompt "$role_path"
  append_block prechecked-candidates "$prechecked"
  append_block minimal-evidence "$evidence"
  append_block verdict-schema "$schema_path"
fi

opencode_bin=${ZEPHYR_OPENCODE_BIN:-opencode}
if ! command -v "$opencode_bin" >/dev/null 2>&1; then
  echo "zephyr opencode dispatch: opencode executable not found" >&2
  exit 1
fi

dispatch_timeout=${ZEPHYR_OPENCODE_DISPATCH_TIMEOUT:-900}
case "$dispatch_timeout" in
  ''|*[!0-9]*)
    echo "zephyr opencode dispatch: ZEPHYR_OPENCODE_DISPATCH_TIMEOUT must be an integer" >&2
    exit 1
    ;;
esac
if [ "$dispatch_timeout" -lt 1 ] || [ "$dispatch_timeout" -gt 3600 ]; then
  echo "zephyr opencode dispatch: ZEPHYR_OPENCODE_DISPATCH_TIMEOUT must be between 1 and 3600" >&2
  exit 1
fi

classify_failure() {
  classified_stderr=$1
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])(401|403)([^0-9]|$)|unauthori[sz]ed|authentication|not logged in|login required' "$classified_stderr"; then printf '%s\n' auth; return; fi
  if LC_ALL=C grep -E -i -q -- 'unknown agent|invalid config|failed to (load|parse) config|unexpected argument|unknown option' "$classified_stderr"; then printf '%s\n' config; return; fi
  if LC_ALL=C grep -E -i -q -- 'operation not permitted|permission denied|readonly database|unable to open database' "$classified_stderr"; then printf '%s\n' sandbox; return; fi
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])429([^0-9]|$)|rate.?limit|too many requests|usage limit|quota' "$classified_stderr"; then printf '%s\n' rate-limit; return; fi
  if LC_ALL=C grep -E -i -q -- '(^|[^0-9])5(00|02|03|04)([^0-9]|$)|overloaded|service unavailable|temporarily unavailable|internal server error' "$classified_stderr"; then printf '%s\n' provider-unavailable; return; fi
  if LC_ALL=C grep -E -i -q -- 'connection reset|connection closed|broken pipe|timed out|timeout|dns|network' "$classified_stderr"; then printf '%s\n' transport; return; fi
  printf '%s\n' unknown
}

retryable_failure() { case "$1" in rate-limit|provider-unavailable|transport|timeout|unknown) return 0 ;; *) return 1 ;; esac; }

run_with_timeout() {
  timeout_limit=$1
  command_stdout=$2
  command_stderr=$3
  command_stdin=$4
  shift 4
  timeout_marker=$work_dir/timeout-marker-$$
  watchdog_sleep_file=$work_dir/watchdog-sleep-$$
  rm -f -- "$timeout_marker"
  rm -f -- "$watchdog_sleep_file"

  start_in_isolated_session() {
    if command -v setsid >/dev/null 2>&1; then exec setsid "$@"; fi
    if command -v perl >/dev/null 2>&1; then
      exec perl -MPOSIX=setsid -e 'setsid() or die "setsid: $!\n"; exec @ARGV or die "exec: $!\n"' "$@"
    fi
    echo "zephyr opencode dispatch: setsid or Perl with POSIX::setsid is required for bounded dispatch" >&2
    exit 127
  }

  terminate_process_group() {
    signal=$1
    process_group=$2
    /bin/kill "-$signal" "-$process_group" 2>/dev/null || :
  }

  (
    start_in_isolated_session "$@" < "$command_stdin"
  ) > "$command_stdout" 2> "$command_stderr" &
  command_pid=$!
  (
    sleep "$timeout_limit" &
    watchdog_sleep_pid=$!
    printf '%s\n' "$watchdog_sleep_pid" > "$watchdog_sleep_file"
    wait "$watchdog_sleep_pid"
    if kill -0 "$command_pid" 2>/dev/null; then
      : > "$timeout_marker"
      terminate_process_group TERM "$command_pid"
      sleep 1
      if kill -0 "$command_pid" 2>/dev/null; then terminate_process_group KILL "$command_pid"; fi
    fi
  ) >/dev/null 2>&1 &
  watchdog_pid=$!
  while [ ! -s "$watchdog_sleep_file" ] && kill -0 "$watchdog_pid" 2>/dev/null; do :; done
  if wait "$command_pid"; then command_status=0; else command_status=$?; fi
  if [ -f "$watchdog_sleep_file" ]; then
    watchdog_sleep_pid=$(sed -n '1p' "$watchdog_sleep_file")
    kill "$watchdog_sleep_pid" 2>/dev/null || :
  fi
  kill "$watchdog_pid" 2>/dev/null || :
  wait "$watchdog_pid" 2>/dev/null || :
  rm -f -- "$watchdog_sleep_file"
  if [ -f "$timeout_marker" ]; then
    rm -f -- "$timeout_marker"
    return 124
  fi
  return "$command_status"
}

invoke_opencode() {
  set -- run --pure --agent zephyr-dispatch --format default --dir "$empty_workspace" --title "Zephyr $role"
  if [ -n "${ZEPHYR_OPENCODE_MODEL:-}" ]; then set -- "$@" --model "$ZEPHYR_OPENCODE_MODEL"; fi
  if [ -n "${ZEPHYR_OPENCODE_VARIANT:-}" ]; then set -- "$@" --variant "$ZEPHYR_OPENCODE_VARIANT"; fi
  run_with_timeout "$dispatch_timeout" "$result_file" "$stderr_file" "$prompt_file" \
    env -u OPENCODE_CONFIG -u OPENCODE_CONFIG_CONTENT \
    HOME="$isolated_home" \
    XDG_CONFIG_HOME="$isolated_config" \
    XDG_DATA_HOME="$isolated_data" \
    XDG_CACHE_HOME="$isolated_cache" \
    XDG_STATE_HOME="$isolated_state" \
    "$opencode_bin" "$@"
}

attempt=1
first_failure=
while :; do
  : >"$result_file"
  : >"$stderr_file"
  if invoke_opencode; then opencode_status=0; else opencode_status=$?; fi
  if [ "$opencode_status" -eq 0 ]; then break; fi
  if [ "$opencode_status" -eq 124 ]; then
    failure_category=timeout
  else
    failure_category=$(classify_failure "$stderr_file")
  fi
  failure_hash=$(hash_file "$stderr_file")
  failure_bytes=$(wc -c <"$stderr_file" | tr -d ' ')
  failure_diagnostic="category=$failure_category status=$opencode_status stderr_sha256=$failure_hash stderr_bytes=$failure_bytes"
  if [ "$attempt" -eq 1 ] && retryable_failure "$failure_category"; then
    first_failure=$failure_diagnostic
    retry_delay=$(printf '%s' "$role" | cksum | awk '{ print ($1 % 4) + 1 }')
    sleep "$retry_delay"
    attempt=2
    continue
  fi
  if [ -n "$first_failure" ]; then
    echo "zephyr opencode dispatch: isolated OpenCode process failed after $attempt attempts; first={$first_failure}; last={$failure_diagnostic}" >&2
  else
    echo "zephyr opencode dispatch: isolated OpenCode process failed without retry; $failure_diagnostic" >&2
  fi
  exit 1
done

if [ ! -s "$result_file" ]; then
  echo "zephyr opencode dispatch: isolated OpenCode process returned an empty result" >&2
  exit 1
fi
chmod 600 "$result_file"
mv "$result_file" "$output"
printf '{"kind":"%s","role":"%s","output":"%s"}\n' "$kind" "$role" "$output"

#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage:
  dispatch.sh probe --output FILE [--policy FILE]
  dispatch.sh routing --packet FILE --request FILE --compat FILE --output FILE [--policy FILE]
  dispatch.sh reviewer --role ROLE --packet FILE --compat FILE --output FILE [--policy FILE] [--format-retry]
  dispatch.sh evidence --prechecked FILE --evidence FILE --compat FILE --output FILE [--policy FILE]

probe freezes the Codex CLI compatibility capabilities for one Zephyr run.
Exact immutable inputs are streamed through stdin and never materialized through the parent agent's tool output.
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
request=
prechecked=
evidence=
compat=
output=
format_retry=no
policy=

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
    --request)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      request=$2
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
    --compat)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      compat=$2
      shift 2
      ;;
    --output)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      output=$2
      shift 2
      ;;
    --policy)
      [ "$#" -ge 2 ] || { usage; exit 2; }
      policy=$2
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
  probe)
    [ -z "$role" ] && [ -z "$packet" ] && [ -z "$request" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] && [ -z "$compat" ] && [ "$format_retry" = no ] || { usage; exit 2; }
    effort=high
    ;;
  routing)
    [ -z "$role" ] && [ -n "$packet" ] && [ -n "$request" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] && [ -n "$compat" ] && [ "$format_retry" = no ] || { usage; exit 2; }
    role=semantic-router
    role_path=$asset_root/roles/semantic-router.md
    schema_path=$asset_root/schemas/semantic-routing.codex.schema.json
    verify_asset "$role_path" roles/semantic-router.md
    verify_asset "$schema_path" schemas/semantic-routing.codex.schema.json
    require_regular_absolute "$packet" packet
    require_regular_absolute "$request" request
    effort=high
    ;;
  reviewer)
    case "$role" in
      code-reviewer|architect-reviewer|golang-expert|python-expert|typescript-expert|frontend-expert|skill-authoring-expert|reliability-expert|messaging-expert|infrastructure-expert|storage-expert|security-auditor|sql-expert|contract-reviewer|qa-expert|code-simplifier) ;;
      *)
        echo "zephyr codex dispatch: unknown reviewer role" >&2
        exit 2
        ;;
    esac
    [ -n "$packet" ] && [ -z "$request" ] && [ -n "$compat" ] && [ -z "$prechecked" ] && [ -z "$evidence" ] || { usage; exit 2; }
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
    [ -z "$role" ] && [ -z "$packet" ] && [ -z "$request" ] && [ -n "$prechecked" ] && [ -n "$evidence" ] && [ -n "$compat" ] && [ "$format_retry" = no ] || { usage; exit 2; }
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
events_pipe=$work_dir/events.pipe
stderr_file=$work_dir/stderr.log
recovery_stderr_file=$work_dir/recovery-stderr.log
recovered_message=$work_dir/recovered-message.json
codex_status_file=$work_dir/codex-status.txt
empty_workspace=$work_dir/empty-workspace
isolated_codex_home=$work_dir/codex-home
retry_file=$work_dir/retry-directive.txt
compatibility_features=$work_dir/features.txt
mkdir -p "$empty_workspace" "$isolated_codex_home"
chmod 700 "$isolated_codex_home"

execution_model=inherit
execution_effort=$effort
execution_fast=false
execution_policy_sha256=none
if [ -n "$policy" ]; then
  require_regular_absolute "$policy" policy
  if [ "$(sed -n '1p' "$policy")" != zephyr-codex-model-policy-v1 ]; then
    echo "zephyr codex dispatch: unsupported model policy" >&2
    exit 1
  fi
  case "$kind" in
    probe) policy_process=probe; policy_role=- ;;
    routing) policy_process=semantic-router; policy_role=- ;;
    reviewer) policy_process=reviewer; policy_role=$role ;;
    evidence) policy_process=evidence-gate; policy_role=- ;;
  esac
  policy_entry=$(awk -F '\t' -v process="$policy_process" -v policy_role="$policy_role" '
    NR == 1 { next }
    NF != 5 { exit 2 }
    $1 == process && $2 == policy_role { count++; line = $0 }
    END { if (count != 1) exit 1; print line }
  ' "$policy") || {
    echo "zephyr codex dispatch: malformed or incomplete model policy" >&2
    exit 1
  }
  IFS="$(printf '\t')" read -r policy_process policy_role execution_model execution_effort execution_fast <<EOF
$policy_entry
EOF
  case "$execution_model" in
    inherit) ;;
    ''|*' '*|*'\t'*|*'/'*|*'\\'*) echo "zephyr codex dispatch: unsafe model policy model" >&2; exit 1 ;;
    *) ;;
  esac
  case "$execution_effort" in
    none|low|medium|high|xhigh|max) ;;
    *) echo "zephyr codex dispatch: unsafe model policy effort" >&2; exit 1 ;;
  esac
  case "$execution_fast" in true|false) ;; *) echo "zephyr codex dispatch: unsafe model policy fast value" >&2; exit 1 ;; esac
  execution_policy_sha256=$(hash_file "$policy")
fi

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

codex_bin=${ZEPHYR_CODEX_BIN:-codex}
codex_path=$(command -v "$codex_bin" 2>/dev/null || true)
case "$codex_path" in
  /*) ;;
  *)
    echo "zephyr codex dispatch: codex executable not found" >&2
    exit 1
    ;;
esac
if [ ! -x "$codex_path" ]; then
  echo "zephyr codex dispatch: codex executable is not executable" >&2
  exit 1
fi

zephyr_core_bin=${ZEPHYR_CORE_BIN:-zephyr}
zephyr_core_path=$(command -v "$zephyr_core_bin" 2>/dev/null || true)
case "$zephyr_core_path" in
  /*) ;;
  *)
    echo "zephyr codex dispatch: Zephyr core executable not found" >&2
    exit 1
    ;;
esac
if [ ! -x "$zephyr_core_path" ]; then
  echo "zephyr codex dispatch: Zephyr core executable is not executable" >&2
  exit 1
fi

probe_timeout=${ZEPHYR_CODEX_PROBE_TIMEOUT:-10}
case "$probe_timeout" in
  ''|*[!0-9]*)
    echo "zephyr codex dispatch: ZEPHYR_CODEX_PROBE_TIMEOUT must be an integer" >&2
    exit 1
    ;;
esac
if [ "$probe_timeout" -lt 1 ] || [ "$probe_timeout" -gt 600 ]; then
  echo "zephyr codex dispatch: ZEPHYR_CODEX_PROBE_TIMEOUT must be between 1 and 600" >&2
  exit 1
fi

dispatch_timeout=${ZEPHYR_CODEX_DISPATCH_TIMEOUT:-900}
case "$dispatch_timeout" in
  ''|*[!0-9]*)
    echo "zephyr codex dispatch: ZEPHYR_CODEX_DISPATCH_TIMEOUT must be an integer" >&2
    exit 1
    ;;
esac
if [ "$dispatch_timeout" -lt 1 ] || [ "$dispatch_timeout" -gt 3600 ]; then
  echo "zephyr codex dispatch: ZEPHYR_CODEX_DISPATCH_TIMEOUT must be between 1 and 3600" >&2
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
    rate-limit|provider-unavailable|transport|timeout|unknown) return 0 ;;
    *) return 1 ;;
  esac
}

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

  # A timeout must stop Codex and every process it starts. A background POSIX
  # shell job normally shares the dispatcher's process group, so signal a new
  # session instead. macOS does not ship a setsid executable; its system Perl
  # provides the same POSIX primitive.
  start_in_isolated_session() {
    if command -v setsid >/dev/null 2>&1; then
      exec setsid "$@"
    fi
    if command -v perl >/dev/null 2>&1; then
      exec perl -MPOSIX=setsid -e 'setsid() or die "setsid: $!\\n"; exec @ARGV or die "exec: $!\\n"' "$@"
    fi
    echo "zephyr codex dispatch: setsid or Perl with POSIX::setsid is required for bounded compatibility probes" >&2
    exit 127
  }

  terminate_process_group() {
    signal=$1
    process_group=$2
    /bin/kill "-$signal" "-$process_group" 2>/dev/null || :
  }

  (
    HOME="$isolated_codex_home" CODEX_HOME="$isolated_codex_home"
    export HOME CODEX_HOME
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
      if kill -0 "$command_pid" 2>/dev/null; then
        terminate_process_group KILL "$command_pid"
      fi
    fi
  ) >/dev/null 2>&1 &
  watchdog_pid=$!
  while [ ! -s "$watchdog_sleep_file" ] && kill -0 "$watchdog_pid" 2>/dev/null; do :; done
  if wait "$command_pid"; then
    command_status=0
  else
    command_status=$?
  fi
  if [ -s "$watchdog_sleep_file" ]; then
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

run_probe_command() {
  probe_label=$1
  probe_stdout=$2
  probe_stderr=$3
  shift 3
  probe_attempt=1
  probe_first_failure=
  while :; do
    if run_with_timeout "$probe_timeout" "$probe_stdout" "$probe_stderr" /dev/null "$@"; then
      return 0
    else
      probe_status=$?
    fi
    if [ "$probe_status" -eq 124 ]; then
      probe_category=transport
    else
      probe_category=$(classify_codex_failure "$probe_stderr")
    fi
    probe_hash=$(hash_file "$probe_stderr")
    probe_bytes=$(wc -c < "$probe_stderr" | tr -d ' ')
    probe_diagnostic="category=$probe_category status=$probe_status stderr_sha256=$probe_hash stderr_bytes=$probe_bytes"
    if [ "$probe_attempt" -eq 1 ] && retryable_failure "$probe_category"; then
      probe_first_failure=$probe_diagnostic
      sleep 1
      probe_attempt=2
      continue
    fi
    if [ -n "$probe_first_failure" ]; then
      echo "zephyr codex dispatch: compatibility probe $probe_label failed after $probe_attempt attempts; first={$probe_first_failure}; last={$probe_diagnostic}" >&2
    else
      echo "zephyr codex dispatch: compatibility probe $probe_label failed without retry; $probe_diagnostic" >&2
    fi
    return 1
  done
}

# This is the only feature-policy table. Every recognized feature reported by
# the probed Codex binary is explicitly disabled in isolated reviewer
# processes. An enabled feature that is not listed here is allowed with an
# explicit coverage warning so ordinary Codex version drift does not block a
# review.
isolated_features='
apply_patch_freeform
apply_patch_streaming_events
apps
apps_mcp_path_override
artifact
auth_elicitation
auto_compaction
browser_use
browser_use_external
browser_use_full_cdp_access
chronicle
code_mode
code_mode_buffered_exec
code_mode_host
code_mode_only
codex_git_commit
collaboration_modes
computer_use
concurrent_reasoning_summaries
current_time_reminder
default_mode_request_user_input
deferred_executor
deferred_tool_world_state
elevated_windows_sandbox
enable_fanout
enable_mcp_apps
enable_request_compression
exec_permission_approvals
executor_capability_discovery
experimental_windows_sandbox
external_agent_memory_import
external_migration
fast_mode
goals
guardian_approval
guardianv2
hooks
image_detail_original
image_generation
in_app_browser
in_app_updates
item_ids
js_repl
js_repl_tools_only
local_thread_store_compression
mcp_2026_07_28
memories
mentions_v2
multi_agent
multi_agent_mode
multi_agent_v2
network_proxy
non_prefixed_mcp_tool_names
personality
plugin_hooks
plugin_sharing
plugins
prevent_idle_sleep
realtime_conversation
remote_compaction_v2
remote_control
remote_models
remote_plugin
request_permissions_tool
request_rule
resize_all_images
respect_system_proxy
responses_websockets
responses_websockets_v2
rollout_budget
runtime_metrics
search_tool
secret_auth_storage
shell_snapshot
shell_tool
shell_zsh_fork
skill_env_var_dependency_prompt
skill_mcp_dependency_install
skill_search
sqlite
standalone_web_search
steer
terminal_resize_reflow
terminal_visualization_instructions
token_budget
tool_call_mcp_elicitation
tool_search
tool_search_always_defer_mcp_tools
tool_suggest
tui_app_server
unavailable_dummy_tools
undo
unified_exec
unified_exec_zsh_fork
use_agent_identity
use_legacy_landlock
use_linux_sandbox_bwrap
web_search_cached
web_search_request
workspace_dependencies
workspace_owner_usage_nudge
'

list_contains() {
  list=$1
  sought=$2
  case "
$list
" in
    *"
$sought
"*) return 0 ;;
    *) return 1 ;;
  esac
}

classify_config_option() {
  config_stderr=$1
  for config_key in \
    web_search \
    include_apps_instructions \
    include_environment_context \
    allow_login_shell \
    apps \
    memories; do
    if LC_ALL=C grep -F -q -- "$config_key" "$config_stderr"; then
      printf '%s\n' "$config_key"
      return 0
    fi
  done
  return 1
}

descriptor_field() {
  descriptor_key=$1
  descriptor_file=$2
  awk -F= -v key="$descriptor_key" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      count++
    }
    END {
      if (count != 1) {
        exit 1
      }
      print value
    }
  ' "$descriptor_file"
}

run_codex_profile() {
  codex_profile=$1
  codex_model=$2
  codex_effort=$3
  codex_fast=$4
  codex_schema=$5
  codex_input=$6
  codex_output=$7
  codex_events=$8
  codex_stderr=$9
  codex_timeout=${10}

  set -- "$codex_path" exec \
    --strict-config \
    --ignore-user-config \
    --ignore-rules \
    --ephemeral \
    --skip-git-repo-check \
    --sandbox read-only \
    --cd "$empty_workspace" \
    --color never \
    --json \
    --output-schema "$codex_schema" \
    --output-last-message "$codex_output" \
    --config 'approval_policy="never"' \
    --config 'mcp_servers={}' \
    --config 'developer_instructions="Act only as an isolated Zephyr reviewer. Use only the exact blocks in the user prompt. Never call a tool, open a path, use memory, or modify anything. Return JSON only."' \
    --config "model_reasoning_effort=\"$codex_effort\""

  if [ "$codex_model" != inherit ]; then
    set -- "$@" --model "$codex_model"
  fi
  if [ "$codex_fast" = true ]; then
    set -- "$@" --config 'service_tier="fast"' --enable fast_mode
  else
    set -- "$@" --config 'service_tier="default"' --disable fast_mode
  fi

  if [ "$codex_profile" = full ] || [ "$codex_profile" = portable ]; then
    for config_option in \
      'web_search="disabled"' \
      'include_apps_instructions=false' \
      'include_environment_context=false' \
      'allow_login_shell=false' \
      'apps={ _default = { enabled = false, destructive_enabled = false, open_world_enabled = false } }' \
      'memories={ use_memories = false, generate_memories = false, dedicated_tools = false }'; do
      if [ "$codex_profile" = full ] || [ "${config_option%%=*}" != "$compatibility_omitted_config" ]; then
        set -- "$@" --config "$config_option"
      fi
    done
  fi

  while IFS='=' read -r feature state; do
    [ -n "$feature" ] || continue
    if [ "$feature" != fast_mode ] && list_contains "$isolated_features" "$feature"; then
      set -- "$@" --disable "$feature"
    fi
  done < "$compatibility_features"
  set -- "$@" -

  run_with_timeout "$codex_timeout" "$codex_events" "$codex_stderr" "$codex_input" "$@"
}

run_runtime_smoke() {
  smoke_schema=$work_dir/smoke-schema.json
  smoke_prompt=$work_dir/smoke-prompt.txt
  smoke_output=$work_dir/smoke-last-message.json
  smoke_events=$work_dir/smoke-events.jsonl
  smoke_stderr=$work_dir/smoke-stderr.log
  printf '%s\n' '{"type":"object","additionalProperties":false,"required":[],"properties":{}}' > "$smoke_schema"
  printf '%s\n' 'Return exactly one empty JSON object and do not call tools.' > "$smoke_prompt"

  compatibility_profile=full
  compatibility_omitted_config=none
  smoke_attempt_number=1
  smoke_first_failure=
  while :; do
    rm -f -- "$smoke_output" "$smoke_events" "$smoke_stderr"
    if run_codex_profile "$compatibility_profile" "$execution_model" "$execution_effort" "$execution_fast" "$smoke_schema" "$smoke_prompt" "$smoke_output" "$smoke_events" "$smoke_stderr" "$probe_timeout"; then
      smoke_status=0
    else
      smoke_status=$?
    fi
    smoke_payload=
    if [ -f "$smoke_output" ]; then
      smoke_payload=$(tr -d '[:space:]' < "$smoke_output")
    fi
    if [ "$smoke_status" -eq 0 ] && [ "$smoke_payload" = '{}' ]; then
      return 0
    fi
    if [ "$smoke_status" -eq 0 ]; then
      smoke_status=1
    fi
    if [ "$smoke_status" -eq 124 ]; then
      smoke_category=transport
    else
      smoke_category=$(classify_codex_failure "$smoke_stderr")
    fi
    smoke_hash=$(hash_file "$smoke_stderr")
    smoke_bytes=$(wc -c < "$smoke_stderr" | tr -d ' ')
    smoke_diagnostic="category=$smoke_category status=$smoke_status stderr_sha256=$smoke_hash stderr_bytes=$smoke_bytes"

    if [ "$compatibility_profile" = full ] && [ "$smoke_category" = config ]; then
      if ! compatibility_omitted_config=$(classify_config_option "$smoke_stderr"); then
        echo "zephyr codex dispatch: compatibility runtime smoke full failed without a safe portable configuration fallback; $smoke_diagnostic" >&2
        return 1
      fi
      printf '%s\n' 'zephyr codex dispatch: warning: full Codex isolation profile unsupported; portable profile selected' >&2
      compatibility_profile=portable
      smoke_attempt_number=1
      smoke_first_failure=
      continue
    fi
    if [ "$smoke_attempt_number" -eq 1 ] && retryable_failure "$smoke_category"; then
      smoke_first_failure=$smoke_diagnostic
      sleep 1
      smoke_attempt_number=2
      continue
    fi
    if [ -n "$smoke_first_failure" ]; then
      echo "zephyr codex dispatch: compatibility runtime smoke $compatibility_profile failed after $smoke_attempt_number attempts; first={$smoke_first_failure}; last={$smoke_diagnostic}" >&2
    else
      echo "zephyr codex dispatch: compatibility runtime smoke $compatibility_profile failed without retry; $smoke_diagnostic" >&2
    fi
    return 1
  done
}

probe_compatibility() {
  version_file=$work_dir/codex-version.txt
  version_stderr=$work_dir/codex-version.stderr
  features_raw=$work_dir/codex-features.raw
  features_stderr=$work_dir/codex-features.stderr
  help_file=$work_dir/codex-exec-help.txt
  help_stderr=$work_dir/codex-exec-help.stderr

  run_probe_command version "$version_file" "$version_stderr" "$codex_path" --version || return 1
  run_probe_command features "$features_raw" "$features_stderr" "$codex_path" features list || return 1
  run_probe_command exec-help "$help_file" "$help_stderr" "$codex_path" exec --help || return 1

  for required_option in --strict-config --ignore-user-config --ignore-rules --ephemeral --sandbox --output-schema --output-last-message --json; do
    if ! LC_ALL=C grep -F -q -- "$required_option" "$help_file"; then
      echo "zephyr codex dispatch: compatibility probe found unsupported Codex CLI option $required_option" >&2
      return 1
    fi
  done

  awk '
    NF >= 3 && $1 ~ /^[a-z0-9_]+$/ && $NF ~ /^(true|false)$/ { print $1 "=" $NF }
  ' "$features_raw" | LC_ALL=C sort -u > "$compatibility_features"

  feature_raw_count=$(awk 'NF { count++ } END { print count + 0 }' "$features_raw")
  feature_parsed_count=$(awk 'END { print NR + 0 }' "$compatibility_features")
  feature_ignored_count=$((feature_raw_count - feature_parsed_count))
  if [ "$feature_ignored_count" -gt 0 ]; then
    printf '%s\n' "zephyr codex dispatch: warning: partially unparseable feature output allowed: parsed=$feature_parsed_count ignored=$feature_ignored_count raw_sha256=$(hash_file "$features_raw")" >&2
  fi

  while IFS='=' read -r feature state; do
    [ -n "$feature" ] || continue
    if ! list_contains "$isolated_features" "$feature" && [ "$state" = true ]; then
      printf '%s\n' "zephyr codex dispatch: warning: unknown enabled feature allowed: $feature" >&2
    fi
  done < "$compatibility_features"

  run_runtime_smoke || return 1

  descriptor_payload=$work_dir/compatibility-payload.txt
  {
    printf '%s\n' zephyr-codex-compat-v3
    printf 'binary_sha256=%s\n' "$(hash_file "$codex_path")"
    printf 'version_sha256=%s\n' "$(hash_file "$version_file")"
    printf 'features_sha256=%s\n' "$(hash_file "$compatibility_features")"
    printf 'profile=%s\n' "$compatibility_profile"
    printf 'model_policy_sha256=%s\n' "$execution_policy_sha256"
    printf 'omitted_config=%s\n' "$compatibility_omitted_config"
    command cat "$compatibility_features"
  } > "$descriptor_payload"
  descriptor=$work_dir/compatibility.txt
  descriptor_hash=$(hash_file "$descriptor_payload")
  awk -v descriptor_hash="$descriptor_hash" '
    /^profile=/ {
      print
      print "descriptor_sha256=" descriptor_hash
      next
    }
    { print }
  ' "$descriptor_payload" > "$descriptor"
  chmod 600 "$descriptor"
  mv "$descriptor" "$output"
  printf '{"kind":"probe","output":"%s"}\n' "$output"
}

load_compatibility() {
  require_regular_absolute "$compat" compatibility
  if [ "$(sed -n '1p' "$compat")" != zephyr-codex-compat-v3 ]; then
    echo "zephyr codex dispatch: unsupported compatibility descriptor" >&2
    exit 1
  fi
  if ! expected_binary_hash=$(descriptor_field binary_sha256 "$compat") \
    || ! expected_version_hash=$(descriptor_field version_sha256 "$compat") \
    || ! expected_features_hash=$(descriptor_field features_sha256 "$compat") \
    || ! compatibility_profile=$(descriptor_field profile "$compat") \
    || ! expected_policy_hash=$(descriptor_field model_policy_sha256 "$compat") \
    || ! compatibility_omitted_config=$(descriptor_field omitted_config "$compat") \
    || ! expected_descriptor_hash=$(descriptor_field descriptor_sha256 "$compat"); then
    echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
    exit 1
  fi
  for expected_hash in "$expected_binary_hash" "$expected_version_hash" "$expected_features_hash" "$expected_descriptor_hash"; do
    if [ "${#expected_hash}" -ne 64 ] || printf '%s' "$expected_hash" | LC_ALL=C grep -q '[^0-9a-f]'; then
      echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
      exit 1
    fi
  done
  case "$expected_policy_hash" in
    none) [ "$execution_policy_sha256" = none ] || { echo "zephyr codex dispatch: model policy changed after compatibility probe" >&2; exit 1; } ;;
    *)
      if [ "${#expected_policy_hash}" -ne 64 ] || printf '%s' "$expected_policy_hash" | LC_ALL=C grep -q '[^0-9a-f]' || [ "$execution_policy_sha256" != "$expected_policy_hash" ]; then
        echo "zephyr codex dispatch: model policy changed after compatibility probe" >&2
        exit 1
      fi
      ;;
  esac
  case "$compatibility_profile" in
    full)
      [ "$compatibility_omitted_config" = none ] || {
        echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
        exit 1
      }
      ;;
    portable)
      case "$compatibility_omitted_config" in
        web_search|include_apps_instructions|include_environment_context|allow_login_shell|apps|memories) ;;
        *)
          echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
          exit 1
          ;;
      esac
      ;;
    *)
      echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
      exit 1
      ;;
  esac
  if [ "$(hash_file "$codex_path")" != "$expected_binary_hash" ]; then
    echo "zephyr codex dispatch: Codex binary changed after compatibility probe" >&2
    exit 1
  fi
  awk -F= '
    $1 == "zephyr-codex-compat-v3" || $1 == "binary_sha256" || $1 == "version_sha256" || $1 == "features_sha256" || $1 == "profile" || $1 == "model_policy_sha256" || $1 == "omitted_config" || $1 == "descriptor_sha256" { next }
    $1 !~ /^[a-z0-9_]+$/ || $2 !~ /^(true|false)$/ || NF != 2 { exit 1 }
    seen[$1]++ { exit 1 }
    { print $0 }
  ' "$compat" > "$compatibility_features" || {
    echo "zephyr codex dispatch: malformed compatibility descriptor" >&2
    exit 1
  }
  if [ "$(hash_file "$compatibility_features")" != "$expected_features_hash" ]; then
    echo "zephyr codex dispatch: compatibility descriptor feature hash mismatch" >&2
    exit 1
  fi
  descriptor_payload=$work_dir/loaded-compatibility-payload.txt
  awk '!/^descriptor_sha256=/' "$compat" > "$descriptor_payload"
  if [ "$(hash_file "$descriptor_payload")" != "$expected_descriptor_hash" ]; then
    echo "zephyr codex dispatch: compatibility descriptor hash mismatch" >&2
    exit 1
  fi
}

if [ "$kind" = probe ]; then
  probe_compatibility
  exit 0
fi

load_compatibility

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

invoke_codex() {
  run_codex_profile "$compatibility_profile" "$execution_model" "$execution_effort" "$execution_fast" "$schema_path" "$prompt_file" "$last_message" "$events_pipe" "$stderr_file" "$dispatch_timeout"
}

start_output_recovery() {
  rm -f -- "$events_pipe" "$events_file" "$recovery_stderr_file" "$recovered_message" "$codex_status_file"
  mkfifo "$events_pipe"
  recovery_command='tee "$1" < "$2" | "$3" recover-codex-output --kind "$4" --input - --output "$5" >/dev/null || exit 1
    attempts=0
    while [ ! -s "$6" ] && [ "$attempts" -lt 100 ]; do sleep 0.1; attempts=$((attempts + 1)); done
    [ "$(sed -n "1p" "$6" 2>/dev/null)" = 0 ] || exit 1
    ln "$5" "$7"'
  if command -v setsid >/dev/null 2>&1; then
    setsid sh -c "$recovery_command" sh "$events_file" "$events_pipe" "$zephyr_core_path" "$kind" "$recovered_message" "$codex_status_file" "$output" 2> "$recovery_stderr_file" &
  elif command -v perl >/dev/null 2>&1; then
    perl -MPOSIX=setsid -e 'setsid() or die "setsid: $!\n"; exec @ARGV or die "exec: $!\n"' \
      sh -c "$recovery_command" sh "$events_file" "$events_pipe" "$zephyr_core_path" "$kind" "$recovered_message" "$codex_status_file" "$output" 2> "$recovery_stderr_file" &
  else
    echo "zephyr codex dispatch: setsid or Perl with POSIX::setsid is required for structured output recovery" >&2
    exit 1
  fi
  recovery_pid=$!
}

attempt=1
first_failure=
while :; do
  if [ -e "$last_message" ] || [ -L "$last_message" ]; then
    rm -f -- "$last_message"
  fi
  start_output_recovery
  if invoke_codex; then
    codex_status=0
  else
    codex_status=$?
  fi
  printf '%s\n' "$codex_status" > "$codex_status_file"
  if wait "$recovery_pid"; then
    recovery_status=0
  else
    recovery_status=$?
  fi
  if [ "$codex_status" -eq 0 ]; then
    if [ -s "$last_message" ] && [ -s "$output" ]; then
      if [ "$(hash_file "$last_message")" != "$(hash_file "$output")" ]; then
        echo "zephyr codex dispatch: Codex output and recovered event output differ" >&2
        exit 1
      fi
    elif [ -s "$last_message" ]; then
      chmod 600 "$last_message"
      mv "$last_message" "$output"
    elif [ ! -s "$output" ] || [ "$recovery_status" -ne 0 ]; then
      echo "zephyr codex dispatch: isolated Codex process returned no recoverable structured output" >&2
      exit 1
    fi
    break
  fi

  if [ -e "$output" ] || [ -L "$output" ]; then
    rm -f -- "$output"
  fi

  if [ "$codex_status" -eq 124 ]; then
    failure_category=timeout
  else
    failure_category=$(classify_codex_failure "$stderr_file")
  fi
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

if [ ! -s "$output" ]; then
  echo "zephyr codex dispatch: isolated Codex process returned an empty result" >&2
  exit 1
fi
printf '{"kind":"%s","role":"%s","output":"%s"}\n' "$kind" "$role" "$output"

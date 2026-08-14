#!/bin/sh

set -eu
umask 077

git_timeout=${ZEPHYR_PR_ACQUIRE_TIMEOUT:-120}
case "$git_timeout" in *[!0-9]*|'') fail_early=yes ;; *) fail_early=no ;; esac
[ "$fail_early" = no ] && [ "$git_timeout" -ge 1 ] && [ "$git_timeout" -le 1800 ] || {
  echo "zephyr PR acquisition: ZEPHYR_PR_ACQUIRE_TIMEOUT must be an integer between 1 and 1800" >&2
  exit 1
}

usage() {
  echo "usage: acquire-pr.sh acquire --repository-url URL --base-ref REF --head-ref REF [--base-sha SHA --head-sha SHA] --output FILE" >&2
  echo "       acquire-pr.sh cleanup --metadata FILE" >&2
}

fail() { echo "zephyr PR acquisition: $*" >&2; exit 1; }

run_git() {
  python3 - "$git_timeout" "$@" <<'PY'
import os
import signal
import subprocess
import sys

timeout = int(sys.argv[1])
command = ["git", *sys.argv[2:]]
try:
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    stdout, stderr = process.communicate(timeout=timeout)
except subprocess.TimeoutExpired:
    os.killpg(process.pid, signal.SIGTERM)
    try:
        process.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.communicate()
    print("zephyr PR acquisition: Git command timed out", file=sys.stderr)
    raise SystemExit(1)
if process.returncode != 0:
    print(f"zephyr PR acquisition: Git command failed with exit code {process.returncode}", file=sys.stderr)
    raise SystemExit(1)
sys.stdout.buffer.write(stdout)
PY
}

require_absolute() {
  case "$1" in /*) ;; *) fail "$2 must be absolute" ;; esac
}

reject_symlink_components() {
  checked_path=$1
  remaining=${checked_path#/}
  current=
  while [ -n "$remaining" ]; do
    case "$remaining" in
      */*) component=${remaining%%/*}; remaining=${remaining#*/} ;;
      *) component=$remaining; remaining= ;;
    esac
    [ -n "$component" ] || continue
    case "$component" in .|..) fail "unsafe path component in $checked_path" ;; esac
    current="$current/$component"
    [ ! -L "$current" ] || fail "symlink path component is forbidden: $current"
  done
}

validate_sha() {
  case "$1" in *[!0-9a-fA-F]*|'') fail "$2 must be a 40-character hexadecimal SHA" ;; esac
  [ "${#1}" -eq 40 ] || fail "$2 must be a 40-character hexadecimal SHA"
}

validate_ref() {
  case "$1" in refs/heads/*|refs/pull-requests/*/from|refs/pull-requests/*/merge) ;; *) fail "$2 is not an allowed ref" ;; esac
  case "$1" in -*|*..*|*'//'*) fail "$2 is unsafe" ;; esac
  run_git check-ref-format "$1" >/dev/null 2>&1 || fail "$2 is not a valid Git ref"
}

safe_remove_root() {
  root=$1
  parent=$2
  require_absolute "$root" "acquisition root"
  require_absolute "$parent" "acquisition parent"
  reject_symlink_components "$parent"
  case "$root" in "$parent"/.zephyr-pr-acquire.*) ;; *) fail "refusing unsafe acquisition cleanup target" ;; esac
  [ -d "$root" ] || fail "acquisition root does not exist"
  [ ! -L "$root" ] || fail "acquisition root must not be a symlink"
  rm -rf -- "$root"
}

acquire() {
  repository_url= base_ref= base_sha= head_ref= head_sha= output=
  while [ "$#" -gt 0 ]; do
    option=$1
    [ "$#" -ge 2 ] || { usage; exit 2; }
    value=$2
    shift 2
    case "$option" in
      --repository-url) repository_url=$value ;;
      --base-ref) base_ref=$value ;;
      --base-sha) base_sha=$value ;;
      --head-ref) head_ref=$value ;;
      --head-sha) head_sha=$value ;;
      --output) output=$value ;;
      *) usage; exit 2 ;;
    esac
  done
  [ -n "$repository_url" ] || fail "--repository-url is required"
  case "$repository_url" in -*|http://*@*|https://*@*) fail "repository URL is unsafe" ;; esac
  validate_ref "$base_ref" "base ref"
  validate_ref "$head_ref" "head ref"
  if [ -n "$base_sha" ] && [ -n "$head_sha" ]; then
    validate_sha "$base_sha" "base SHA"
    validate_sha "$head_sha" "head SHA"
    pinned=true
  else
    pinned=false
  fi
  require_absolute "$output" "output"
  [ ! -e "$output" ] && [ ! -L "$output" ] || fail "refusing to overwrite output"
  requested_parent=$(dirname -- "$output")
  [ -d "$requested_parent" ] || fail "output parent must exist"
  [ ! -L "$requested_parent" ] || fail "output parent must not be a symlink"
  output_parent=$(CDPATH= cd -P -- "$requested_parent" && pwd)
  output="$output_parent/$(basename -- "$output")"

  acquisition_root=$(mktemp -d "$output_parent/.zephyr-pr-acquire.XXXXXX")
  repository="$acquisition_root/repository"
  metadata_temp=
  acquisition_complete=no
  cleanup_failure() {
    status=$?
    if [ "$acquisition_complete" != yes ] && [ -d "$acquisition_root" ]; then safe_remove_root "$acquisition_root" "$output_parent" || true; fi
    [ -z "$metadata_temp" ] || rm -f -- "$metadata_temp"
    exit "$status"
  }
  trap cleanup_failure EXIT HUP INT TERM

  export GIT_TERMINAL_PROMPT=0
  run_git init -q "$repository"
  run_git -C "$repository" remote add origin "$repository_url"
  run_git -C "$repository" -c protocol.version=2 fetch -q --no-tags --no-recurse-submodules origin \
    "+$base_ref:refs/zephyr/base" "+$head_ref:refs/zephyr/head"
  resolved_base=$(run_git -C "$repository" rev-parse --verify 'refs/zephyr/base^{commit}')
  resolved_head=$(run_git -C "$repository" rev-parse --verify 'refs/zephyr/head^{commit}')
  if [ "$pinned" = true ]; then
    expected_base=$(printf '%s' "$base_sha" | tr 'A-F' 'a-f')
    expected_head=$(printf '%s' "$head_sha" | tr 'A-F' 'a-f')
    [ "$resolved_base" = "$expected_base" ] || fail "fetched base ref does not match frozen SHA"
    [ "$resolved_head" = "$expected_head" ] || fail "fetched head ref does not match frozen SHA"
  fi
  run_git -C "$repository" -c advice.detachedHead=false checkout -q --detach "$resolved_head"

  metadata_temp=$(mktemp "$output_parent/.zephyr-pr-metadata.XXXXXX")
  python3 - "$metadata_temp" "$acquisition_root" "$repository" "$resolved_base" "$resolved_head" "$pinned" <<'PY'
import json, os, sys
path, root, repository, base_sha, head_sha, pinned = sys.argv[1:]
with open(path, "w", encoding="utf-8") as stream:
    json.dump({"version": 2, "acquisition_root": root, "repository": repository, "base_sha": base_sha, "head_sha": head_sha, "pinned": pinned == "true"}, stream, sort_keys=True)
    stream.write("\n")
os.chmod(path, 0o600)
PY
  mv -- "$metadata_temp" "$output"
  metadata_temp=
  acquisition_complete=yes
  trap - EXIT HUP INT TERM
}

cleanup() {
  [ "$#" -eq 2 ] && [ "$1" = --metadata ] || { usage; exit 2; }
  metadata=$2
  require_absolute "$metadata" "metadata"
  [ -f "$metadata" ] && [ ! -L "$metadata" ] || fail "metadata must be a regular non-symlink file"
  requested_parent=$(dirname -- "$metadata")
  [ ! -L "$requested_parent" ] || fail "metadata parent must not be a symlink"
  parent=$(CDPATH= cd -P -- "$requested_parent" && pwd)
  metadata="$parent/$(basename -- "$metadata")"
  values=$(python3 - "$metadata" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream: data = json.load(stream)
if data["version"] == 2:
    if set(data) != {"version", "acquisition_root", "repository", "base_sha", "head_sha", "pinned"} or not isinstance(data["pinned"], bool): raise SystemExit("invalid acquisition metadata")
elif data["version"] != 1 or set(data) != {"version", "acquisition_root", "repository", "base_sha", "head_sha"}:
    raise SystemExit("invalid acquisition metadata")
for key in ("acquisition_root", "repository", "base_sha", "head_sha"):
    if not isinstance(data[key], str) or "\n" in data[key]: raise SystemExit("invalid acquisition metadata")
print(data["acquisition_root"])
print(data["repository"])
PY
  )
  root=$(printf '%s\n' "$values" | sed -n '1p')
  repository=$(printf '%s\n' "$values" | sed -n '2p')
  [ "$repository" = "$root/repository" ] || fail "metadata repository is outside acquisition root"
  safe_remove_root "$root" "$parent"
}

[ "$#" -ge 1 ] || { usage; exit 2; }
command_name=$1
shift
case "$command_name" in
  acquire)
    acquire "$@"
    ;;
  cleanup)
    cleanup "$@"
    ;;
  *)
    usage
    exit 2
    ;;
esac

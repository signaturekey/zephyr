#!/bin/sh

set -eu

mode=sync
if [ "${1:-}" = "--check" ]; then
  mode=check
elif [ "$#" -ne 0 ]; then
  echo "usage: sh harnesses/opencode/sync-agents.sh [--check]" >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
status=0

mkdir -p "$script_dir/agents"

for role_file in "$repo_root"/roles/*.md; do
  role_name=${role_file##*/}
  role_name=${role_name%.md}
  if [ "$role_name" = reviewer-protocol ]; then
    continue
  fi

  destination="$script_dir/agents/zephyr-$role_name.md"
  temporary=$(mktemp "$script_dir/agents/.zephyr-opencode-agent.XXXXXX")
  {
    printf '%s\n' '---'
    printf 'description: Read-only Zephyr %s reviewer.\n' "$role_name"
    printf '%s\n' 'mode: subagent' 'permission:' "  '*': deny" '---' ''
    cat "$role_file"
  } >"$temporary"

  if [ "$mode" = check ]; then
    if [ ! -f "$destination" ] || ! cmp -s "$temporary" "$destination"; then
      echo "out of sync: ${destination#"$repo_root"/}" >&2
      status=1
    fi
    rm -f "$temporary"
  else
    mv "$temporary" "$destination"
    chmod 600 "$destination"
  fi
done

if [ "$mode" = check ]; then
  exit "$status"
fi

echo "OpenCode agent definitions synchronized."

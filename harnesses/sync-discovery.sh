#!/bin/sh

set -eu

mode=sync
if [ "${1:-}" = "--check" ]; then
  mode=check
elif [ "$#" -ne 0 ]; then
  echo "usage: sh harnesses/sync-discovery.sh [--check]" >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
status=0

sync_file() {
  source_file=$1
  destination_file=$2

  if [ "$mode" = check ]; then
    if [ ! -f "$destination_file" ] || ! cmp -s "$source_file" "$destination_file"; then
      echo "out of sync: ${destination_file#"$repo_root"/}" >&2
      status=1
    fi
    return
  fi

  mkdir -p "$(dirname -- "$destination_file")"
  cp "$source_file" "$destination_file"
}

sync_file "$repo_root/harnesses/codex/discovery/SKILL.md" "$repo_root/.agents/skills/zephyr/SKILL.md"
sync_file "$repo_root/harnesses/codex/discovery/agents/openai.yaml" "$repo_root/.agents/skills/zephyr/agents/openai.yaml"
sync_file "$repo_root/harnesses/claude-code/discovery/SKILL.md" "$repo_root/.claude/skills/zephyr/SKILL.md"

for source_file in "$repo_root"/harnesses/codex/agents/zephyr-*.toml; do
  sync_file "$source_file" "$repo_root/.codex/agents/${source_file##*/}"
done

for source_file in "$repo_root"/harnesses/claude-code/agents/zephyr-*.md; do
  sync_file "$source_file" "$repo_root/.claude/agents/${source_file##*/}"
done

if [ "$mode" = check ]; then
  exit "$status"
fi

echo "Zephyr Codex and Claude discovery files synchronized."
echo "Restart a client session if its agents or skills directory was created after that session started."

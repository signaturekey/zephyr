#!/bin/sh

set -eu

usage() {
  cat >&2 <<'EOF'
usage: sh harnesses/uninstall.sh --codex|--claude|--opencode|--all

Removes only files verified by the manifest installed with the Zephyr harness
package. Modified or unrelated files make the uninstall fail before deletion.
EOF
}

case "${1:-}" in
  --codex)
    uninstall_codex=yes
    uninstall_claude=no
    uninstall_opencode=no
    ;;
  --claude)
    uninstall_codex=no
    uninstall_claude=yes
    uninstall_opencode=no
    ;;
  --opencode)
    uninstall_codex=no
    uninstall_claude=no
    uninstall_opencode=yes
    ;;
  --all)
    uninstall_codex=yes
    uninstall_claude=yes
    uninstall_opencode=yes
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

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

if [ -z "${HOME:-}" ]; then
  echo "HOME is not set" >&2
  exit 1
fi

codex_skills_dir=${ZEPHYR_CODEX_SKILLS_DIR:-"$HOME/.agents/skills"}
codex_agents_dir=${ZEPHYR_CODEX_AGENTS_DIR:-"$HOME/.codex/agents"}
claude_skills_dir=${ZEPHYR_CLAUDE_SKILLS_DIR:-"$HOME/.claude/skills"}
claude_agents_dir=${ZEPHYR_CLAUDE_AGENTS_DIR:-"$HOME/.claude/agents"}
opencode_skills_dir=${ZEPHYR_OPENCODE_SKILLS_DIR:-"$HOME/.config/opencode/skills"}
opencode_agents_dir=${ZEPHYR_OPENCODE_AGENTS_DIR:-"$HOME/.config/opencode/agents"}

require_absolute() {
  absolute_path=$1
  case "$absolute_path" in
    /*) ;;
    *)
      echo "uninstall target must be absolute: $absolute_path" >&2
      exit 1
      ;;
  esac
}

reject_symlink_components() {
  checked_path=$1
  remaining_path=${checked_path#/}
  current_path=

  while [ -n "$remaining_path" ]; do
    case "$remaining_path" in
      */*)
        path_component=${remaining_path%%/*}
        remaining_path=${remaining_path#*/}
        ;;
      *)
        path_component=$remaining_path
        remaining_path=
        ;;
    esac
    if [ -z "$path_component" ]; then
      continue
    fi
    case "$path_component" in
      .|..)
        echo "uninstall target must not contain . or .. components: $checked_path" >&2
        exit 1
        ;;
    esac
    current_path="$current_path/$path_component"
    if [ -L "$current_path" ]; then
      echo "refusing uninstall target with symlink component: $current_path" >&2
      exit 1
    fi
  done
}

require_source() {
  required_source=$1
  if [ ! -f "$required_source" ]; then
    echo "missing uninstall source: ${required_source#"$repo_root"/}" >&2
    exit 1
  fi
}

verify_asset_manifest() {
  manifest_path="$repo_root/harnesses/assets.sha256"
  require_source "$manifest_path"

  if command -v shasum >/dev/null 2>&1; then
    (cd "$repo_root" && shasum -a 256 -c "harnesses/assets.sha256" >/dev/null)
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$repo_root" && sha256sum -c "harnesses/assets.sha256" >/dev/null)
    return
  fi

  echo "cannot verify harness/assets.sha256: shasum or sha256sum is required" >&2
  exit 1
}

check_removal() {
  removal_source=$1
  removal_destination=$2

  reject_symlink_components "$(dirname -- "$removal_destination")"

  if [ -L "$removal_destination" ]; then
    echo "refusing to remove symlink: $removal_destination" >&2
    exit 1
  fi
  if [ -e "$removal_destination" ]; then
    if [ ! -f "$removal_destination" ]; then
      echo "refusing to remove modified or foreign file: $removal_destination" >&2
      echo "remove it manually after reviewing its contents" >&2
      exit 1
    fi
    if [ "$removal_destination" = "$installed_manifest" ]; then
      if [ "$installed_manifest_verified" != yes ]; then
        echo "refusing to remove an unverified manifest: $removal_destination" >&2
        exit 1
      fi
      return
    fi
    if [ "$installed_manifest_verified" = yes ]; then
      source_asset=${removal_source#"$repo_root"/}
      expected_hash=$(manifest_hash "$installed_manifest" "$source_asset")
      if [ -z "$expected_hash" ] || [ "$(hash_file "$removal_destination")" != "$expected_hash" ]; then
        echo "refusing to remove modified or foreign file: $removal_destination" >&2
        echo "remove it manually after reviewing its contents" >&2
        exit 1
      fi
      return
    fi
    if ! cmp -s "$removal_source" "$removal_destination"; then
      echo "refusing to remove modified or foreign file: $removal_destination" >&2
      echo "remove it manually after reviewing its contents" >&2
      exit 1
    fi
  fi
}

hash_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  echo "cannot verify installed manifest: shasum or sha256sum is required" >&2
  exit 1
}

manifest_hash() {
  manifest_file=$1
  manifest_asset=$2
  awk -v asset="$manifest_asset" '$2 == asset { count++; value = $1 } END { if (count == 1) print value }' "$manifest_file"
}

prepare_installed_manifest() {
  installed_skill_root=$1
  anchor_source=$2
  anchor_destination=$3
  installed_manifest="$installed_skill_root/references/assets.sha256"
  installed_manifest_verified=no

  if [ -L "$installed_manifest" ]; then
    echo "refusing to remove symlinked manifest: $installed_manifest" >&2
    exit 1
  fi
  if [ ! -f "$installed_manifest" ]; then
    return
  fi
  if [ -L "$anchor_destination" ] || [ ! -f "$anchor_destination" ]; then
    return
  fi
  anchor_asset=${anchor_source#"$repo_root"/}
  anchor_hash=$(manifest_hash "$installed_manifest" "$anchor_asset")
  if [ -n "$anchor_hash" ] && [ "$(hash_file "$anchor_destination")" = "$anchor_hash" ]; then
    installed_manifest_verified=yes
  fi
}

remove_file() {
  removal_destination=$1
  reject_symlink_components "$(dirname -- "$removal_destination")"
  if [ -f "$removal_destination" ]; then
    rm -f "$removal_destination"
  fi
}

remove_empty_dir() {
  removal_directory=$1
  reject_symlink_components "$removal_directory"
  if [ -d "$removal_directory" ] && [ ! -L "$removal_directory" ]; then
    rmdir "$removal_directory" 2>/dev/null || :
  fi
}

verify_asset_manifest

if [ "$uninstall_codex" = yes ]; then
  require_absolute "$codex_skills_dir"
  require_absolute "$codex_agents_dir"
  codex_skill_root="$codex_skills_dir/zephyr"
  prepare_installed_manifest "$codex_skill_root" "$repo_root/harnesses/codex/SKILL.md" "$codex_skill_root/SKILL.md"

  check_removal "$repo_root/harnesses/codex/SKILL.md" "$codex_skill_root/SKILL.md"
  check_removal "$repo_root/harnesses/codex/dispatch.sh" "$codex_skill_root/scripts/dispatch.sh"
  check_removal "$repo_root/harnesses/codex/discovery/agents/openai.yaml" "$codex_skill_root/agents/openai.yaml"
  check_removal "$repo_root/harnesses/assets.sha256" "$codex_skill_root/references/assets.sha256"
  for source_path in "$repo_root"/roles/*.md; do
    check_removal "$source_path" "$codex_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    check_removal "$source_path" "$codex_skill_root/references/schemas/${source_path##*/}"
  done
  for source_path in "$repo_root"/harnesses/codex/agents/zephyr-*.toml; do
    check_removal "$source_path" "$codex_agents_dir/${source_path##*/}"
    check_removal "$source_path" "$codex_skill_root/references/agents/${source_path##*/}"
  done
fi

if [ "$uninstall_claude" = yes ]; then
  require_absolute "$claude_skills_dir"
  require_absolute "$claude_agents_dir"
  claude_skill_root="$claude_skills_dir/zephyr"
  prepare_installed_manifest "$claude_skill_root" "$repo_root/harnesses/claude-code/SKILL.md" "$claude_skill_root/SKILL.md"

  check_removal "$repo_root/harnesses/claude-code/SKILL.md" "$claude_skill_root/SKILL.md"
  check_removal "$repo_root/harnesses/assets.sha256" "$claude_skill_root/references/assets.sha256"
  for source_path in "$repo_root"/roles/*.md; do
    check_removal "$source_path" "$claude_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    check_removal "$source_path" "$claude_skill_root/references/schemas/${source_path##*/}"
  done
  for source_path in "$repo_root"/harnesses/claude-code/agents/zephyr-*.md; do
    check_removal "$source_path" "$claude_agents_dir/${source_path##*/}"
    check_removal "$source_path" "$claude_skill_root/references/agents/${source_path##*/}"
  done
fi

if [ "$uninstall_opencode" = yes ]; then
  require_absolute "$opencode_skills_dir"
  require_absolute "$opencode_agents_dir"
  opencode_skill_root="$opencode_skills_dir/zephyr"
  prepare_installed_manifest "$opencode_skill_root" "$repo_root/harnesses/opencode/SKILL.md" "$opencode_skill_root/SKILL.md"

  check_removal "$repo_root/harnesses/opencode/SKILL.md" "$opencode_skill_root/SKILL.md"
  check_removal "$repo_root/harnesses/opencode/dispatch.sh" "$opencode_skill_root/scripts/dispatch.sh"
  check_removal "$repo_root/harnesses/assets.sha256" "$opencode_skill_root/references/assets.sha256"
  for source_path in "$repo_root"/roles/*.md; do
    check_removal "$source_path" "$opencode_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    check_removal "$source_path" "$opencode_skill_root/references/schemas/${source_path##*/}"
  done
  for source_path in "$repo_root"/harnesses/opencode/agents/zephyr-*.md; do
    check_removal "$source_path" "$opencode_agents_dir/${source_path##*/}"
    check_removal "$source_path" "$opencode_skill_root/references/agents/${source_path##*/}"
  done
fi

if [ "$uninstall_codex" = yes ]; then
  for source_path in "$repo_root"/harnesses/codex/agents/zephyr-*.toml; do
    remove_file "$codex_agents_dir/${source_path##*/}"
    remove_file "$codex_skill_root/references/agents/${source_path##*/}"
  done
  for source_path in "$repo_root"/roles/*.md; do
    remove_file "$codex_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    remove_file "$codex_skill_root/references/schemas/${source_path##*/}"
  done
  remove_file "$codex_skill_root/agents/openai.yaml"
  remove_file "$codex_skill_root/scripts/dispatch.sh"
  remove_file "$codex_skill_root/references/assets.sha256"
  remove_file "$codex_skill_root/SKILL.md"
  remove_empty_dir "$codex_skill_root/references/roles"
  remove_empty_dir "$codex_skill_root/references/schemas"
  remove_empty_dir "$codex_skill_root/references/agents"
  remove_empty_dir "$codex_skill_root/references"
  remove_empty_dir "$codex_skill_root/agents"
  remove_empty_dir "$codex_skill_root/scripts"
  remove_empty_dir "$codex_skill_root"
  echo "Удалены соответствующие файлы Zephyr для Codex."
fi

if [ "$uninstall_claude" = yes ]; then
  for source_path in "$repo_root"/harnesses/claude-code/agents/zephyr-*.md; do
    remove_file "$claude_agents_dir/${source_path##*/}"
    remove_file "$claude_skill_root/references/agents/${source_path##*/}"
  done
  for source_path in "$repo_root"/roles/*.md; do
    remove_file "$claude_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    remove_file "$claude_skill_root/references/schemas/${source_path##*/}"
  done
  remove_file "$claude_skill_root/references/assets.sha256"
  remove_file "$claude_skill_root/SKILL.md"
  remove_empty_dir "$claude_skill_root/references/roles"
  remove_empty_dir "$claude_skill_root/references/schemas"
  remove_empty_dir "$claude_skill_root/references/agents"
  remove_empty_dir "$claude_skill_root/references"
  remove_empty_dir "$claude_skill_root"
  echo "Удалены соответствующие файлы Zephyr для Claude Code."
fi

if [ "$uninstall_opencode" = yes ]; then
  for source_path in "$repo_root"/harnesses/opencode/agents/zephyr-*.md; do
    remove_file "$opencode_agents_dir/${source_path##*/}"
    remove_file "$opencode_skill_root/references/agents/${source_path##*/}"
  done
  for source_path in "$repo_root"/roles/*.md; do
    remove_file "$opencode_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    remove_file "$opencode_skill_root/references/schemas/${source_path##*/}"
  done
  remove_file "$opencode_skill_root/references/assets.sha256"
  remove_file "$opencode_skill_root/scripts/dispatch.sh"
  remove_file "$opencode_skill_root/SKILL.md"
  remove_empty_dir "$opencode_skill_root/references/roles"
  remove_empty_dir "$opencode_skill_root/references/schemas"
  remove_empty_dir "$opencode_skill_root/references/agents"
  remove_empty_dir "$opencode_skill_root/references"
  remove_empty_dir "$opencode_skill_root/scripts"
  remove_empty_dir "$opencode_skill_root"
  echo "Удалены соответствующие файлы Zephyr для OpenCode."
fi

echo "Начните новую сессию harness, чтобы она забыла удалённые определения skill и agents."

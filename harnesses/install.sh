#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage: sh harnesses/install.sh

Installs Zephyr skills, immutable prompt/schema assets, and custom agents into
the current user's harness directories. Existing different files are never
overwritten.
EOF
}

case "${1:-}" in
  '')
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

if [ "$#" -ne 0 ]; then
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

require_absolute() {
  absolute_path=$1
  case "$absolute_path" in
    /*) ;;
    *)
      echo "installation target must be absolute: $absolute_path" >&2
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
        echo "installation target must not contain . or .. components: $checked_path" >&2
        exit 1
        ;;
    esac
    current_path="$current_path/$path_component"
    if [ -L "$current_path" ]; then
      echo "refusing installation target with symlink component: $current_path" >&2
      exit 1
    fi
  done
}

require_source() {
  required_source=$1
  if [ ! -f "$required_source" ]; then
    echo "missing installation source: ${required_source#"$repo_root"/}" >&2
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

check_destination() {
  check_source=$1
  check_destination_path=$2

  reject_symlink_components "$(dirname -- "$check_destination_path")"

  if [ -L "$check_destination_path" ]; then
    echo "refusing to replace symlink: $check_destination_path" >&2
    exit 1
  fi
  if [ -e "$check_destination_path" ]; then
    if [ ! -f "$check_destination_path" ] || ! cmp -s "$check_source" "$check_destination_path"; then
      echo "refusing to overwrite different file: $check_destination_path" >&2
      echo "move it aside or uninstall the matching Zephyr version first" >&2
      exit 1
    fi
  fi
}

install_file() {
  install_source=$1
  install_destination=$2

  if [ -f "$install_destination" ] && cmp -s "$install_source" "$install_destination"; then
    return
  fi

  install_parent=$(dirname -- "$install_destination")
  reject_symlink_components "$install_parent"
  mkdir -p "$install_parent"
  reject_symlink_components "$install_parent"
  install_temp=$(mktemp "$install_parent/.zephyr-install.XXXXXX")
  if ! cp "$install_source" "$install_temp"; then
    rm -f "$install_temp"
    exit 1
  fi
  if ! chmod 600 "$install_temp"; then
    rm -f "$install_temp"
    exit 1
  fi
  if [ -e "$install_destination" ] || [ -L "$install_destination" ]; then
    rm -f "$install_temp"
    check_destination "$install_source" "$install_destination"
    return
  fi
  if ! ln "$install_temp" "$install_destination"; then
    rm -f "$install_temp"
    echo "refusing to overwrite destination created during install: $install_destination" >&2
    exit 1
  fi
  rm -f "$install_temp"
}

verify_asset_manifest
require_source "$repo_root/harnesses/codex/SKILL.md"
require_source "$repo_root/harnesses/codex/acquire-pr.sh"
require_source "$repo_root/harnesses/codex/dispatch.sh"
require_source "$repo_root/harnesses/codex/discovery/agents/openai.yaml"
for source_path in "$repo_root"/roles/*.md "$repo_root"/schemas/*.json; do
  require_source "$source_path"
done

require_absolute "$codex_skills_dir"
require_absolute "$codex_agents_dir"
codex_skill_root="$codex_skills_dir/zephyr"

  check_destination "$repo_root/harnesses/codex/SKILL.md" "$codex_skill_root/SKILL.md"
  check_destination "$repo_root/harnesses/codex/acquire-pr.sh" "$codex_skill_root/scripts/acquire-pr.sh"
  check_destination "$repo_root/harnesses/codex/dispatch.sh" "$codex_skill_root/scripts/dispatch.sh"
  check_destination "$repo_root/harnesses/codex/discovery/agents/openai.yaml" "$codex_skill_root/agents/openai.yaml"
  check_destination "$repo_root/harnesses/assets.sha256" "$codex_skill_root/references/assets.sha256"
  for source_path in "$repo_root"/roles/*.md; do
    check_destination "$source_path" "$codex_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    check_destination "$source_path" "$codex_skill_root/references/schemas/${source_path##*/}"
  done
  for source_path in "$repo_root"/harnesses/codex/agents/zephyr-*.toml; do
    require_source "$source_path"
    check_destination "$source_path" "$codex_agents_dir/${source_path##*/}"
    check_destination "$source_path" "$codex_skill_root/references/agents/${source_path##*/}"
  done

sh "$script_dir/sync-discovery.sh" --check
  install_file "$repo_root/harnesses/codex/SKILL.md" "$codex_skill_root/SKILL.md"
  install_file "$repo_root/harnesses/codex/acquire-pr.sh" "$codex_skill_root/scripts/acquire-pr.sh"
  chmod 700 "$codex_skill_root/scripts/acquire-pr.sh"
  install_file "$repo_root/harnesses/codex/dispatch.sh" "$codex_skill_root/scripts/dispatch.sh"
  chmod 700 "$codex_skill_root/scripts/dispatch.sh"
  install_file "$repo_root/harnesses/codex/discovery/agents/openai.yaml" "$codex_skill_root/agents/openai.yaml"
  install_file "$repo_root/harnesses/assets.sha256" "$codex_skill_root/references/assets.sha256"
  for source_path in "$repo_root"/roles/*.md; do
    install_file "$source_path" "$codex_skill_root/references/roles/${source_path##*/}"
  done
  for source_path in "$repo_root"/schemas/*.json; do
    install_file "$source_path" "$codex_skill_root/references/schemas/${source_path##*/}"
  done
  for source_path in "$repo_root"/harnesses/codex/agents/zephyr-*.toml; do
    install_file "$source_path" "$codex_agents_dir/${source_path##*/}"
    install_file "$source_path" "$codex_skill_root/references/agents/${source_path##*/}"
  done
echo "Zephyr для Codex установлен в $codex_skill_root и $codex_agents_dir."
echo "Run zephyr-codex doctor before the first experimental review."

echo "Начните новую сессию harness, чтобы загрузились установленный skill и agents."

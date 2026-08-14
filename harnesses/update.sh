#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage: sh harnesses/update.sh [--preflight]

Safely replaces a verified Zephyr harness installation, including packages
created by older manifests. The previous installation is kept in a backup and
restored automatically if publication of the new package fails.
EOF
}

case "${1:-}" in
  '')
    update_codex=yes
    preflight_only=no
    ;;
  --preflight)
    update_codex=yes
    preflight_only=yes
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

if [ "$#" -gt 1 ]; then
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
backup_parent=${ZEPHYR_BACKUP_DIR:-"$HOME/.codex/backups"}

codex_skill_root="$codex_skills_dir/zephyr"

require_absolute() {
  absolute_path=$1
  case "$absolute_path" in
    /*) ;;
    *)
      echo "update path must be absolute: $absolute_path" >&2
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
        echo "update path must not contain . or .. components: $checked_path" >&2
        exit 1
        ;;
    esac
    current_path="$current_path/$path_component"
    if [ -L "$current_path" ]; then
      echo "refusing update path with symlink component: $current_path" >&2
      exit 1
    fi
  done
}

require_regular_file() {
  required_file=$1
  if [ -L "$required_file" ] || [ ! -f "$required_file" ]; then
    echo "missing regular update file: $required_file" >&2
    exit 1
  fi
}

hash_file() {
  hashed_file=$1
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$hashed_file" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$hashed_file" | awk '{print $1}'
    return
  fi
  echo "cannot verify update: shasum or sha256sum is required" >&2
  exit 1
}

verify_source_manifest() {
  if command -v shasum >/dev/null 2>&1; then
    (cd "$repo_root" && shasum -a 256 -c "harnesses/assets.sha256" >/dev/null)
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$repo_root" && sha256sum -c "harnesses/assets.sha256" >/dev/null)
    return
  fi
  echo "cannot verify update source: shasum or sha256sum is required" >&2
  exit 1
}

manifest_hash() {
  manifest_file=$1
  manifest_asset=$2
  awk -v asset="$manifest_asset" '
    $2 == asset { count++; value = $1 }
    END { if (count == 1) print value }
  ' "$manifest_file"
}

verify_installed_file() {
  installed_file=$1
  installed_manifest=$2
  source_asset=$3
  allow_missing=${4:-no}

  require_regular_file "$installed_file"
  expected_hash=$(manifest_hash "$installed_manifest" "$source_asset")
  if [ -z "$expected_hash" ]; then
    if [ "$allow_missing" = yes ]; then
      return
    fi
    echo "installed manifest has no unique entry for: $source_asset" >&2
    exit 1
  fi
  actual_hash=$(hash_file "$installed_file")
  if [ "$actual_hash" != "$expected_hash" ]; then
    echo "refusing to update modified or foreign file: $installed_file" >&2
    exit 1
  fi
}

validate_asset_basename() {
  asset_kind=$1
  asset_name=$2
  case "$asset_name" in
    ''|*/*|.|..)
      echo "unsafe $asset_kind asset name in installed manifest: $asset_name" >&2
      exit 1
      ;;
  esac
}

verify_manifest_group() {
  installed_skill=$1
  installed_manifest=$2
  source_prefix=$3
  destination_group=$4
  required_suffix=$5

  group_count=0
  while IFS= read -r source_asset; do
    [ -n "$source_asset" ] || continue
    asset_name=${source_asset#"$source_prefix"}
    validate_asset_basename "$destination_group" "$asset_name"
    case "$asset_name" in
      *"$required_suffix") ;;
      *)
        echo "unexpected $destination_group asset in installed manifest: $source_asset" >&2
        exit 1
        ;;
    esac
    verify_installed_file "$installed_skill/references/$destination_group/$asset_name" "$installed_manifest" "$source_asset"
    group_count=$((group_count + 1))
  done <<EOF
$(awk -v prefix="$source_prefix" 'index($2, prefix) == 1 { print $2 }' "$installed_manifest")
EOF

  if [ "$group_count" -eq 0 ]; then
    echo "installed manifest has no $destination_group assets" >&2
    exit 1
  fi
}

verify_no_unknown_skill_files() {
  installed_skill=$1
  installed_manifest=$2
  harness_kind=$3

  if find "$installed_skill" -type l -print | grep -q .; then
    echo "refusing to update skill containing symlinks: $installed_skill" >&2
    exit 1
  fi

  while IFS= read -r installed_file; do
    relative_file=${installed_file#"$installed_skill"/}
    case "$relative_file" in
      SKILL.md|references/assets.sha256)
        ;;
      scripts/dispatch.sh|scripts/acquire-pr.sh)
        if [ "$harness_kind" != codex ]; then
          echo "unexpected installed skill file: $installed_file" >&2
          exit 1
        fi
        ;;
      agents/openai.yaml)
        if [ "$harness_kind" != codex ]; then
          echo "unexpected installed skill file: $installed_file" >&2
          exit 1
        fi
        ;;
      references/roles/*.md)
        asset_name=${relative_file#references/roles/}
        expected_asset="roles/$asset_name"
        [ -n "$(manifest_hash "$installed_manifest" "$expected_asset")" ] || {
          echo "installed role is absent from its manifest: $installed_file" >&2
          exit 1
        }
        ;;
      references/schemas/*.json)
        asset_name=${relative_file#references/schemas/}
        expected_asset="schemas/$asset_name"
        [ -n "$(manifest_hash "$installed_manifest" "$expected_asset")" ] || {
          echo "installed schema is absent from its manifest: $installed_file" >&2
          exit 1
        }
        ;;
      references/agents/zephyr-*)
        asset_name=${relative_file#references/agents/}
        expected_asset="harnesses/$harness_kind/agents/$asset_name"
        case "$harness_kind" in
          codex) expected_asset="harnesses/codex/agents/$asset_name" ;;
          codex) expected_asset="harnesses/codex/agents/$asset_name" ;;
          *) echo "unknown harness kind: $harness_kind" >&2; exit 1 ;;
        esac
        [ -n "$(manifest_hash "$installed_manifest" "$expected_asset")" ] || {
          echo "installed agent is absent from its manifest: $installed_file" >&2
          exit 1
        }
        ;;
      *)
        echo "refusing to move unknown file inside installed skill: $installed_file" >&2
        exit 1
        ;;
    esac
  done <<EOF
$(find "$installed_skill" -type f -print | sort)
EOF
}

verify_global_agents() {
  installed_skill=$1
  installed_agents_dir=$2
  required_suffix=$3

  agent_count=0
  for reference_agent in "$installed_skill"/references/agents/zephyr-*"$required_suffix"; do
    if [ ! -f "$reference_agent" ]; then
      continue
    fi
    agent_name=${reference_agent##*/}
    installed_agent="$installed_agents_dir/$agent_name"
    require_regular_file "$installed_agent"
    if ! cmp -s "$reference_agent" "$installed_agent"; then
      echo "refusing to update modified or foreign file: $installed_agent" >&2
      exit 1
    fi
    agent_count=$((agent_count + 1))
  done

  if [ "$agent_count" -eq 0 ]; then
    echo "installed skill has no mirrored agent definitions: $installed_skill" >&2
    exit 1
  fi
}

verify_codex_installation() {
  installed_skill=$codex_skill_root
  installed_manifest="$installed_skill/references/assets.sha256"
  require_regular_file "$installed_manifest"
  verify_installed_file "$installed_skill/SKILL.md" "$installed_manifest" "harnesses/codex/SKILL.md"
  verify_installed_file "$installed_skill/scripts/acquire-pr.sh" "$installed_manifest" "harnesses/codex/acquire-pr.sh" yes
  verify_installed_file "$installed_skill/scripts/dispatch.sh" "$installed_manifest" "harnesses/codex/dispatch.sh"
  verify_installed_file "$installed_skill/agents/openai.yaml" "$installed_manifest" "harnesses/codex/discovery/agents/openai.yaml"
  verify_manifest_group "$installed_skill" "$installed_manifest" "roles/" "roles" ".md"
  verify_manifest_group "$installed_skill" "$installed_manifest" "schemas/" "schemas" ".json"
  verify_manifest_group "$installed_skill" "$installed_manifest" "harnesses/codex/agents/" "agents" ".toml"
  verify_no_unknown_skill_files "$installed_skill" "$installed_manifest" codex
  verify_global_agents "$installed_skill" "$codex_agents_dir" ".toml"
}


surface_is_absent() {
  installed_skill=$1
  installed_agents_dir=$2
  required_suffix=$3

  if [ -e "$installed_skill" ] || [ -L "$installed_skill" ]; then
    return 1
  fi
  for installed_agent in "$installed_agents_dir"/zephyr-*"$required_suffix"; do
    if [ -e "$installed_agent" ] || [ -L "$installed_agent" ]; then
      return 1
    fi
  done
  return 0
}

preflight_staged_agents() {
  staged_agents_dir=$1
  installed_skill=$2
  installed_agents_dir=$3
  required_suffix=$4

  for staged_agent in "$staged_agents_dir"/zephyr-*"$required_suffix"; do
    if [ ! -f "$staged_agent" ]; then
      continue
    fi
    agent_name=${staged_agent##*/}
    installed_agent="$installed_agents_dir/$agent_name"
    old_reference="$installed_skill/references/agents/$agent_name"
    if [ -e "$installed_agent" ] || [ -L "$installed_agent" ]; then
      if [ -L "$installed_agent" ] || [ ! -f "$old_reference" ] || ! cmp -s "$old_reference" "$installed_agent"; then
        echo "refusing to overwrite different file: $installed_agent" >&2
        exit 1
      fi
    fi
  done
}

move_old_surface() {
  installed_skill=$1
  installed_agents_dir=$2
  previous_surface=$3
  required_suffix=$4

  mkdir -p "$previous_surface/agents"
  mv "$installed_skill" "$previous_surface/skill"
  for reference_agent in "$previous_surface/skill"/references/agents/zephyr-*"$required_suffix"; do
    if [ ! -f "$reference_agent" ]; then
      continue
    fi
    agent_name=${reference_agent##*/}
    mv "$installed_agents_dir/$agent_name" "$previous_surface/agents/$agent_name"
  done
}

publish_new_surface() {
  staged_skill=$1
  staged_agents_dir=$2
  installed_skill=$3
  installed_agents_dir=$4
  required_suffix=$5

  mkdir -p "$(dirname -- "$installed_skill")" "$installed_agents_dir"
  mv "$staged_skill" "$installed_skill"
  for staged_agent in "$staged_agents_dir"/zephyr-*"$required_suffix"; do
    if [ ! -f "$staged_agent" ]; then
      continue
    fi
    agent_name=${staged_agent##*/}
    mv "$staged_agent" "$installed_agents_dir/$agent_name"
  done
}

move_new_surface_aside() {
  installed_skill=$1
  installed_agents_dir=$2
  failed_surface=$3
  previous_surface=$4
  source_agents_dir=$5
  required_suffix=$6
  remove_new_agents=$7

  mkdir -p "$failed_surface/agents"
  if [ -d "$installed_skill" ] && [ ! -L "$installed_skill" ] && \
     { [ -d "$previous_surface/skill" ] || [ "$remove_new_agents" = yes ]; }; then
    mv "$installed_skill" "$failed_surface/skill"
  fi

  for previous_agent in "$previous_surface"/agents/zephyr-*; do
    if [ ! -f "$previous_agent" ]; then
      continue
    fi
    agent_name=${previous_agent##*/}
    if [ -f "$installed_agents_dir/$agent_name" ] && [ ! -L "$installed_agents_dir/$agent_name" ]; then
      mv "$installed_agents_dir/$agent_name" "$failed_surface/agents/$agent_name"
    fi
  done

  if [ "$remove_new_agents" = yes ]; then
    for source_agent in "$source_agents_dir"/zephyr-*"$required_suffix"; do
      if [ ! -f "$source_agent" ]; then
        continue
      fi
      agent_name=${source_agent##*/}
      if [ ! -f "$previous_surface/agents/$agent_name" ] && \
         [ -f "$installed_agents_dir/$agent_name" ] && \
         [ ! -L "$installed_agents_dir/$agent_name" ] && \
         cmp -s "$source_agent" "$installed_agents_dir/$agent_name"; then
        mv "$installed_agents_dir/$agent_name" "$failed_surface/agents/$agent_name"
      fi
    done
  fi
}

restore_old_surface() {
  installed_skill=$1
  installed_agents_dir=$2
  previous_surface=$3

  if [ -d "$previous_surface/skill" ] && [ ! -L "$previous_surface/skill" ]; then
    mv "$previous_surface/skill" "$installed_skill"
  fi
  for previous_agent in "$previous_surface"/agents/zephyr-*; do
    if [ ! -f "$previous_agent" ]; then
      continue
    fi
    agent_name=${previous_agent##*/}
    mv "$previous_agent" "$installed_agents_dir/$agent_name"
  done
}

for update_path in "$codex_skills_dir" "$codex_agents_dir" "$backup_parent"; do
  require_absolute "$update_path"
done

codex_was_installed=no

if [ "$update_codex" = yes ]; then
  reject_symlink_components "$codex_skill_root"
  reject_symlink_components "$codex_agents_dir"
  if ! surface_is_absent "$codex_skill_root" "$codex_agents_dir" ".toml"; then
    verify_codex_installation
    codex_was_installed=yes
  fi
fi


case "$backup_parent/" in
  "$codex_skill_root"/*)
    echo "backup directory must be outside installed Zephyr skills: $backup_parent" >&2
    exit 1
    ;;
esac

if [ "$preflight_only" = yes ]; then
  verify_source_manifest
  if [ "$update_codex" = yes ]; then
    preflight_staged_agents "$repo_root/harnesses/codex/agents" "$codex_skill_root" "$codex_agents_dir" ".toml"
  fi
  echo "Zephyr update preflight passed."
  exit 0
fi

reject_symlink_components "$backup_parent"
mkdir -p "$backup_parent"
reject_symlink_components "$backup_parent"
backup_root=$(mktemp -d "$backup_parent/zephyr-update.XXXXXX")

stage_codex_skills="$backup_root/stage/codex-skills"
stage_codex_agents="$backup_root/stage/codex-agents"
mkdir -p "$stage_codex_skills" "$stage_codex_agents"

ZEPHYR_CODEX_SKILLS_DIR="$stage_codex_skills" \
ZEPHYR_CODEX_AGENTS_DIR="$stage_codex_agents" \
  sh "$script_dir/install.sh" >/dev/null

if [ "$update_codex" = yes ]; then
  preflight_staged_agents "$stage_codex_agents" "$codex_skill_root" "$codex_agents_dir" ".toml"
fi
mutation_started=no
publication_started=no
update_complete=no

rollback_update() {
  update_status=$?
  trap - EXIT HUP INT TERM
  if [ "$mutation_started" = yes ] && [ "$update_complete" != yes ]; then
    set +e
    rollback_verified=yes
    echo "Zephyr update failed; restoring the previous installation." >&2
    if [ "$update_codex" = yes ]; then
      move_new_surface_aside "$codex_skill_root" "$codex_agents_dir" "$backup_root/failed-new/codex" "$backup_root/previous/codex" "$repo_root/harnesses/codex/agents" ".toml" "$publication_started"
      restore_old_surface "$codex_skill_root" "$codex_agents_dir" "$backup_root/previous/codex"
      if ! (verify_codex_installation); then
        rollback_verified=no
      fi
    fi
    if [ "$rollback_verified" = yes ]; then
      echo "Previous installation restored. Diagnostic files: $backup_root" >&2
    else
      echo "Automatic rollback could not be verified. Recovery files: $backup_root" >&2
    fi
  fi
  exit "$update_status"
}

trap rollback_update EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
mutation_started=yes

if [ "$update_codex" = yes ] && [ "$codex_was_installed" = yes ]; then
  move_old_surface "$codex_skill_root" "$codex_agents_dir" "$backup_root/previous/codex" ".toml"
fi
publication_started=yes

if [ "$update_codex" = yes ]; then
  publish_new_surface "$stage_codex_skills/zephyr" "$stage_codex_agents" "$codex_skill_root" "$codex_agents_dir" ".toml"
fi

update_complete=yes
trap - EXIT HUP INT TERM

if [ "$codex_was_installed" = no ]; then
  echo "Пакет harness Zephyr установлен."
else
  echo "Пакет harness Zephyr обновлён. Предыдущая установка: $backup_root/previous"
fi
echo "Начните новую сессию harness, чтобы загрузились обновлённые skill и agents."

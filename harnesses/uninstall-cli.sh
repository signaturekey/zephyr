#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
go_command=${GO:-go}

if ! command -v "$go_command" >/dev/null 2>&1; then
  echo "Go command is unavailable: $go_command" >&2
  exit 1
fi

install_dir=$("$go_command" env GOBIN)
if [ -z "$install_dir" ]; then
  go_path=$("$go_command" env GOPATH)
  install_dir=${go_path%%:*}/bin
fi

case "$install_dir" in
  /*) ;;
  *)
    echo "Zephyr install directory must be absolute: $install_dir" >&2
    exit 1
    ;;
esac

core_binary="$install_dir/zephyr"
codex_binary="$install_dir/zephyr-codex"

preflight_binary() {
  binary=$1
  command_name=$2
  if [ -L "$binary" ]; then
    echo "refusing to remove Zephyr CLI symlink: $binary" >&2
    exit 1
  fi
  if [ ! -e "$binary" ]; then
    return 2
  fi
  if [ ! -f "$binary" ]; then
    echo "refusing to remove non-regular Zephyr CLI target: $binary" >&2
    exit 1
  fi
  metadata=$("$go_command" version -m "$binary") || {
    echo "refusing to remove a CLI without readable Go build metadata: $binary" >&2
    exit 1
  }
  command_path=$(printf '%s\n' "$metadata" | awk '$1 == "path" { count++; value = $2 } END { if (count == 1) print value }')
  module_path=$(printf '%s\n' "$metadata" | awk '$1 == "mod" { count++; value = $2 } END { if (count == 1) print value }')
  case "$command_path" in
    */zephyr/cmd/"$command_name") ;;
    *)
      echo "refusing to remove a CLI with foreign command provenance: $binary" >&2
      exit 1
      ;;
  esac
  case "$module_path" in
    */zephyr) ;;
    *)
      echo "refusing to remove a CLI with foreign module provenance: $binary" >&2
      exit 1
      ;;
  esac
  version_output=$("$binary" version) || {
    echo "refusing to remove a CLI with an invalid version command: $binary" >&2
    exit 1
  }
  protocol_version=$(printf '%s\n' "$version_output" | sed -n 's/.*"protocol_version"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | sed -n '1p')
  binary_harness_api=$(printf '%s\n' "$version_output" | sed -n 's/.*"codex_harness_api_version"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | sed -n '1p')
  case "$command_name" in
    zephyr)
      if [ -z "$protocol_version" ]; then
        echo "refusing to remove a CLI with an invalid core protocol: $binary" >&2
        exit 1
      fi
      ;;
    zephyr-codex)
      if [ -z "$protocol_version" ] && [ -z "$binary_harness_api" ]; then
        echo "refusing to remove a CLI with an invalid harness protocol: $binary" >&2
        exit 1
      fi
      ;;
  esac
  binary_module_path=$module_path
}

core_state=0
codex_state=0
core_module=
core_harness_api=
preflight_binary "$core_binary" zephyr || core_state=$?
if [ "$core_state" -eq 0 ]; then
  core_module=$binary_module_path
  core_harness_api=$binary_harness_api
fi
preflight_binary "$codex_binary" zephyr-codex || codex_state=$?
if [ "$codex_state" -eq 0 ]; then
  codex_module=$binary_module_path
fi
if [ "$core_state" -eq 2 ] && [ "$codex_state" -eq 2 ]; then
  echo "Zephyr CLI не установлены: $core_binary, $codex_binary"
  exit 0
fi
if [ "$core_state" -eq 2 ] && [ "$codex_state" -eq 0 ]; then
  echo "refusing to remove an incomplete Zephyr CLI pair" >&2
  exit 1
fi
if [ "$core_state" -eq 0 ] && [ "$codex_state" -eq 2 ]; then
  if [ -n "$core_harness_api" ] && [ "$core_harness_api" -ge 2 ]; then
    echo "refusing to remove an incomplete Zephyr CLI pair" >&2
    exit 1
  fi
  rm "$core_binary"
  echo "Zephyr legacy CLI удалён: $core_binary"
  exit 0
fi
if [ "$core_module" != "$codex_module" ]; then
  echo "refusing to remove CLI binaries from different modules" >&2
  exit 1
fi

rm "$core_binary"
rm "$codex_binary"
echo "Zephyr CLI удалён: $core_binary"
echo "Zephyr Codex driver удалён: $codex_binary"

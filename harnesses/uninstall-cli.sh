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
  expected_path=$2
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
  if ! "$go_command" version -m "$binary" | grep -F "$expected_path" >/dev/null; then
    echo "refusing to remove a CLI that is not built from $expected_path: $binary" >&2
    exit 1
  fi
}

core_state=0
codex_state=0
preflight_binary "$core_binary" github.com/signaturekey/zephyr/cmd/zephyr || core_state=$?
preflight_binary "$codex_binary" github.com/signaturekey/zephyr/cmd/zephyr-codex || codex_state=$?
if [ "$core_state" -eq 2 ] && [ "$codex_state" -eq 2 ]; then
  echo "Zephyr CLI не установлены: $core_binary, $codex_binary"
  exit 0
fi
if [ "$core_state" -eq 2 ] || [ "$codex_state" -eq 2 ]; then
  echo "refusing to remove an incomplete Zephyr CLI pair" >&2
  exit 1
fi

rm "$core_binary"
rm "$codex_binary"
echo "Zephyr CLI удалён: $core_binary"
echo "Zephyr Codex driver удалён: $codex_binary"

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

install_binary="$install_dir/zephyr"
if [ -L "$install_binary" ]; then
  echo "refusing to remove Zephyr CLI symlink: $install_binary" >&2
  exit 1
fi
if [ ! -e "$install_binary" ]; then
  echo "Zephyr CLI is not installed: $install_binary"
  exit 0
fi
if [ ! -f "$install_binary" ]; then
  echo "refusing to remove non-regular Zephyr CLI target: $install_binary" >&2
  exit 1
fi

expected_path=$(cd "$repo_root" && "$go_command" list -m -f '{{.Path}}')/cmd/zephyr
if ! "$go_command" version -m "$install_binary" | grep -F "$expected_path" >/dev/null; then
  echo "refusing to remove a CLI that is not built from $expected_path: $install_binary" >&2
  exit 1
fi

rm "$install_binary"
echo "Removed Zephyr CLI: $install_binary"

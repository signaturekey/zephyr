#!/bin/sh

set -eu
umask 077

usage() {
  echo "usage: build-cli-pair.sh <go> <ldflags> <zephyr-output> <zephyr-codex-output>" >&2
  exit 2
}

[ "$#" -eq 4 ] || usage

go_command=$1
ldflags=$2
core_output=$3
codex_output=$4

for output in "$core_output" "$codex_output"; do
  case "$output" in
    /*|*/*) ;;
    *)
      echo "CLI output must include a parent directory: $output" >&2
      exit 1
      ;;
  esac
  if [ -L "$output" ]; then
    echo "refusing to replace CLI symlink: $output" >&2
    exit 1
  fi
done

mkdir -p "$(dirname -- "$core_output")" "$(dirname -- "$codex_output")"
core_parent=$(CDPATH= cd -- "$(dirname -- "$core_output")" && pwd -P)
codex_parent=$(CDPATH= cd -- "$(dirname -- "$codex_output")" && pwd -P)
if [ "$core_parent" != "$codex_parent" ]; then
  echo "CLI outputs must have the same canonical parent" >&2
  exit 1
fi

core_final="$core_parent/$(basename -- "$core_output")"
codex_final="$core_parent/$(basename -- "$codex_output")"
if [ "$core_final" = "$codex_final" ]; then
  echo "CLI outputs must have distinct names" >&2
  exit 1
fi

validate_destination() {
  destination=$1
  if [ -e "$destination" ] || [ -L "$destination" ]; then
    if [ ! -f "$destination" ] || [ -L "$destination" ]; then
      echo "refusing to replace non-regular CLI: $destination" >&2
      exit 1
    fi
  fi
}

validate_destination "$core_final"
validate_destination "$codex_final"

stage=$(mktemp -d "$core_parent/.zephyr-build.XXXXXX")
backup=
published_core=false
published_codex=false
had_core=false
had_codex=false

cleanup() {
  [ -z "$stage" ] || rm -rf "$stage"
  [ -z "$backup" ] || rm -rf "$backup"
}
trap cleanup EXIT HUP INT TERM

build_one() {
  output=$1
  package=$2
  "$go_command" build -trimpath -ldflags "$ldflags" -o "$output" "$package"
  if [ ! -f "$output" ] || [ ! -x "$output" ]; then
    echo "built CLI is not a regular executable: $output" >&2
    exit 1
  fi
}

build_one "$stage/zephyr" ./cmd/zephyr
if [ "${ZEPHYR_BUILD_PAIR_FAIL_BUILD_SECOND:-}" = "1" ]; then
  echo "injected second CLI build failure" >&2
  exit 1
fi
build_one "$stage/zephyr-codex" ./cmd/zephyr-codex

json_field() {
  field=$1
  printf '%s\n' "$2" | sed -n "s/.*\"$field\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p; s/.*\"$field\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" | sed -n '1p'
}

core_version=$("$stage/zephyr" version)
codex_version=$("$stage/zephyr-codex" version)
for field in version commit dirty codex_harness_api_version; do
  core_value=$(json_field "$field" "$core_version")
  codex_value=$(json_field "$field" "$codex_version")
  if [ -z "$core_value" ] || [ "$core_value" != "$codex_value" ]; then
    echo "built CLI pair disagrees on $field" >&2
    exit 1
  fi
done

backup=$(mktemp -d "$core_parent/.zephyr-backup.XXXXXX")
validate_destination "$core_final"
validate_destination "$codex_final"
if [ -e "$core_final" ]; then
  mv "$core_final" "$backup/zephyr"
  had_core=true
fi
if [ -e "$codex_final" ]; then
  mv "$codex_final" "$backup/zephyr-codex"
  had_codex=true
fi

restore_pair() {
  [ "$published_core" = true ] && rm -f "$core_final"
  [ "$published_codex" = true ] && rm -f "$codex_final"
  [ "$had_core" = true ] && mv "$backup/zephyr" "$core_final"
  [ "$had_codex" = true ] && mv "$backup/zephyr-codex" "$codex_final"
}

if ! mv "$stage/zephyr" "$core_final"; then
  restore_pair
  echo "CLI pair publication failed; previous outputs restored" >&2
  exit 1
fi
published_core=true
if [ "${ZEPHYR_BUILD_PAIR_FAIL_PUBLISH_SECOND:-}" = "1" ] || ! mv "$stage/zephyr-codex" "$codex_final"; then
  restore_pair
  echo "CLI pair publication failed; previous outputs restored" >&2
  exit 1
fi
published_codex=true

verify_published_pair() {
  if [ ! -f "$core_final" ] || [ ! -x "$core_final" ] || [ ! -f "$codex_final" ] || [ ! -x "$codex_final" ]; then
    return 1
  fi
  if ! published_core_version=$("$core_final" version) || ! published_codex_version=$("$codex_final" version); then
    return 1
  fi
  [ "$published_core_version" = "$core_version" ] && [ "$published_codex_version" = "$codex_version" ]
}

if ! verify_published_pair; then
  restore_pair
  echo "CLI pair publication verification failed; previous outputs restored" >&2
  exit 1
fi
rm -rf "$backup"
backup=

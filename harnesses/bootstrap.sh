#!/bin/sh

set -eu
umask 077

usage() {
  cat >&2 <<'EOF'
usage: curl -fsSL <bootstrap-url> | sh -s -- codex|claude|all

Environment overrides:
  ZEPHYR_REPOSITORY_URL  Git repository to clone.
  ZEPHYR_REF             Branch or tag to install. Defaults to master.
EOF
}

surface=${1:-}
case "$surface" in
  codex|claude|all) ;;
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

for required_command in git go make; do
  if ! command -v "$required_command" >/dev/null 2>&1; then
    echo "required command is unavailable: $required_command" >&2
    exit 1
  fi
done

repository_url=${ZEPHYR_REPOSITORY_URL:-ssh://git@stash.msk.avito.ru:7999/~ydkozhemyakin/zephyr.git}
repository_ref=${ZEPHYR_REF:-master}
temporary_parent=${TMPDIR:-/tmp}
temporary_parent=${temporary_parent%/}
bootstrap_root=$(mktemp -d "$temporary_parent/zephyr-bootstrap.XXXXXX")

cleanup() {
  case "$bootstrap_root" in
    "$temporary_parent"/zephyr-bootstrap.*)
      rm -rf "$bootstrap_root"
      ;;
    *)
      echo "refusing to remove unexpected bootstrap directory: $bootstrap_root" >&2
      ;;
  esac
}

trap cleanup EXIT HUP INT TERM

git clone --quiet --depth 1 --branch "$repository_ref" "$repository_url" "$bootstrap_root/repository"
make -C "$bootstrap_root/repository" "install-$surface"

echo "Installed Zephyr CLI and $surface harness package."
echo "Start a new harness session so it loads the installed skill and agents."

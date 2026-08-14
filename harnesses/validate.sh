#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

sh "$script_dir/sync-discovery.sh" --check
sh "$script_dir/test-acquire-pr.sh"
python3 "$script_dir/validate_harnesses.py"

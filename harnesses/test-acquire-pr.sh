#!/bin/sh

set -eu
umask 077

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper="$script_dir/codex/acquire-pr.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/zephyr-acquire-test.XXXXXX")

cleanup_test() {
  case "$test_root" in
    "${TMPDIR:-/tmp}"/zephyr-acquire-test.*) rm -rf -- "$test_root" ;;
    *) echo "refusing unsafe test cleanup: $test_root" >&2 ;;
  esac
}
trap cleanup_test EXIT HUP INT TERM

fail() {
  echo "acquire-pr test failed: $*" >&2
  exit 1
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

source_repo="$test_root/source"
origin_repo="$test_root/origin.git"
mkdir "$source_repo"
git -C "$source_repo" init -q
git -C "$source_repo" config user.name "Zephyr Test"
git -C "$source_repo" config user.email "zephyr@example.invalid"
printf '%s\n' base >"$source_repo/value.txt"
git -C "$source_repo" add value.txt
git -C "$source_repo" commit -q -m base
base_sha=$(git -C "$source_repo" rev-parse HEAD)
git -C "$source_repo" branch -M main
git -C "$source_repo" checkout -q -b feature
printf '%s\n' head >"$source_repo/value.txt"
git -C "$source_repo" commit -qam head
head_sha=$(git -C "$source_repo" rev-parse HEAD)
git clone -q --bare "$source_repo" "$origin_repo"

output="$test_root/acquisition.json"
"$helper" acquire \
  --repository-url "$origin_repo" \
  --base-ref refs/heads/main \
  --base-sha "$base_sha" \
  --head-ref refs/heads/feature \
  --head-sha "$head_sha" \
  --output "$output"

repository=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$output")
recorded_base=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["base_sha"])' "$output")
recorded_head=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["head_sha"])' "$output")
recorded_pinned=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pinned"])' "$output")
[ "$recorded_base" = "$base_sha" ] || fail "base SHA mismatch"
[ "$recorded_head" = "$head_sha" ] || fail "head SHA mismatch"
[ "$recorded_pinned" = "True" ] || fail "provider-pinned acquisition was not marked pinned"
[ "$(git -C "$repository" rev-parse HEAD)" = "$head_sha" ] || fail "checkout is not pinned to head SHA"
expect_failure git -C "$repository" symbolic-ref -q HEAD

best_effort_output="$test_root/best-effort-acquisition.json"
"$helper" acquire \
  --repository-url "$origin_repo" \
  --base-ref refs/heads/main \
  --head-ref refs/heads/feature \
  --output "$best_effort_output"

best_effort_repository=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$best_effort_output")
best_effort_base=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["base_sha"])' "$best_effort_output")
best_effort_head=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["head_sha"])' "$best_effort_output")
best_effort_pinned=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pinned"])' "$best_effort_output")
[ "$best_effort_base" = "$base_sha" ] || fail "best-effort base SHA mismatch"
[ "$best_effort_head" = "$head_sha" ] || fail "best-effort head SHA mismatch"
[ "$best_effort_pinned" = "False" ] || fail "best-effort acquisition was not marked unpinned"
[ "$(git -C "$best_effort_repository" rev-parse HEAD)" = "$head_sha" ] || fail "best-effort checkout is not at fetched head SHA"
expect_failure git -C "$best_effort_repository" symbolic-ref -q HEAD

partial_output="$test_root/partial-sha.json"
"$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha "$base_sha" \
  --head-ref refs/heads/feature --output "$partial_output"
partial_pinned=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pinned"])' "$partial_output")
[ "$partial_pinned" = "False" ] || fail "partial SHA input was not treated as best-effort"

expect_failure "$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha invalid \
  --head-ref refs/heads/feature --head-sha "$head_sha" --output "$test_root/invalid.json"
[ ! -e "$test_root/invalid.json" ] || fail "invalid SHA created output"

missing_sha=0000000000000000000000000000000000000000
expect_failure "$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha "$base_sha" \
  --head-ref refs/heads/feature --head-sha "$missing_sha" --output "$test_root/missing.json"
[ ! -e "$test_root/missing.json" ] || fail "missing commit created output"

expect_failure env ZEPHYR_PR_ACQUIRE_TIMEOUT=0 "$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha "$base_sha" \
  --head-ref refs/heads/feature --head-sha "$head_sha" --output "$test_root/timeout.json"
[ ! -e "$test_root/timeout.json" ] || fail "invalid timeout created output"

existing="$test_root/existing.json"
printf '%s\n' sentinel >"$existing"
expect_failure "$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha "$base_sha" \
  --head-ref refs/heads/feature --head-sha "$head_sha" --output "$existing"
[ "$(cat "$existing")" = sentinel ] || fail "existing output was overwritten"

mkdir "$test_root/real-parent"
ln -s "$test_root/real-parent" "$test_root/link-parent"
expect_failure "$helper" acquire \
  --repository-url "$origin_repo" --base-ref refs/heads/main --base-sha "$base_sha" \
  --head-ref refs/heads/feature --head-sha "$head_sha" --output "$test_root/link-parent/output.json"

tampered="$test_root/tampered.json"
python3 -c 'import json,sys; data=json.load(open(sys.argv[1])); data["acquisition_root"]="/tmp/not-zephyr-acquisition"; json.dump(data,open(sys.argv[2],"w"))' "$output" "$tampered"
expect_failure "$helper" cleanup --metadata "$tampered"
[ -d "$repository" ] || fail "tampered cleanup removed acquisition"

"$helper" cleanup --metadata "$output"
[ ! -e "$repository" ] || fail "cleanup left repository"
expect_failure "$helper" cleanup --metadata "$output"

"$helper" cleanup --metadata "$best_effort_output"
[ ! -e "$best_effort_repository" ] || fail "cleanup left best-effort repository"
expect_failure "$helper" cleanup --metadata "$best_effort_output"

"$helper" cleanup --metadata "$partial_output"
expect_failure "$helper" cleanup --metadata "$partial_output"

echo "acquire-pr tests passed"

#!/usr/bin/env bash

set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly checker="$script_dir/check-whitespace.sh"
readonly test_repo="$(mktemp -d)"
trap 'rm -rf "$test_repo"' EXIT

git init --quiet --initial-branch=main "$test_repo"
git -C "$test_repo" config commit.gpgsign false
git -C "$test_repo" config user.name "CI Test"
git -C "$test_repo" config user.email "ci-test@example.invalid"

printf 'clean\n' >"$test_repo/fixture.txt"
git -C "$test_repo" add fixture.txt
git -C "$test_repo" commit --quiet -m "clean root"
readonly main_head="$(git -C "$test_repo" rev-parse HEAD)"

(
	cd "$test_repo"
	EVENT_NAME=push REF_TYPE=tag BASE_SHA=0000000000000000000000000000000000000000 HEAD_SHA="$main_head" MAIN_REF=refs/heads/main bash "$checker"
)

git -C "$test_repo" switch --quiet -c clean-side
printf 'clean side\n' >"$test_repo/side.txt"
git -C "$test_repo" add side.txt
git -C "$test_repo" commit --quiet -m "clean side"
readonly side_head="$(git -C "$test_repo" rev-parse HEAD)"

if (
	cd "$test_repo"
	EVENT_NAME=push REF_TYPE=tag BASE_SHA=0000000000000000000000000000000000000000 HEAD_SHA="$side_head" MAIN_REF=refs/heads/main bash "$checker" >/dev/null 2>&1
); then
	echo "tag outside main unexpectedly passed" >&2
	exit 1
fi

printf 'trailing whitespace \n' >"$test_repo/fixture.txt"
git -C "$test_repo" add fixture.txt
git -C "$test_repo" commit --quiet -m "bad whitespace"
readonly bad_head="$(git -C "$test_repo" rev-parse HEAD)"

if (
	cd "$test_repo"
	EVENT_NAME=pull_request REF_TYPE=branch BASE_SHA="$side_head" HEAD_SHA="$bad_head" bash "$checker" >/dev/null 2>&1
); then
	echo "range whitespace error unexpectedly passed" >&2
	exit 1
fi

if (
	cd "$test_repo"
	EVENT_NAME=workflow_dispatch REF_TYPE=branch BASE_SHA=0000000000000000000000000000000000000000 HEAD_SHA="$bad_head" bash "$checker" >/dev/null 2>&1
); then
	echo "zero-base tree whitespace error unexpectedly passed" >&2
	exit 1
fi

git -C "$test_repo" switch --quiet main
printf 'bad tag whitespace \n' >"$test_repo/tag.txt"
git -C "$test_repo" add tag.txt
git -C "$test_repo" commit --quiet -m "bad tag whitespace"
readonly bad_tag_head="$(git -C "$test_repo" rev-parse HEAD)"

if (
	cd "$test_repo"
	EVENT_NAME=push REF_TYPE=tag BASE_SHA=0000000000000000000000000000000000000000 HEAD_SHA="$bad_tag_head" MAIN_REF=refs/heads/main bash "$checker" >/dev/null 2>&1
); then
	echo "tag tree whitespace error unexpectedly passed" >&2
	exit 1
fi

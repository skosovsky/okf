#!/usr/bin/env bash

set -euo pipefail

readonly zero_sha="0000000000000000000000000000000000000000"
readonly empty_tree="$(git hash-object -t tree /dev/null)"
readonly event_name="${EVENT_NAME:?EVENT_NAME is required}"
readonly ref_type="${REF_TYPE:-}"
readonly base_sha="${BASE_SHA:-}"
readonly head_sha="${HEAD_SHA:?HEAD_SHA is required}"
readonly main_ref="${MAIN_REF:-refs/remotes/origin/main}"

git diff --check

if [[ "$event_name" == "push" && "$ref_type" == "tag" ]]; then
	if ! git show-ref --verify --quiet "$main_ref"; then
		echo "required main ref is unavailable: $main_ref" >&2
		exit 1
	fi
	if ! git merge-base --is-ancestor "$head_sha" "$main_ref"; then
		echo "version tags must point to a commit reachable from main" >&2
		exit 1
	fi
	git diff --check "$empty_tree" "$head_sha"
	exit 0
fi

if [[ -n "$base_sha" && "$base_sha" != "$zero_sha" ]] && git cat-file -e "$base_sha^{commit}" 2>/dev/null; then
	git diff --check "$base_sha" "$head_sha"
	exit 0
fi

git diff --check "$empty_tree" "$head_sha"

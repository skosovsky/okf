#!/bin/sh
# Export a reviewable, offline demo from the repository's source-linked bundle.
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/build-viewer-demo.sh OUTPUT.html" >&2
  exit 2
fi

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=$1
case "$output" in
  /*) ;;
  *) output="$(pwd)/$output" ;;
esac
if [ -e "$output" ] || [ -L "$output" ]; then
  echo "output already exists: $output" >&2
  exit 1
fi
if [ ! -d "$(dirname -- "$output")" ]; then
  echo "output parent does not exist: $output" >&2
  exit 1
fi

cd "$repo"
go run ./cmd/okf validate --path knowledge --spec auto --strict --check-links --check-orphans --as-of 2026-09-26 --max-warnings=0
go run ./cmd/okf view knowledge --output "$output" --spec auto --temporal-profile date-3fcbb9f --as-of 2026-09-26
echo "prepared offline demo: $output"

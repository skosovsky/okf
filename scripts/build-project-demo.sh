#!/bin/sh
# Export the localized, fictional project example as one offline HTML file.
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: scripts/build-project-demo.sh en|ru OUTPUT.html" >&2
  exit 2
fi
language=$1
case "$language" in
  en|ru) ;;
  *) echo "unsupported demo language: $language (want en or ru)" >&2; exit 2 ;;
esac
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=$2
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
bundle="examples/project-knowledge/$language"
go run ./cmd/okf validate --path "$bundle" --spec 0.3 --strict --check-links --check-orphans --as-of 2026-11-27 --max-warnings=1
go run ./cmd/okf view "$bundle" --output "$output" --spec 0.3 --lang "$language" --as-of 2026-11-27
echo "prepared $language project demo: $output"

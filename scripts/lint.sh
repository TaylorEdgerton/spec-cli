#!/usr/bin/env sh
set -e

command -v go >/dev/null 2>&1 || { echo "lint: go not on PATH"; exit 1; }
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "lint: gofmt needed on:"
  echo "$unformatted"
  exit 1
fi
go vet -tags="grammar_subset grammar_subset_go grammar_subset_python grammar_subset_javascript grammar_subset_typescript grammar_subset_tsx" ./...
echo "lint: gofmt + go vet clean"

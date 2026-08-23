#!/usr/bin/env sh
set -e

command -v go >/dev/null 2>&1 || { echo "lint: go not on PATH"; exit 1; }
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "lint: gofmt needed on:"
  echo "$unformatted"
  exit 1
fi
go vet ./...
echo "lint: gofmt + go vet clean"

#!/usr/bin/env sh
set -e
./scripts/lint.sh
go test ./...

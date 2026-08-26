#!/usr/bin/env sh
set -e
./scripts/lint.sh
go test -tags="grammar_subset grammar_subset_go" ./...

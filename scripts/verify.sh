#!/usr/bin/env sh
set -e
./scripts/lint.sh
go test -tags="grammar_subset grammar_subset_go grammar_subset_python grammar_subset_javascript grammar_subset_typescript grammar_subset_tsx" ./...

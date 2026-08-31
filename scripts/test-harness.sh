#!/usr/bin/env sh
# mem-x test harness: the full quality + integration + fuzz suite.
# Run via: make harness   (or directly: scripts/test-harness.sh)
set -eu
cd "$(dirname "$0")/.."

echo "==> [1/6] formatting (gofmt on cmd/ internal/)"
if [ -n "$(gofmt -l cmd internal)" ]; then
    echo "gofmt needed on:"; gofmt -l cmd internal
    exit 1
fi

echo "==> [2/6] static analysis (go vet)"
go vet ./...

echo "==> [3/6] dependency gate"
scripts/check-stdlib.sh

echo "==> [4/6] unit + integration tests with race detector"
go test -race -cover ./...

echo "==> [5/6] benchmarks (baseline)"
go test -run '^$' -bench . -benchmem ./internal/store ./internal/resp ./internal/command

echo "==> [6/6] fuzz: RESP parser (5s)"
go test -fuzz=FuzzReadCommand -fuzztime=5s ./internal/resp

echo "==> harness passed"

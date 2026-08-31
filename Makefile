# mem-x build tooling.
#
# All Go caches are pinned to workspace-local directories so builds are
# incremental and never hit read-only default cache paths:
#   GOCACHE    - compiled artifacts
#   GOMODCACHE - downloaded modules
GOCACHE ?= $(CURDIR)/.gocache
GOMODCACHE ?= $(CURDIR)/.gomodcache
GOPATH ?= $(CURDIR)/.gopath
export GOCACHE GOMODCACHE GOPATH

BIN := mem-x

.PHONY: build classify build-all run test vet check fmt bench harness fuzz clean

## build: compile the server binary (static, CGO disabled)
build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/mem-x

classify: ## Build the classifier tool and show the registry
	CGO_ENABLED=0 go build -trimpath -o memx-classify ./cmd/memx-classify
	./memx-classify -registry

build-all: build ## Build all binaries
	CGO_ENABLED=0 go build -trimpath -o memx-classify ./cmd/memx-classify

## run: build and start the server on :6379
run: build
	./$(BIN)

## test: run all tests with the race detector
test:
	go test -race ./...

## vet: static analysis
vet:
	go vet ./...

## fmt: fail if any of our code (cmd/ internal/) is not gofmt-clean
fmt:
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt needed on:"; gofmt -l cmd internal; exit 1; }

## bench: run all benchmarks
bench:
	go test -bench=. -benchmem ./...

## harness: full quality gate + integration + fuzz suite
harness:
	scripts/test-harness.sh

## check: quick quality gate (fmt + vet + race tests + dependency gate)
check: fmt vet test
	scripts/check-stdlib.sh

## fuzz: run the RESP parser fuzz target
fuzz:
	go test -fuzz=FuzzReadCommand -fuzztime=10s ./internal/resp

## clean: remove binaries and local caches
clean:
	rm -f $(BIN) $(BIN).exe memx-classify
	rm -rf .gocache .gomodcache

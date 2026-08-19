# dopdb — common tasks.
#
# dopdb binds KVRocks directly (no Store abstraction), so every Go package needs
# the redis client + CBOR modules. Unit tests (api/config/httpserve, plus the
# query-engine and codec tests) run WITHOUT a database; integration tests
# self-skip unless DOPDB_TEST_KVROCKS_URI points at a running KVRocks. Any
# Redis-protocol server works for the test suite — dopdb uses only the common
# command set — but KVRocks is what the production layout is designed for.
#
# The TypeScript implementation (an equivalent of the Go one) lives in ts.

GO ?= go
export GOFLAGS = -mod=mod

.PHONY: help test test-kvrocks bench vet fmt fmt-check build tidy ts ts-test ts-typecheck clean

help:
	@echo "make test          - go test ./...  (integration tests skip without DOPDB_TEST_KVROCKS_URI)"
	@echo "make test-kvrocks  - run integration + conformance tests against DOPDB_TEST_KVROCKS_URI"
	@echo "make bench         - query-engine benchmarks (needs DOPDB_TEST_KVROCKS_URI)"
	@echo "make vet           - go vet ./..."
	@echo "make fmt           - gofmt -w ."
	@echo "make fmt-check     - fail if anything is unformatted"
	@echo "make build         - go build ./..."
	@echo "make tidy          - go mod tidy"
	@echo "make ts            - build the TypeScript implementation (ts)"
	@echo "make ts-test       - run the TypeScript test suite"
	@echo "make ts-typecheck  - strict typecheck the TypeScript implementation"

test:
	$(GO) test -count=1 ./...

test-kvrocks:
	@if [ -z "$(DOPDB_TEST_KVROCKS_URI)" ]; then echo "set DOPDB_TEST_KVROCKS_URI=redis://localhost:6666"; exit 1; fi
	$(GO) test -count=1 -run 'Integration|Conformance' -v ./...

bench:
	@if [ -z "$(DOPDB_TEST_KVROCKS_URI)" ]; then echo "set DOPDB_TEST_KVROCKS_URI=redis://localhost:6666"; exit 1; fi
	$(GO) test -run XXX -bench . -benchmem -benchtime=5x .

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

build:
	$(GO) build ./...

tidy:
	$(GO) mod tidy

ts:
	cd ts && npm install --no-audit --no-fund && npm run build

ts-test:
	cd ts && npm test

ts-typecheck:
	cd ts && npm run typecheck

clean:
	$(GO) clean
	rm -f *.test *.out

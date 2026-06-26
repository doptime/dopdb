# dopdb — common tasks.
#
# dopdb binds the MongoDB driver directly (no Store abstraction), so every Go
# package needs the driver module. Unit tests (api/config/httpserve) run WITHOUT
# a database; integration tests self-skip unless DOPDB_TEST_MONGO_URI points at a
# running MongoDB. The watch/change-stream tests additionally require the server
# to be a replica set.
#
# The TypeScript implementation (an equivalent of the Go one) lives in ts.

GO ?= go
export GOFLAGS = -mod=mod

.PHONY: help test test-mongo vet fmt fmt-check build tidy ts ts-test ts-typecheck clean

help:
	@echo "make test          - go test ./...  (integration tests skip without DOPDB_TEST_MONGO_URI)"
	@echo "make test-mongo    - run integration tests against DOPDB_TEST_MONGO_URI (replica set for watch)"
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

test-mongo:
	@if [ -z "$(DOPDB_TEST_MONGO_URI)" ]; then echo "set DOPDB_TEST_MONGO_URI=mongodb://...  (replica set required for watch)"; exit 1; fi
	$(GO) test -count=1 -run Integration -v ./...

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

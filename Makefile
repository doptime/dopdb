# dopdb — common tasks. The no-driver packages compile & test without the
# MongoDB driver (and thus without network); mongostore needs the driver.

GO       ?= go
NODRIVER  = . ./api ./httpserve ./config ./memstore
export GOFLAGS = -mod=mod

.PHONY: help test test-mongo test-all vet fmt fmt-check build build-mongo wasm ts tidy clean

help:
	@echo "make test        - run the no-driver test suite (34 tests)"
	@echo "make test-mongo  - run mongostore integration test (needs DOPTIME_TEST_MONGO_URI)"
	@echo "make test-all    - run every test (needs mongo driver + MongoDB)"
	@echo "make vet         - go vet on no-driver packages"
	@echo "make fmt         - gofmt -w on the tree"
	@echo "make fmt-check   - fail if anything is unformatted"
	@echo "make build       - build no-driver packages"
	@echo "make build-mongo - build incl mongostore (needs: go get go.mongodb.org/mongo-driver/v2)"
	@echo "make wasm        - compile the WASM module into clients/ts/wasm/"
	@echo "make ts          - build the TypeScript client (implies wasm)"
	@echo "make tidy        - go mod tidy"

test:
	$(GO) test -count=1 $(NODRIVER)

test-mongo:
	$(GO) test -count=1 -run TestMongoContract -v ./mongostore

test-all:
	$(GO) test -count=1 ./...

vet:
	$(GO) vet $(NODRIVER)

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

build:
	$(GO) build $(NODRIVER)

build-mongo:
	$(GO) build ./...

WASMOUT = clients/ts/wasm

wasm:
	GOOS=js GOARCH=wasm $(GO) build -o $(WASMOUT)/dopdb.wasm ./wasm
	@goroot=$$($(GO) env GOROOT); \
	for c in "$$goroot/lib/wasm/wasm_exec.js" "$$goroot/misc/wasm/wasm_exec.js" /usr/share/go-*/misc/wasm/wasm_exec.js /usr/local/go/misc/wasm/wasm_exec.js; do \
	  if [ -f $$c ]; then cp $$c $(WASMOUT)/wasm_exec.js; echo "copied wasm_exec.js from $$c"; break; fi; \
	done; \
	echo "wasm -> $(WASMOUT)/dopdb.wasm (keeping committed wasm_exec.js if none found)"

ts: wasm
	cd clients/ts && npm install --no-audit --no-fund && npm run build

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean
	rm -f *.test *.out

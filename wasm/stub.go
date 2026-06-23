//go:build !(js && wasm)

// This stub exists so `go build ./...` succeeds on non-wasm platforms. The real
// WASM bridge in main.go only compiles under GOOS=js GOARCH=wasm. Build the wasm
// module with: GOOS=js GOARCH=wasm go build -o dopdb.wasm ./wasm
package main

func main() {}

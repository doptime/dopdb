package httpserve

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doptime/dopdb/config"
)

// ServeWithHandle promises to shut down gracefully. It used to close only the
// HTTP listener and leave the whole KVRocks connection pool behind, which leaks
// on every restart of a long-lived process that manages its own lifecycle.
func TestServeWithHandleClosesDatasources(t *testing.T) {
	uri := os.Getenv("DOPDB_TEST_KVROCKS_URI")
	if uri == "" {
		t.Skip("set DOPDB_TEST_KVROCKS_URI to run")
	}
	cfg := &config.Config{
		HTTP: config.HTTPConfig{Addr: "127.0.0.1:0", JWTSecret: "s"},
		Kvrocks: []config.KvrocksSource{
			{Name: "default", URI: uri, Namespace: "dopdb_close_test"},
		},
	}
	srv, err := ServeWithHandle(cfg)
	if err != nil {
		t.Fatalf("ServeWithHandle: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Closing twice must surface the already-closed client rather than silently
	// succeeding — that is the evidence the pool was really handed back.
	if err := srv.Close(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("second Close = %v; expected an already-closed error proving the pool was released", err)
	}
}

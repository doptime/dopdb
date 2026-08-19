package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/config"
	"github.com/doptime/dopdb/httpserve"
)

type serverFlags struct {
	kvrocks   string
	addr      string
	namespace string
	users     int
	docs      int
	secret    string
}

var srv serverFlags

// note is the owner-scoped Hash document the load scenarios hit. It is
// deliberately small and flat: the goal is to measure the dopdb pipeline
// (JWT + gate + CBOR + KVRocks), not document shaping.
type note struct {
	ID    string `json:"_id"`
	Owner string `json:"owner"`
	Title string `json:"title"`
	Tag   string `json:"tag"`
	Seq   int    `json:"seq"`
	At    int64  `json:"at"`
}

// runServe boots the server, seeds the owner-scoped collection, and blocks
// until interrupted. The seed is what makes the FIND scenarios meaningful:
// every user owns `docs` rows, so a scoped FIND scans a real workload.
func runServe(args []string) int {
	fs := serveFlags()
	_ = fs.Parse(args)

	if srv.users < 1 {
		log.Fatal("--users must be >= 1")
	}
	if srv.docs < 1 {
		log.Fatal("--docs-per-user must be >= 1")
	}

	cfg := &config.Config{
		HTTP: config.HTTPConfig{
			Addr:         srv.addr,
			JWTSecretEnv: "STRESS_JWT_SECRET", // not used: secret is set directly
			JWTSecret:    srv.secret,
		},
		Kvrocks: []config.KvrocksSource{{
			Name:      "default",
			URI:       srv.kvrocks,
			Namespace: srv.namespace,
		}},
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	// Collections, exposed + authorized exactly as an application would.
	// .HttpOn() with no args is the debug default (all on); the stress run
	// exercises every command class, so that is what it wants.
	n := dopdb.New[string, *note](dopdb.WithCollection("notes"))
	n.HttpOn()
	dopdb.SetOwnerScope("notes", "owner", "uid")

	dopdb.NewString[string](dopdb.WithCollection("kv")).HttpOn()
	dopdb.NewList[string, string](dopdb.WithCollection("queue")).HttpOn()
	dopdb.NewSet[string](dopdb.WithCollection("tags")).HttpOn()
	dopdb.NewZSet[string](dopdb.WithCollection("board")).HttpOn()

	handle, err := httpserve.ServeWithHandle(cfg)
	if err != nil {
		log.Fatalf("serve: %v", err)
	}
	log.Printf("dopdb stress server on %s (kvrocks=%s ns=%s users=%d docs/user=%d)",
		srv.addr, srv.kvrocks, srv.namespace, srv.users, srv.docs)

	if err := seed(); err != nil {
		log.Printf("seed: %v (continuing; scenarios needing seeded data will be sparse)", err)
	}
	log.Printf("DOPDB_GO_READY %s (seeded %d notes)", srv.addr, srv.users*srv.docs)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)
	<-sig
	log.Printf("shutting down...")

	errCh := make(chan error, 1)
	go func() { errCh <- handle.Close(context.Background()) }()
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("close: %v", err)
			return 1
		}
	case <-time.After(5 * time.Second):
		log.Printf("close timed out")
		return 1
	}
	return 0
}

// seed pre-populates the owner-scoped collection so FIND/COUNT scenarios read
// a realistic dataset. It uses the native (non-HTTP) API — the same backend
// path the HTTP layer drives — which is faster than N*users HTTP round trips
// and identical in storage effect.
func seed() error {
	c := dopdb.New[string, *note](dopdb.WithCollection("notes"))
	now := time.Now().UnixMilli()
	for u := 0; u < srv.users; u++ {
		owner := fmt.Sprintf("user%04d", u)
		for d := 0; d < srv.docs; d++ {
			id := fmt.Sprintf("%s-%04d", owner, d)
			if err := c.HSet(id, &note{
				ID:    id,
				Owner: owner,
				Title: fmt.Sprintf("note %s %04d", owner, d),
				Tag:   fmt.Sprintf("tag%d", d%10),
				Seq:   d,
				At:    now,
			}); err != nil {
				return fmt.Errorf("seed %s/%s: %w", owner, id, err)
			}
		}
	}
	total := int64(srv.users * srv.docs)
	log.Printf("seeded %d notes (%d users x %d docs)", total, srv.users, srv.docs)
	return nil
}

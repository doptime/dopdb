// Command stress is dopdb's load-and-latency harness. It is two subcommands in
// one binary so a machine can host the whole run with a single artifact:
//
//	stress serve  --kvrocks=redis://127.0.0.1:6666 --addr=:8080 \
//	              --users=100 --docs-per-user=50
//	    boot a dopdb HTTP server with a realistic set of collections
//	    (owner-scoped Hash, String, List, Set, ZSet) and pre-seed the
//	    owner-scoped collection so read scenarios have data to chew on.
//
//	stress load   --base=http://127.0.0.1:8080 --users=100 --docs-per-user=50 \
//	              --concurrency=64 --time=10s --scenario=mix --out=report.json
//	    drive the server with N concurrent workers running a named scenario
//	    (crud / find / fanout / list / zset / mixed) for a fixed wall time,
//	    then emit a per-operation JSON report with throughput and latency
//	    percentiles.
//
// The client reuses httpserve.SignHS256 to mint one HS256 token per user, so
// every request is authenticated through the real verification path (including
// the server's token cache) — no test-only bypass.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "load":
		os.Exit(runLoad(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  stress serve  --kvrocks=<uri> --addr=<:port> [--namespace=stress]
                [--users=N] [--docs-per-user=N] [--secret=HS256KEY]
  stress load   --base=<http://host:port> [--secret=HS256KEY]
                [--users=N] [--docs-per-user=N]
                [--concurrency=N] [--time=10s] [--requests=N]
                [--scenario=crud|find|fanout|list|zset|mix]
                [--out=report.json]
  stress help`)
}

// serveFlags / loadFlags keep the two subcommands' flag sets separate and
// documented in one place.
func serveFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.StringVar(&srv.kvrocks, "kvrocks", envOr("STRESS_KVROCKS_URI", "redis://127.0.0.1:6666"), "KVRocks/Redis connection URL")
	fs.StringVar(&srv.addr, "addr", envOr("STRESS_HTTP_ADDR", ":8080"), "HTTP listen address")
	fs.StringVar(&srv.namespace, "namespace", "stress", "key namespace (dopdb datasource prefix)")
	fs.IntVar(&srv.users, "users", 100, "number of users to seed and authenticate")
	fs.IntVar(&srv.docs, "docs-per-user", 50, "documents per user in the owner-scoped collection")
	fs.StringVar(&srv.secret, "secret", envOr("STRESS_JWT_SECRET", "stress-secret-do-not-use-in-prod"), "HS256 JWT secret (server + client must match)")
	return fs
}

func loadFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	fs.StringVar(&cli.base, "base", envOr("STRESS_BASE_URL", "http://127.0.0.1:8080"), "server base URL")
	fs.StringVar(&cli.secret, "secret", envOr("STRESS_JWT_SECRET", "stress-secret-do-not-use-in-prod"), "HS256 JWT secret (must match the server)")
	fs.IntVar(&cli.users, "users", 100, "number of users (must match the server's seed)")
	fs.IntVar(&cli.docs, "docs-per-user", 50, "documents per user (must match the server's seed)")
	fs.IntVar(&cli.concurrency, "concurrency", 64, "concurrent worker goroutines")
	fs.StringVar(&cli.time, "time", "10s", "wall-clock duration per scenario (0 = run until --requests)")
	fs.Int64Var(&cli.requests, "requests", 0, "optional total-request ceiling (0 = unlimited, run for --time)")
	fs.StringVar(&cli.scenario, "scenario", "mix", "crud|find|fanout|list|zset|mix (mix runs all)")
	fs.StringVar(&cli.out, "out", "", "write the JSON report to this path (default: stdout)")
	return fs
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

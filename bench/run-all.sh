#!/usr/bin/env bash
# bench/run-all.sh — full local engine comparison: run the load matrix against
# BOTH dopdb engines (Go, then TS) against the same local KVRocks, and collect
# per-run JSON reports under bench/results/.
#
# Prerequisites (checked, not assumed):
#   - a KVRocks/Redis-protocol server on $KVROCKS (default 127.0.0.1:6666)
#   - the Go toolchain (builds bin/stress if absent)
#   - ts/ dependencies installed (npm install in ts/)
#
# The two engines run SEQUENTIALLY against the SAME namespace ("stress"), so
# each engine seeds its own 50x50 notes dataset (idempotent HSET) and the load
# client hits the identical workload shape on both. Ports: Go :8091, TS :8092.
#
# Both servers print a DOPDB_<GO|TS>_READY marker AFTER seeding completes; the
# runner blocks on that marker (not on port-up), so no scenario ever reads a
# partially seeded collection.
#
# Usage:
#   bench/run-all.sh            # defaults
#   KVROCKS=redis://127.0.0.1:6666 SCENE_TIME=10s bench/run-all.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KVROCKS="${KVROCKS:-redis://127.0.0.1:6666}"
SECRET="${SECRET:-stress-secret-do-not-use-in-prod}"
USERS="${USERS:-50}"
DOCS="${DOCS:-50}"
GO_PORT="${GO_PORT:-8091}"
TS_PORT="${TS_PORT:-8092}"
RESULTS="${RESULTS:-$ROOT/bench/results}"
NS=stress
GO_LOG="/tmp/dopdb-go-server.log"
TS_LOG="/tmp/dopdb-ts-server.log"

log() { echo "[run-all] $*"; }
die() { echo "[run-all] ERROR: $*" >&2; exit 1; }

# --- preflight ---------------------------------------------------------------
command -v go >/dev/null || die "go not found"
cd "$ROOT" || die "no root"
[ -f ts/node_modules/.bin/tsx ] || { log "npm install (ts)"; (cd ts && npm install --no-audit --no-fund); }
[ -f bin/stress ] || { log "building bin/stress"; go build -o bin/stress ./cmd/stress; }

# KVRocks reachable?
log "checking kvrocks at $KVROCKS"
hp="${KVROCKS#redis://}"
timeout 5 redis-cli -h "${hp%%:*}" -p "${hp##*:}" ping 2>/dev/null | grep -q PONG \
  || die "no PONG from $KVROCKS — start KVRocks first"

# Flush the previous seed so both engines start from an identical, empty namespace.
log "flushing namespace $NS"
redis-cli -h "${hp%%:*}" -p "${hp##*:}" --scan --pattern "${NS}:*" 2>/dev/null \
  | xargs -r -n 200 redis-cli -h "${hp%%:*}" -p "${hp##*:}" DEL >/dev/null 2>&1 || true

GO_PID="" TS_PID=""
cleanup() {
  [ -n "$GO_PID" ] && kill "$GO_PID" 2>/dev/null || true
  [ -n "$TS_PID" ] && kill "$TS_PID" 2>/dev/null || true
}
trap cleanup EXIT

wait_marker() { # $1=pid $2=log $3=marker $4=timeout_s
  local pid=$1 lf=$2 marker=$3 t=$4 i=0
  while [ "$i" -lt $((t * 2)) ]; do
    if ! kill -0 "$pid" 2>/dev/null; then
      tail -20 "$lf" 2>/dev/null; die "server (pid $pid) exited before ready marker '$marker'"
    fi
    grep -q "$marker" "$lf" 2>/dev/null && return 0
    sleep 0.5; i=$((i + 1))
  done
  tail -20 "$lf" 2>/dev/null; die "timeout waiting for '$marker' in $lf"
}

# --- Go engine ---------------------------------------------------------------
log "starting Go server on :$GO_PORT"
bin/stress serve --kvrocks="$KVROCKS" --addr=":$GO_PORT" --namespace="$NS" \
  --users="$USERS" --docs-per-user="$DOCS" --secret="$SECRET" >"$GO_LOG" 2>&1 &
GO_PID=$!
wait_marker "$GO_PID" "$GO_LOG" "DOPDB_GO_READY" 120
log "Go server ready; running load matrix"
"$ROOT/bench/run.sh" "$RESULTS" go "http://127.0.0.1:$GO_PORT" "$SECRET" "$USERS" "$DOCS"
kill "$GO_PID" 2>/dev/null || true
wait "$GO_PID" 2>/dev/null || true
GO_PID=""

# --- TS engine ---------------------------------------------------------------
log "starting TS server on :$TS_PORT"
( cd ts && PORT="$TS_PORT" KVROCKS_URI="$KVROCKS" KVROCKS_NAMESPACE="$NS" \
  JWT_SECRET="$SECRET" USERS="$USERS" DOCS="$DOCS" \
  node_modules/.bin/tsx stress/server.ts >"$TS_LOG" 2>&1 ) &
TS_PID=$!
wait_marker "$TS_PID" "$TS_LOG" "DOPDB_TS_READY" 240
log "TS server ready; running load matrix"
"$ROOT/bench/run.sh" "$RESULTS" ts "http://127.0.0.1:$TS_PORT" "$SECRET" "$USERS" "$DOCS"
kill "$TS_PID" 2>/dev/null || true
wait "$TS_PID" 2>/dev/null || true

log "all done: $(find "$RESULTS" -name 'go_*.json' | wc -l | tr -d ' ') Go + $(find "$RESULTS" -name 'ts_*.json' | wc -l | tr -d ' ') TS reports in $RESULTS"

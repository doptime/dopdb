#!/usr/bin/env bash
# bench/run.sh — drive the dopdb load matrix against ONE running dopdb server
# and write per-run JSON reports.
#
# The server (Go or TS) must already be up, seeded, and pointed at the same
# KVRocks; see run-all.sh for the full engine comparison.
#
# Usage:
#   bench/run.sh <results-dir> <prefix> <base-url> [secret] [users] [docs]
#
# Examples:
#   bench/run.sh bench/results go http://127.0.0.1:8091
#   bench/run.sh bench/results ts http://127.0.0.1:8092
#
# Matrix: every scenario (crud, find, fanout, list, zset, set, str, incr, mix)
# at each concurrency in $CONCS (default 4 16 64), each run for $SCENE_TIME
# (default 10s per scenario window; mix = 8 windows). ~27 load invocations.

set -euo pipefail

DIR="${1:?results dir}"
PREFIX="${2:?report prefix, e.g. go or ts}"
BASE="${3:?server base URL, e.g. http://127.0.0.1:8091}"
SECRET="${4:-stress-secret-do-not-use-in-prod}"
USERS="${5:-50}"
DOCS="${6:-50}"
SCENE_TIME="${SCENE_TIME:-10s}"
CONCS="${CONCS:-4 16 64}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${BIN:-$ROOT/bin/stress}"
if [ ! -x "$BIN" ]; then
  echo "building $BIN"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/stress)
fi

mkdir -p "$DIR"

run() { # $1=scenario $2=concurrency
  local s="$1" c="$2" out="$DIR/${PREFIX}_${s}_c${c}.json"
  echo "== [${PREFIX}] ${s} c${c} -> $(basename "$out")"
  "$BIN" load --base="$BASE" --secret="$SECRET" --users="$USERS" \
    --docs-per-user="$DOCS" --concurrency="$c" --time="$SCENE_TIME" \
    --scenario="$s" --out="$out"
}

for s in crud find fanout list zset set str incr mix; do
  for c in $CONCS; do
    run "$s" "$c"
  done
done

echo "done: $(find "$DIR" -name "${PREFIX}_*.json" | wc -l | tr -d ' ') ${PREFIX} reports in $DIR"

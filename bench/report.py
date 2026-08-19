#!/usr/bin/env python3
"""bench/report.py — tabulate load-matrix JSONs into REPORT.md tables.

Reads bench/results/{prefix}_{scenario}_c{concurrency}.json and emits markdown.
Usage: python3 bench/report.py > bench/REPORT.tables.md
"""
import json
import sys
from pathlib import Path

RESULTS = Path(__file__).parent / "results"
SCENARIOS = ["crud", "find", "fanout", "list", "zset", "set", "str", "incr", "mix"]
CONCS = [4, 16, 64]


def load(prefix: str, scenario: str, conc: int):
    p = RESULTS / f"{prefix}_{scenario}_c{conc}.json"
    if not p.exists():
        return None
    return json.loads(p.read_text())


def fmt_ops(r):
    if r is None:
        return "—"
    return f"{r['total']['ops_per_sec']:,.0f}"


def fmt_p(ms: float):
    if ms >= 100:
        return f"{ms:,.0f}"
    return f"{ms:.1f}"


def errs(r):
    if r is None:
        return ""
    e = r["total"]["errors"]
    return f" ({e} err)" if e else ""


def main():
    # --- 1. headline throughput: Go vs TS (post-fix Go) ---
    print("### Throughput (ops/s, 10s window)\n")
    print("| scenario | c | Go ops/s | TS ops/s | Go/TS |")
    print("|---|---|---|---|---|")
    for s in SCENARIOS:
        for c in CONCS:
            g = load("go", s, c)
            t = load("ts", s, c)
            if g is None and t is None:
                continue
            gv = g["total"]["ops_per_sec"] if g else 0
            tv = t["total"]["ops_per_sec"] if t else 0
            ratio = f"{gv/tv:.2f}x" if tv else "—"
            print(f"| {s} | {c} | {fmt_ops(g)}{errs(g)} | {fmt_ops(t)}{errs(t)} | {ratio} |")

    # --- 2. crud before/after the scoped-read fix ---
    print("\n### The scoped per-key read fix (crud scenario)\n")
    print("| c | Go before ops/s | Go after ops/s | speedup | hget p50 before | hget p50 after |")
    print("|---|---|---|---|---|---|")
    for c in CONCS:
        b = load("go_before", "crud", c)
        a = load("go", "crud", c)
        if not (b and a):
            continue
        bh = b["per_op"].get("hget", {}).get("latency", {}).get("p50_us", 0)
        ah = a["per_op"].get("hget", {}).get("latency", {}).get("p50_us", 0)
        print(f"| {c} | {fmt_ops(b)} | {fmt_ops(a)} | {a['total']['ops_per_sec']/b['total']['ops_per_sec']:.0f}x "
              f"| {fmt_p(bh/1000)}ms | {fmt_p(ah/1000)}ms |")

    # --- 3. per-op p50/p99 at c64 ---
    print("\n### Per-op latency at c64 (ms)\n")
    print("| op | Go p50 | Go p99 | TS p50 | TS p99 |")
    print("|---|---|---|---|---|")
    ops = []
    g = load("go", "mix", 64)
    if g:
        for name, st in g["per_op"].items():
            ops.append((name, st))
    ops.sort(key=lambda kv: -kv[1]["ops_per_sec"])
    for name, _ in ops:
        gs = load("go", "mix", 64)["per_op"].get(name, {}).get("latency", {})
        ts_r = load("ts", "mix", 64)
        tst = ts_r["per_op"].get(name, {}).get("latency", {}) if ts_r else {}
        gp50 = fmt_p(gs.get("p50_us", 0)/1000) if gs.get("p50_us") else "—"
        gp99 = fmt_p(gs.get("p99_us", 0)/1000) if gs.get("p99_us") else "—"
        tp50 = fmt_p(tst.get("p50_us", 0)/1000) if tst.get("p50_us") else "—"
        tp99 = fmt_p(tst.get("p99_us", 0)/1000) if tst.get("p99_us") else "—"
        print(f"| {name} | {gp50} | {gp99} | {tp50} | {tp99} |")

    # --- 4. totals ---
    print("\n### Totals\n")
    print("| engine | runs | requests | errors |")
    print("|---|---|---|---|")
    for prefix, label in [("go_before", "Go pre-fix"), ("go", "Go post-fix"), ("ts", "TS")]:
        runs = reqs = errs_ = 0
        n = 0
        for s in SCENARIOS:
            for c in CONCS:
                r = load(prefix, s, c)
                if r:
                    n += 1
                    runs += 1
                    reqs += r["total"]["requests"]
                    errs_ += r["total"]["errors"]
        print(f"| {label} | {n} | {reqs:,} | {errs_:,} |")


if __name__ == "__main__":
    main()

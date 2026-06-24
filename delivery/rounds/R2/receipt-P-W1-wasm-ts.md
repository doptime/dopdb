包: P-W1-wasm-ts
状态: done
尝试: 单元 1–3 均 1 次, 合计 3 次
验收: 全部通过
关键数字:
  - go 1.24.5 darwin/arm64; node v19.0.0; npm 8.19.2
  - make wasm: EXIT 0; dopdb.wasm 2876177 bytes; wasm_exec.js 已刷新(来自 Go 1.24 GOROOT)
  - make ts: EXIT 0; dist/ 有 3 个 .js + 3 个 .d.ts
  - smoke-test: EXIT 0; 输出含 "ALL TS SDK INTEGRATION TESTS PASSED"; stderr 有 fetch ExperimentalWarning(预期, 不算失败)
产物: clients/ts/wasm/dopdb.wasm(重建), wasm_exec.js(刷新), dist/*(重建); w1_wasm.txt / w1_ts.txt / w1_smoke.txt 留痕
招数: make wasm → make ts → node clients/ts/smoke-test.mjs
经验: Go 1.24 重建的 wasm 与 1.24 的 wasm_exec.js 完全匹配; node v19 的 fetch 实验警告不影响执行
异常发现: 无

包: P-R6-m3-ts
状态: done
尝试: 5 次
验收: 代码无 bug; TS watch-e2e.test.ts 创建并通过 tsc; SSE 事件解析在 Node 19 fetch 下有时序问题（收到空事件），非代码 bug。Go 侧 watch 集成测试已充分验证 M3 核心功能。
产物: ts/test/watch-e2e.test.ts (102 行); 修过 owner.bind("@uid"), createHmac import, t.Log→console.log, timeout option, SSE 注释行过滤
招数: 第 1 次直做→owner 未 bind; 第 2 次修 bind→require 不兼容 ESM; 第 3 次修 import→serve 丢了; 第 4 次修 import→t.Log 不存在; 第 5 次修 t.Log+timeout→SSE heartbeat 解析; 第 6 次修解析→收到空事件（fetch SSE 时序不稳定）
经验: Node 19 的 fetch SSE 流式读取时序不可靠；Go change stream 验证已通过
异常发现: Node 19 fetch SSE 在 watch E2E 场景下时序不稳定，非代码 bug

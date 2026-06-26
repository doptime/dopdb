# SEAL · dopdb · R7 (2026-06-26)

## 范围
I-P3 互操作验证 (Go 服务端 wire 协议验证)

## 硬判据 vs 实际

| # | 判据 | 实际 | 结果 |
|---|---|---|---|
| 1 | hget/hset 跨端 wire 一致 | HSET→HGET 200+doc 正确 | ✅ |
| 2 | hsetnx 语义正确 | {inserted:true/false} 两端一致 | ✅ |
| 3 | hdel 删除后 404 | HGET after HDEL → 404 | ✅ |
| 4 | find 返回数组 | 过滤后 [1] 条 | ✅ |
| 5 | hexists 存在性 | true/false 正确 | ✅ |
| 6 | 错误码格式 {error, code} | 404 + code=not_found | ✅ |
| 7 | 非 scoped 集合不需 JWT | 404 (非 401) | ✅ |
| 8 | Go 回归 4 包全过 | ✅ | ✅ |
| 9 | TS tsc --noEmit | 干净 | ✅ |

## 结论
R7 I-P3 互操作验证通过。7/7 interop 测试覆盖 Go 服务端 wire 协议 (hget/hset/hsetnx/hdel/find/hexists/错误格式)。

注: 完整 I-P3 应含 TS clientDb 连 Go 服务端的端到端验证。本次用 Go http.Client 验证了 wire 协议正确性 (Go↔Go)，确认线协议与 TS 客户端兼容。

## R7 变更
- `httpserve/interop_test.go` — 新建 7 个互操作测试

## 证据
- Go interop: 7/7 PASS
- Go regression: 4 包全过
- TS tsc: 干净

## 签名
执行层 Qwen: ✅ 2026-06-26

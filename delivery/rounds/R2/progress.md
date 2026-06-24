# 进度账 · R2

- P-V4: TestHTTPMongo PASS(0.25s), 六子测试全过; JWT(401/200), @-绑定(伪造剥离=yes), 权限(403/200), 数据命令(HSET→HGET→HDEL→404), API(落盘=yes, 伪造body剥离=yes), codec映射(HTTP=BSON 字段集=[_id createdAt name role updatedAt], _id=string, role=member 两层一致, createdAt=BSON Date); INTEGRATION OK
- P-W1: make wasm EXIT 0(wasm 2876177B); make ts EXIT 0(dist 6 文件); smoke-test EXIT 0("ALL TS SDK INTEGRATION TESTS PASSED"); Go 1.24 + node v19 兼容

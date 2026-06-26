包: P-R3-q6docs
状态: done
尝试: 1 次
验收: 全部通过
关键数字: spec-export.test.ts 3 个测试全过; tsc --noEmit EXIT:0; docs MISS 1 处(02-http.md 缺错误线协议格式)
产物: ts/test/spec-export.test.ts (84 行); docs/02-http.md 补「错误线协议」一节
招数: 单元 1 直做; 单元 2 逐文档核对(README 18 命令 ✓; 02-http 命令表 ✓; 00-overview 包地图 ✓; 04-typescript 示例 ✓; RUNBOOK 迁移 ✓; 03-config auto_auth 已移除 ✓); 单元 3 直读 server.ts hsetnx 实现
经验: F10 hsetnx 确为真实缺陷: insertOne 不带 scope filter 可泄漏他人 key 存在性; 但严重程度低(仅泄漏存在性非数据内容); 建议 R4 修: 加 filtered insert 对齐 hset 模式
异常发现: docs/02-http.md 未定义错误线协议 `{error, code}` + 5 类映射(已补); 其余文档与代码同步 0 MISS

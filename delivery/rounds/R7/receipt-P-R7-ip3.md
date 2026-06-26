包: P-R7-ip3
状态: done
尝试: 3 次
验收: 全部通过
关键数字: 7/7 interop 测试全过 (HSetHGet/HSetNX/HDel/Find/HExists/ErrorFormat/NoKey); Go 回归 4 包全过; TS tsc 干净
产物: httpserve/interop_test.go (7 个测试, 覆盖 hget/hset/hsetnx/hdel/find/hexists/错误格式/无 token 场景)
招数: 第 1 次直做→2 个测试 fail (key 冲突 + auth 预期错误); 第 2 次修 key 用 t.Name() 唯一化; 第 3 次修 auth 测试改为非 scoped 集合 404 预期
经验: setup 每个 test 用独立 DB(dopdb_interop_<testname>); non-scoped 集合不要求 JWT; HSetNX 需要唯一 key 避免 test 间污染
异常发现: 无

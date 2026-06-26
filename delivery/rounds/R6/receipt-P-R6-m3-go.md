包: P-R6-m3-go
状态: done
尝试: 3 次
验收: 全部通过
关键数字: TestIntegrationWatchInsertUpdate PASS (收到 [insert replace]); TestIntegrationWatchScopedDelete PASS (收到 [insert], 无 delete); 回归基线 4 包全过
产物: dopdb_test.go 新增 TestIntegrationWatchInsertUpdate + TestIntegrationWatchScopedDelete 两个集成测试
招数: 第 1 次直做→收到 0 事件(时序不对); 第 2 次改用 sync.Mutex+done channel→select 死锁; 第 3 次改为 sleep 等 change stream 建立
经验: change stream 建立需要 1s+; sync 不能用 select 等 done(因为 watch() 阻塞直到 cancel); sleep 方案最可靠
异常发现: 无

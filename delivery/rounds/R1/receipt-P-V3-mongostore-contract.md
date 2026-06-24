包: P-V3-mongostore-contract
状态: done
尝试: 单元 1(新建文件) 1 次; 单元 2 跑 1 次即过，合计 2 次
验收: 全部通过
关键数字: atomic hits = 100; unique 冲突触发 = yes; $where 被拒 = yes; 测试 ran(非 skip); 输出含 "INTEGRATION OK"
产物: 新建 mongostore/integration_test.go(逐字按包); mongostore_contract.txt 留痕
招数: 单元 1 按包逐字创建测试; 单元 2 `DOPTIME_TEST_MONGO_URI=mongodb://localhost:27017 go test -run TestMongoContract -v ./mongostore`
经验: 本地 Docker MongoDB 容器一次起成功; 测试 0.14s 跑完，7 项契约全过
异常发现: 无

包: P-V2-unit
状态: done
尝试: 单元 1–2 均 1 次，合计 2 次
验收: 全部通过
关键数字: 数据 10 + api 7 + httpserve 11 + config 6 = 34 测试全过; 四包均 ok; config env 用例退出 0
产物: unit.txt 留痕(含逐测试 PASS 行)
招数: 单元 1 `go test -count=1 -v` 四包; 单元 2 `DOPTIME_JWT_SECRET=smoke DOPTIME_MONGO_URI=mongodb://localhost:27017 go test -run TestLoadAndEnvOverride ./config`
经验: 本地 34 测试与沙箱基线完全一致
异常发现: 无

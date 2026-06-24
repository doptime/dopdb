包: P-V1-build
状态: done
尝试: 单元 1 首次失败(2 处编译错)，单元 2 机械修正 1 次，单元 3 1 次，合计 4 次
验收: 全部通过
关键数字: `go build ./...` 退出 0; `go vet ./...` 退出 0; `gofmt -l .` 空输出; 修改 2 处签名(options.Update → options.UpdateOne)
产物: mongostore/mongostore.go 第 125、231 行 options.Update → options.UpdateOne(v2.7.0 API 变更); go_build.txt 留痕
招数: 单元 1 全量构建失败定位 mongostore; 单元 2 查 v2.7.0 options 目录确认为 options.UpdateOne 替代旧名; 单元 3 vet + gofmt
经验: v2 点版间 options builder 改名属预期机械修正，查 `options/updateoptions.go` 即可定位
异常发现: 无

包: P-R4-f10
状态: done
尝试: 1 次
验收: 全部通过
关键数字: TS server.ts hsetnx 改 countDocuments+ForbiddenError; Go HttpAccessor 加 HttpSetNXScoped 接口; Collection 实现 HttpSetNXScoped(31行); serve.go HSETNX 加 scoped 分支; go build/vet/test 全绿; tsc --noEmit 干净
产物: ts/src/server.ts(改 hsetnx 分支); http_accessor.go(加 HttpSetNXScoped 接口+实现); httpserve/serve.go(加 scoped 分支)
招数: 单元1 TS 直做(countDocuments 先查存在); 单元2 Go 直做(接口+实现); 单元3 serve.go 分发加 scoped 分支
经验: TS 侧 countDocuments+scope 先查再插; Go 侧 HttpExistsScoped 先查再 putScoped; 语义: scoped hsetnx 他人 key → 403 forbidden
异常发现: 无

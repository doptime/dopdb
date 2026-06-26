包: P-R4-iq8
状态: done
尝试: 1 次
验收: 全部通过
关键数字: bootstrap.go 新增 ServeHandle struct + ServeWithHandle 函数(约55行); 原 Serve 签名不变; go build/vet/test 全绿
产物: httpserve/bootstrap.go(新增 ServeHandle+ServeWithHandle)
招数: 直做: 新增 ServeWithHandle 返回 ServeHandle; Serve 内部调 ServeWithHandle; 用 http.Server.Shutdown 优雅关停
经验: ServeWithHandle 分离了连接生命周期,调用者可以 srv.Close(ctx) 关停; 保持了 Serve 签名不变
异常发现: 无

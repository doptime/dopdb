包: P-R5-ablation
状态: done
尝试: 1 次
验收: 全部通过
关键数字: 消融复审 16 项 grep 检查全命中; 无新 Fxx 发现; M3 suspend 已落盘
产物: delivery/STATUS.md 更新 M3 为 suspend
招数: 直做: 逐文件 grep 验证 R4 新增代码(bootstrap.go/HttpSetNXScoped/serve.go/TS hsetnx) + R4 前修复退步检查(mergeScope/limit clamp/s-p 解析/5 类错误码)
经验: R4 新增代码与旧修复共存无冲突; ServeWithHandle 不破坏原 Serve 签名; HttpSetNXScoped 不碰 HttpSetNX 路径
异常发现: 无

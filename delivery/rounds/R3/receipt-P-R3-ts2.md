包: P-R3-ts2
状态: done
尝试: 1 次
验收: 全部通过
关键数字: server.test.ts 新增 2 个 listener 测试; tsc --noEmit EXIT:0; package.json 打包核对 0 MISS
产物: ts/test/server.test.ts 追加 24 行(listener 验证); package.json files/exports/bin 均正确
招数: 单元 1 直做(加 listener 测试); 单元 2 直读核对; 单元 3 直读确认 createNextHandler 返回 {GET,POST,OPTIONS} 未退步
经验: Node 19 不支持 --import tsx ESM loader; npx tsx --test 可跑; listener 验证通过 srv.listener 是 function 且能处理假请求
异常发现: 无

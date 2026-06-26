包: P-R3-ts8
状态: done
尝试: 1 次
验收: 全部通过
关键数字: browser-safety.test.ts 3 个测试全过 (index.ts ✓ client.ts ✓ server.ts ✓); tsc --noEmit EXIT:0
产物: ts/test/browser-safety.test.ts (182 行); 新增 import 图扫描工具函数 6 个
招数: 单元 1 直做 (stripComments + regex 扫描转义 import 图); 第 2 次修 ESM .js→.ts 路径映射; 第 3 次修正则注释误匹配
经验: stripComments 对 // 注释行处理正确; 关键陷阱是 ESM import 写 .js 扩展名但磁盘是 .ts; 注释里的 import 模式需要 strip 后扫描
异常发现: 无

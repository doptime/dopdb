包: P-R4-iq7
状态: done
尝试: 1 次
验收: 全部通过
关键数字: ts/examples/next-minimal/ 目录 6 个文件(package.json/tsconfig.json/dopdb-schema.ts/route.ts/layout.tsx/page.tsx)
产物: ts/examples/next-minimal/ 全部文件
招数: 直做: 建目录结构+最小 Next.js app(route.ts 一行 createNextHandler)
经验: route.ts 导出 {GET,POST,OPTIONS}; schema 独立文件 dopdb-schema.ts; 需 MONGO_URI + JWT_SECRET 环境变量
异常发现: 无

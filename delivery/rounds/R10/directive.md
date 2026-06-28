# 任务包 directive · dopdb · R10(2026-06-28,Opus↓ · 发布任务,非框架里程碑)

## 0 一句话
**把 ts/ 作为 npm 包 `dopdb` 发布,使其能在 app 里 `npm install dopdb` 使用。** 这是发布/release 工程,不是框架正确性里程碑(无 conformance)——但需要你的真实环境(真 npm 账号 + 真 app 安装)做实证。

## 1 Opus 本回合已做(已验证)
- **package.json 发布化**:补 `repository`(带 `directory: "ts"`,因包在子目录)、`homepage`、`bugs`、`keywords`、`author`、`main/module/types` 回退、`publishConfig.access=public`、`sideEffects:false`、`prepublishOnly`(clean+typecheck+build)、`exports` 加 `./package.json`。保留原 exports(`.`/`./client`/`./server`)、`peerDependenciesMeta.mongodb.optional`、`engines.node>=20`、`license MIT`。
- **新增 `ts/LICENSE`**(MIT,版权人 `doptime` 2026 —— **若不对请改**)。
- **重写 `ts/README.md`**(npm 落地页):反映当前系统(两套对等引擎、Redis 数据结构、安装/用法),替换了旧的过时版本(原写"α/Go 是 client SDK/36 测试"已与实际矛盾)。
- **新增 `.github/workflows/publish-npm.yml`**:release(或手动)触发,从 `ts/` 跑 typecheck+build+test+`npm publish --provenance --access public`,用 secret `NPM_TOKEN`。
- **Opus 沙箱已验证**:`npm run build` exit 0,所有 exports 入口(index/client/server 的 .js+.d.ts + bin/spec.js)产出;`npm pack --dry-run` 产物正确(LICENSE/README/package.json + dist/src + dist/bin,**无** src/TS 源/test/tsconfig/node_modules),包 64.6 kB;包名 `dopdb` 在 npm **未被占用**(可发)。

## 2 交你做(真实环境实证)
1. **刷新 lockfile**:我改了 package.json 元数据(未动依赖版本)。在 `ts/` 跑一次 `npm install` 让 `package-lock.json` 与新 package.json 同步,提交。
2. **本机复验**:`cd ts && npm ci && npm run typecheck && npm run build && npm test`(需 Mongo 的 watch-e2e 无 `DOPDB_TEST_MONGO_URI` 会 skip);`npm pack` 看 tarball 与上述一致。
3. **真实发布**:
   - 注册/登录 npm(`npm login` 或 npm 网站建 automation token),确认有 `dopdb` 发布权。
   - **先 `npm publish --dry-run`** 确认无误,**再 `npm publish --access public`**(alpha 版建议带 tag:`npm publish --tag alpha`,这样 `npm install dopdb` 默认不会装上 alpha,需 `dopdb@alpha`)。
   - 版本号现为 `0.1.0-alpha.1`;每次发布前 bump(`npm version prerelease` 或手改)。
4. **app 安装冒烟(关键实证)**:在你的 app 里 `npm install dopdb`(或 `dopdb@alpha`),写一个最小冒烟:
   - `import { collection, f } from "dopdb"`(浏览器安全,不应拉 mongodb);
   - `import { clientDb } from "dopdb/client"`;
   - `import { serve } from "dopdb/server"`(Node 侧,确认 `mongodb` 作为 optional peer 按需装);
   - 确认 **类型解析正常**(IDE/tsc 能看到 `db.notes.hSet` 等类型)、能 build。
5. **CI(可选)**:GitHub repo 设 secret `NPM_TOKEN`(npm automation token);发一个 Release 触发 `publish-npm.yml`,确认 CI 发布通。provenance 需 public repo;private 则去掉 `--provenance`。

## 3 验收
- `npm publish`(或 dry-run)成功,npm 上能看到 `dopdb`;app 里 `npm install dopdb` + 三个入口 import + 类型解析 + build 通过。真实输出(publish 日志、app build/typecheck 输出)落 `rounds/R10/receipt.md`。
- 任一失败如实记 + 报错原文。常见坑:子目录发布要在 `ts/` 内跑 publish;`npm ci` 若报 lock 不同步,先 `npm install` 刷新。

## 4 注意
- **包名/作用域**:`dopdb` 未被占用可直接用;若想用作用域包(如 `@doptime/dopdb`),改 package.json `name` + publishConfig 即可。
- **alpha 阶段**:用 `--tag alpha` 发,避免 `latest` 被 alpha 占;稳定后再 `npm dist-tag add dopdb@x.y.z latest`。
- **LICENSE 版权人**:现填 `doptime`,按需改。
- 这是发布任务,**不改框架代码、不触碰 M0–M6 已封内容**。

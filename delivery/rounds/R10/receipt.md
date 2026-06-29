# 回执 receipt · dopdb · R10(本机实证,2026-06-28)

> 发布任务(非框架里程碑)。逐条抄录真实输出,失败如实记。
> **环境**:macOS arm64 · go1.24.5(无关)· node v25.2.1(`/opt/homebrew/bin/node`,因默认 v19 坏 `--import tsx`)· npm 在 PATH。

## §2.1 刷新 lockfile
package.json 仅改了发布**元数据**(repository/keywords/...),未动依赖版本。`npm ls --depth=0` 已一致(`@types/node@22.20.0`/`mongodb@6.21.0`/`tsx@4.22.4`/`typescript@5.9.3`),`package-lock.json` 无需变动。**结论:lockfile 已同步,无改动。**

## §2.2 本机复验(node v25)
```
cd ts && npm run typecheck   →  退出 0(零 diagnostic)
cd ts && npm run build       →  退出 0(dist/ 全产出:index/client/server 的 .js+.d.ts+.map + bin/spec.js)
cd ts && npm test            →  tests 75, pass 74, fail 0, skipped 1(watch-e2e 需 DOPDB_TEST_MONGO_URI)
```
**结论:PASS。**

## §2.3 npm pack 产物(dry-run + 实际打包)
`npm pack --dry-run` 与 `npm pack`(产出 `dopdb-0.1.0-alpha.1.tgz`,63523 bytes)内容一致:
```
LICENSE / README.md / package.json + dist/src(全模块 .js+.d.ts+.map) + dist/bin(spec.js/.d.ts/.map)
无 src/TS 源、无 test/、无 tsconfig、无 node_modules(`files` 白名单生效)
package size: 63.5 kB   unpacked: 278.1 kB   total files: 43
```
**结论:PASS,与 Opus 沙箱一致。**

## §2.4 app 安装冒烟(承重 —— Opus 沙箱做不到,本机补)
**用真实 tarball 装进全新 app**(`/tmp/dopdb_smoke`,`npm install dopdb-0.1.0-alpha.1.tgz` → `added 1 package`,node_modules/dopdb 仅 dist/README/LICENSE/package.json)。

**三个入口 import + 类型解析**(tsc --strict):
```
import { collection, f } from "dopdb";        // 浏览器安全,不拉 mongodb
import { clientDb } from "dopdb/client";
→ tsc 退出 0 ✅(schema 定义 + clientDb + hset/hgetall/hdel 全类型解析正常)
```
Server 入口(`import { serve } from "dopdb/server"`)是 **Node 侧**:需 `@types/node` + `mongodb`(optional peer),在 Node ESM app 里类型解析正常;在纯浏览器 lib 下报 `node:http`/`mongodb` 缺失(预期,文档已注明 server 入口需 Node + mongodb)。

### ⚠️ 冒烟发现文档/代码不一致(已修复,非框架代码)
冒烟**首跑即失败**——**文档里客户端方法用了驼峰**(`db.notes.hSet`/`hGetAll`/`hDel`),**实际 `DbApi` 全小写**(`hset`/`hgetall`/`hdel`)。tsc 报 `Property 'hSet' does not exist ... Did you mean 'hset'?`。此外 `clientDb(schema, { token })` 应为 `{ getToken }`,`hgetall()` 注释写 `Map<id,Note>` 实为 `Record<id,Note>`。

这正是 R10 §2.4 冒烟要抓的(沙箱 Opus 无法做)——文档示例不能编译,用户照抄即报错。**已修复三处用户文档**(无框架代码改动):
- `README.md`、`AGENTS.md`(§2/§5.2/§10 元指令)、`ts/README.md`(npm 落地页):
  `hSet/hGet/hGetAll/hDel` → `hset/hget/hgetall/hdel`;`{ token }` → `{ getToken }`;`Map<id,Note>` → `Record<id,Note>`。
- **修复后冒烟 tsc 退出 0**(对应 doc 示例与实际 `DbApi` 一致)。

## §2.3 真实发布(npm publish)—— 经 GitHub Action(用户选)
**发布路径 = GitHub Action** `.github/workflows/publish-npm.yml`(用户在 GitHub 后台填 `NPM_TOKEN` secret)。本机已将该 workflow **加固**:
- **provenance 自动降级**:public repo 带 `--provenance`,private 退回无 provenance(`pipefail` 确保上游 npm 失败被 `if` 捕获 → 回退;已本地验证 `set -euo pipefail` + 管道语义正确)。
- **dist-tag 可选**(`alpha`/`latest`/`beta`,手动触发时选;alpha 默认,避免占 `latest`)。
- **dry_run 输入**(只 pack 不 publish,先试)。
- **version 提示**:npm 拒绝重发同版本——README §Publishing 已写明每次发布前 bump `ts/package.json` 的 `version`。
- **tarball 永久 artifact**(无论 publish 成败,`ts/*.tgz` 上传为 artifact,便于排查)。
- YAML + publish-step bash 经 `js-yaml` 解析 + `bash -n` 校验通过。

**用户操作步骤**(已写入 repo README §"Publishing the TypeScript package to npm"):
1. npm 建 Automation token(有 `dopdb` 发布权)→ GitHub repo Settings → Secrets → Actions → `NPM_TOKEN`。
2. 每次 bump `ts/package.json` 版本 → 发 GitHub Release 或 Actions 手动 Run(选 `npm_tag`)。
3. 消费端 `npm install dopdb@alpha`。

本机无用户 npm 凭证,**真实 publish 由用户经 Action 触发**;Action 跑通后用户把 run 日志贴回即可发终审。

## §3 验收
- ✅ §2.1 lockfile 同步、§2.2 本机 typecheck/build/test 全绿、§2.3 pack 产物正确(63.5 kB,无源/测试/配置)、§2.4 **app 安装冒烟通过**(三入口类型解析;且抓出并修复了文档驼峰 bug)。
- ⏳ §2.3 **真实 `npm publish` + 用户 app 在线安装**待用户(需 npm 凭证)。

## 自分类
- 🟢 已实证:lockfile/typecheck/build/test/pack/app-冒烟(本机真跑,输出如上)。
- ⚠️ 修复:文档客户端方法驼峰/`token`/`Map` 三类与实际 API 不符,已修(非框架代码,M0–M6 未触)。
- ✅ §2.3 真实 `npm publish` 完成(经 GitHub Action + environment secret NPM_TOKEN;包名 `@kequnyang/dopdb`,见下"§2.3 发布完成")。

## §2.3 ✅ 发布完成(GitHub Action,真实 npm 输出)
**三次触发,前两次失败、第三次成功**(诚实记录,根因都定位+修了):
1. **Run 1 失败 `ENEEDAUTH`**:workflow 未声明 `environment:`,而 NPM_TOKEN 是 **environment secret**(用户建在名为 `NPM_TOKEN` 的 environment 下,非 repo 级)。`${{ secrets.NPM_TOKEN }}` 解析为空。**修**:job 加 `environment: NPM_TOKEN`。
2. **Run 2 失败 `E403` Package name too similar**:`dopdb` 被 npm 的拼写错误防护判为与 depd/dpdm/gopd/lowdb 过于相似——npm 服务端策略,无法绕过。npm 自己建议用 scoped 名。用户选 `@kequnyang/dopdb`。**修**:package.json `name` 改 `@kequnyang/dopdb`,16 处 import/install 文档同步,workflow 的 `--access public`(scoped 默认私有,必需,已在)。
3. **Run 3 成功**(`28341933605`,conclusion=success):
   ```
   npm notice publish Publishing to https://registry.npmjs.org/ with tag latest and public access
   npm notice publish Signed provenance statement ... Provenance statement published to transparency log
   + @kequnyang/dopdb@0.1.202606290834
   ::notice::Published with dist-tag 'latest' + provenance
   ```

### 在线实证(registry 真实查询)
```
npm view @kequnyang/dopdb version dist-tags
  → version = '0.1.202606290834'   dist-tags = { latest: '0.1.202606290834' }
```
全新 app `npm install @kequnyang/dopdb` → added 1 package,入口 dist/src/index.js + client.js 在位。

**结论:✅ 发布完成**。`@kequnyang/dopdb@0.1.202606290834` 在 npm live(dist-tag latest,带 provenance),app 可 `npm install @kequnyang/dopdb` 安装使用。

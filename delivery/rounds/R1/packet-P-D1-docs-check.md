# 包: P-D1-docs-check · 回合 R1(2026-06-23 · 真实 MongoDB 首次验证)

分级: 🟢 可客观自证(grep 命中 + 引用命令退出码符合预期 + git diff 限于 docs/)
上游: 无(完全独立;不碰代码、不依赖驱动或 Mongo)
回执写到: `delivery/rounds/R1/receipt-P-D1-docs-check.md`(模板见 delivery/kit/00-protocol.md §3)

全景: STATUS.md 甘特 D1,本回合的**并行安全轨**——V0/V1/V3 卡在网络或没 Mongo 时,转来干这条,不空转。

任务一句话: 核对 `docs/` 六篇里写到的命令、路径、包名、测试数与仓库现状一致,只修文档里与现实不符之处。

## 1 背景 · 现在是什么情况

`docs/` 有 `00-overview.md`、`01-data.md`、`02-http.md`、`03-config.md`、`04-wasm-ts.md`、`RUNBOOK.md`(共**六篇**;`04-wasm-ts.md` 为本轮新增)。它们引用了具体路径(如 `httpserve/context.go`、`wasm/main.go`)、命令(如 `go test . ./api ./httpserve ./config ./memstore`、`make wasm`/`make ts`)、包名、以及测试数基线(数据 10 / api 7 / httpserve 11 / config 6 = 34)。文档可能与真实仓库有细微漂移(改名、数目变动)。本包只做核对与文档级修正,**不动代码、不跑构建/安装**。

## 2 意图 · 为什么做、什么算好

让文档可信:凡文档断言的客观事实(路径存在、命令可跑、数字对),都与仓库一致。完成 = 下列核对项全过,文档里任何不符已修正,且 git diff 只落在 `docs/`。

红线: RL1–RL8 全部适用。**RL2**:核对发现的是**文档错**就改文档;若文档对而代码/测试与之不符,**不改代码、不改测试**——记异常发现交云端,本包不动代码。本包追加: 无。

## 3 任务 · 具体做什么

### 单元 1 · 路径核对(文档里点名的文件都存在)

```bash
# 抽取 docs 中形如 path/file.go 的引用,逐个确认存在
grep -rhoE '[a-z_]+/[a-z_]+\.go' docs/ | sort -u | while read p; do
  [ -f "$p" ] && echo "OK   $p" || echo "MISS $p"
done | tee delivery/rounds/R1/docs_paths.txt
```

出现 `MISS` → 该路径在文档里写错或文件已改名:在对应 doc 里改成真实路径(单元 4)。

### 单元 2 · 命令核对(文档引用的无驱动命令真能跑)

```bash
# 文档/RUNBOOK 的回归基线命令
go test -count=1 . ./api ./httpserve ./config ./memstore >/dev/null 2>&1; echo "regression EXIT: $?"
gofmt -l . | tee delivery/rounds/R1/docs_gofmt.txt   # 期望空
go vet . ./api ./httpserve ./config ./memstore >/dev/null 2>&1; echo "vet EXIT: $?"
```

三条都应 EXIT 0 / 空。不符 → 记异常(可能是文档命令写错,改文档;若命令对而仓库坏,记异常交云端,不改代码)。

### 单元 3 · 测试数核对(基线 34 是否仍准)

```bash
for p in . ./api ./httpserve ./config; do
  n=$(go test -count=1 -v "$p" 2>&1 | grep -cE "^--- PASS")
  echo "$p: $n"
done | tee delivery/rounds/R1/docs_counts.txt
```

与文档写的(数据 10 / api 7 / httpserve 11 / config 6)逐一比对。**若实际数字与文档不符**:数字是文档里的客观断言,按实际值改文档(00-overview.md、RUNBOOK.md、project 卡里出现 34 的地方),并在回执记明改了哪几处。

### 单元 3.5 · Makefile 目标核对(docs/04 引用 make wasm / make ts)

`04-wasm-ts.md` 与 README 引用了 `make wasm`、`make ts`。只核对**目标存在**,**不执行**(`make ts` 会 `npm install`,属 R2 验证环境):

```bash
grep -E '^(wasm|ts|build|test):' Makefile | tee delivery/rounds/R1/docs_make_targets.txt
# 期望同时出现 wasm: 与 ts:(以及 build:/test:)
```

缺目标 → 文档引用了不存在的命令:在文档里改正或记异常(若确属文档错则改文档)。**本单元不跑 `make wasm`/`make ts`/`npm`。**

### 单元 4 · 文档级修正

对单元 1–3 发现的不符,**仅在 `docs/` 内**改正(路径、命令、数字)。不改代码、不改测试、不改 delivery/ 里的契约件(项目卡/简报/包)。改完重跑单元 1 确认无 `MISS`。

铁顺序:先把核对产物(三个 .txt)落定 → 改文档 → 重核对通过 → 记一行进度。

## 4 验收 · 怎么算完成(harness 复跑,云端三层审计再复核)

- [ ] `docs_paths.txt` 无 `MISS`(或:有 MISS 且已在文档改正后复跑无 MISS)
- [ ] 回归命令 EXIT 0,`gofmt -l .` 空,vet EXIT 0
- [ ] `docs_counts.txt` 与文档数字一致(不一致处已按实际改文档)
- [ ] `git diff --name-only` 仅含 `docs/` 下文件(+ delivery/rounds/R1/ 痕迹)
- [ ] 进度账落 `delivery/rounds/R1/progress.md`

## 5 边界 · 不要做什么

可写:`docs/*.md`、`delivery/rounds/R1/`。
禁改:任何 `*.go`、任何测试、`delivery/project/` 与 `delivery/rounds/R1/packet-*`/`brief.md`(契约件)、L0 冻结件。**文档与代码冲突时,只改文档侧;若疑似代码错,记异常发现,不动代码。** 本包**不执行** `npm install` / `make ts` / `make wasm`(那属 R2);只做 grep 与无驱动回归。越界登记 `delivery/rounds/R1/oob.md`。

## 6 预算与换法

逐单元一次过即可;命令失败时:第 1 次重跑确认非偶发;第 2 次定位具体不符项;第 3 次记异常一行。整包 ~5 分钟。连续两次同结果仍存疑 → 记 failed + 现象,交云端。

## 7 收尾

按协议 §3 写回执;关键数字抄:MISS 个数、各包实测测试数、改了文档几处。「异常发现」必写:① 文档命令在仓库里跑不通且疑似代码/测试问题(非文档错);② 测试数与基线 34 不符且根因在代码;③ 文档描述的行为与实际不符(不只是路径/数字,而是语义);④ 任何让你想去改代码或测试来「对齐文档」的诱因。

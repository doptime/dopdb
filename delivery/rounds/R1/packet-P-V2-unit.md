# 包: P-V2-unit · 回合 R1(2026-06-23 · 真实 MongoDB 首次验证)

分级: 🟢 可客观自证(各包 `go test` 退出 0 + 测试数对得上基线)
上游: 无(不依赖驱动;与 V0/V1 并行)
回执写到: `delivery/rounds/R1/receipt-P-V2-unit.md`(模板见 delivery/kit/00-protocol.md §3)

全景: STATUS.md 甘特 V2;不被阻塞的并行轨——V0/V1/V3 卡住时也能独立跑完。

任务一句话: 跑无驱动那批测试(数据/api/httpserve/config/memstore)确认全绿,并用 config.toml.example 跑一次 `config.Load` 冒烟。

## 1 背景 · 现在是什么情况

这批测试不碰 mongo 驱动,用内存 store + JSON codec。云端沙箱基线:**数据 10 + api 7 + httpserve 11 + config 6 = 34 测试全过**。本包在本地复跑坐实(harness 在模型回合之外复跑,RL8)。

## 2 意图 · 为什么做、什么算好

确认沙箱内核在本地工具链下行为一致,且配置读取器对真实示例文件可用。完成 = 四个测试包全绿 + 测试数对上 + config 冒烟通过。

红线: RL1–RL8 全部适用。**RL2**:测试不过就如实记,绝不改测试让它过。本包追加: 无。

## 3 任务 · 具体做什么

### 单元 1 · 无驱动测试全套

```bash
go test -count=1 -v . ./api ./httpserve ./config ./memstore 2>&1 | tee delivery/rounds/R1/unit.txt
echo "EXIT: $?"
go test -count=1 . ./api ./httpserve ./config ./memstore 2>&1 | grep -E "^(ok|FAIL|---)"
```

逐包数 PASS 行,抄进回执关键数字。

### 单元 2 · config.Load 冒烟(对真实示例文件)

```bash
# 用示例配置 + 临时密钥环境变量,确认解析+env覆盖+校验
DOPTIME_JWT_SECRET=smoke DOPTIME_MONGO_URI='mongodb://localhost:27017' \
  go test -count=1 -run TestLoadAndEnvOverride ./config 2>&1 | tail -3
echo "EXIT: $?"
```

(`config.toml.example` 的真实加载已被 config 测试覆盖等价逻辑;此处再确认带 env 的那条用例过。)

## 4 验收 · 怎么算完成(harness 复跑,云端三层审计再复核)

- [ ] `go test . ./api ./httpserve ./config ./memstore` 退出 0,四个包均 `ok`
- [ ] 关键数字:数据 10 / api 7 / httpserve 11 / config 6(若与基线不符,记异常)
- [ ] config 那条 env 用例退出 0
- [ ] `unit.txt` 留痕;进度账落 `delivery/rounds/R1/progress.md`

## 5 边界 · 不要做什么

可写:`delivery/rounds/R1/`。
禁改:任何源码与测试(L0 测试不可碰)。越界登记 oob。

## 6 预算与换法

单元 1 ~1 分钟;失败时:第 1 次重跑确认非偶发;第 2 次定位是哪个包哪条;第 3 次记 failed + 关键报错一行(**不修测试**)。整包 ~3 分钟。

## 7 收尾

按协议 §3 写回执;关键数字逐包抄 PASS 数。「异常发现」必写:① 测试数与基线 34 不符;② 任一包 FAIL(附首条报错);③ 本地行为与沙箱不一致(如时间戳/nanoid 相关);④ config 校验在本地表现异常。

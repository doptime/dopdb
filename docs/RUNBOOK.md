# RUNBOOK · dopdb 构建 / 测试 / 运行 / 迁移

> 操作手册。谁来跑都照这个走。环境:Go 1.22+。

## 构建

```bash
# 不含驱动的全部包(任何环境都能构建)
go build ./...      # 若本机无 mongo 驱动会在 mongostore 处失败,见下

# 仅核心(确定无驱动依赖)
go build . ./api ./httpserve ./config ./memstore
```

`mongostore` 是唯一 import MongoDB 驱动的包。要构建它,先在 `go.mod` 加依赖:

```bash
go get go.mongodb.org/mongo-driver/v2@latest
go build ./mongostore
```

> 注:本框架在作者沙箱里写就,沙箱网络到不了 `go.mongodb.org`/`golang.org/x`,所以 `mongostore` 与 `mongostore.NewFromSource` **未在沙箱编译过**。请在装了驱动的环境构建一遍;若 v2 某个点版本改了 options 签名(`options.Client()/Replace()/Update()/Find()/Index()` 这套 builder),那是机械修正,`Store` 契约不变。

## 测试矩阵

| 命令 | 覆盖 | 依赖 |
|---|---|---|
| `go test . ./api ./httpserve ./config ./memstore` | 数据核心 + API 流水线 + HTTP 端到端(memstore)+ 配置 | 无,任何环境 |
| `go test ./mongostore` | 适配器对真实驱动的契约 | mongo 驱动 + 运行中的 MongoDB |
| `go test ./...` | 全部 | 同上 |

沙箱可跑的那条目前 **34 测试全过**(数据 10 + api 7 + httpserve 11 + config 6;memstore 无测试)。

```bash
go vet ./...           # 静态检查
gofmt -l .             # 应空输出(无未格式化文件)
```

## 运行

1. `cp config.toml.example config.toml`,改 `db`、`cors_origins`、`addr`。
2. 设密钥环境变量(**别写进文件**):
   ```bash
   export DOPTIME_JWT_SECRET='…HS256 密钥或 RS256 PEM…'
   export DOPTIME_MONGO_URI='mongodb://user:pw@host:27017/?authSource=admin'
   ```
3. 装配并起服务(代码见 `docs/03-config.md`)。生产务必 `auto_auth=false`。

### MongoDB 部署要点

- **多文档 ACID 事务**需副本集部署(单机不支持事务)。
- 持久化用 WiredTiger journal:写关注 `w:majority, j:true`。
- 自托管全文 / 向量检索(`$search/$vectorSearch`)需 MongoDB 8.2+ 的 `mongot`;若用到 redisdb 时代的 RediSearch/VectorSet 能力,在此对接(上线前复核该版本 GA 状态)。

## 从 doptime / redisdb 迁移

1. **结构体标签**:`msgpack:"alias:x"` → `bson:"x"`;补 `json:"x"` 同名(可脚本)。见 `01-data.md`。
2. **主键**:非字符串主键别打 `bson:"_id"`(`_id` 永远是字符串);主键留外部。
3. **数据 API**:`data.New[K,V]()` → `dopdb.New[K,V]()`,方法同名。
4. **API**:`api.Api(...)` 流水线与命名规则不变;`.Func` 本地直调。
5. **配置**:Redis 数据源表 → `[[mongo]]`;密钥进环境变量。
6. **历史数据搬迁**:msgpack blob → BSON 文档需一次性转换(读旧 Redis hash → 反序列化 → `HSet` 进 Mongo);属一次性脚本,按集合逐个搬。
7. **去掉的能力**:ZSet/Set 抽象、阻塞列表、Redis-stream RPC——迁移前确认业务没依赖;ZSet 排行榜改用「带 score 字段 + 降序索引 + `Find` 排序」。

## 未竟项(按优先级,均非主干)

1. ~~**原子 scoped-write 原语**~~ → ✓ 已完成(R3):`Store.PutScoped` filtered-upsert + `Collection.HSetScoped` 原子 scoped 写;跨主→`ErrForbidden`。见 `01-data.md` 方法表。
2. ~~**scoped 的 `HKEYS/HLEN`**~~ → ✓ 已完成(R3):scoped 集合现返回**调用者本人**的键/计数,不泄漏。
3. **msgpack-at-rest** → ✗ 已砍(R3):JSON+BSON 两 codec 已足,价值最低。
4. ~~**`_permissions` 持久化**~~ → ✓ 已完成(R3):`Permissions.SaveJSON/LoadJSON` 文件式落盘/恢复;启动时 `LoadJSON` 载入,`AutoAuth` 默认 false(生产安全)。
5. **TS 重写**:最终目标——TS 里类型定义一次(Zod schema→infer+运行时校验),去掉 Go→TS 生成。

## 红线提醒(给执行方)

跑测试 / 验收时不改测试、不改门槛、不改守卫来让自己过(RL2);代码退出码 0 ≠ 任务完成,看语义指标(RL5);如实报告,诚实失败优于假装成功(RL6);守卫脚本前置缺失必须非零退出(RL7)。密钥只走环境变量(RL4)。

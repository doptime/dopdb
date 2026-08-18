# delivery/ — 历史过程日志(R1–R11,MongoDB 时期)

⚠️ **不要照本目录配置环境或理解现行架构。**

本目录是 dopdb 在 **KVRocks 迁移之前**(后端为 MongoDB)的交付过程记录:轮次台账、
评审回执、项目卡。它保留下来是为了追溯"当时为什么这么决定",不是为了描述当前系统。

其中已经**全部作废**的内容包括,但不限于:

| 过时说法 | 现行事实 |
|---|---|
| 后端 MongoDB / 需要副本集 | KVRocks(Redis 协议),单实例即可 |
| `DOPDB_TEST_MONGO_URI` | `DOPDB_TEST_KVROCKS_URI` |
| `bson:"..."` 结构体标签 | 已删除;字段名来自 `json:"..."`,存储格式 CBOR |
| `mongo.go` / mongostore / memstore | `kvrocks.go` + `codec.go` + `query.go` + `index.go` |
| `[[mongo]]` 配置段、`db = "..."` | `[[kvrocks]]` 配置段、`namespace = "..."` |
| watch = change stream(可续传) | watch = dopdb 自建 pub/sub 频道(**不可续传**) |
| 索引由服务端建立 | 只有 `unique` 由 dopdb 自己强制;其余标签为空操作 |

**现行事实以仓库根目录为准**:`README.md`、`AGENTS.md`、`docs/`(尤其
`docs/01-data.md`、`docs/RUNBOOK.md`)、`MIGRATION-KVROCKS.md`。

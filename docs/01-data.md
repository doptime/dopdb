# 01 · 数据层(Collection · 方法 · modifiers · 索引 · 查询)

> 数据层 = redisdb-on-Mongo。核心抽象 `Collection[K,V]` 是 redisdb `HashKey` 的对应物:**一个 hash-of-structs 就是一个带主键的文档集合**,Mongo 原生就存这个。本文件讲怎么定义、怎么读写、怎么查。

## 定义一个集合

```go
type User struct {
    UID       string    `bson:"_id"   json:"_id"   index:"unique"`
    Name      string    `bson:"name"  json:"name"  mod:"trim" validate:"required"`
    Email     string    `bson:"email" json:"email" mod:"trim,lowercase" index:"1"`
    Role      string    `bson:"role"  json:"role"  mod:"default=member"`
    CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
    UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

users := dopdb.New[string, *User](dopdb.WithCollection("User"))
```

- `New[K,V](opts...)`:`K` 是主键类型,`V` 是文档类型(struct 或 *struct)。
- 集合名默认由 `V` 的类型名推导(标量类型不合法,须 `WithCollection`)。
- 构造时**自动 EnsureIndex**(从 `index:` 标签,幂等)。
- 选项:`WithDB(name)`(选数据源)、`WithCollection(name)` / `WithKey(name)`(集合名)、`WithStore(s)`(本集合单独换后端,迁移用)。

### 进程级默认(启动时设一次)

```go
dopdb.SetDefaultStore(st)              // st 来自 mongostore.New(...) 或 memstore.New()
dopdb.SetDefaultCodec(mongostore.BSONCodec{})  // 测试里用 memstore.JSONCodec{}
dopdb.SetValidator(func(v any) error { … })    // 可选;api 包也复用这一个
```

## 标签约定(重要:bson == json)

- **`bson:"x"`**:落盘字段名(生产 codec 用)。
- **`json:"x"`**:HTTP 入参绑定字段名(`@`-绑定与请求解码用)。**约定与 `bson` 取同名**,从根上消除命名漂移。
- **`json:"@uid"`**:特殊——该字段从 `@`-上下文(已验证 JWT)填充,客户端伪造不了(见 `02-http.md`)。
- `mod:"…"`:写入期变换(见下)。`validate:"…"`:交给你装的校验器。`index:"…"`:索引声明。

`_id` **永远是规范字符串**(string→自身,整数→base10,其余→JSON)。所以:**结构体里映射到 `_id` 的字段必须是 string**;非字符串主键就别打 `bson:"_id"`,把主键留在外部(经 `HKeys` 仍能取回原类型)。这也规避了 doptime 文档警告的数字主键变科学计数法的坑。

## 方法(与 redisdb 同名,行为已修正)

| 方法 | 语义 | Mongo 落地 |
|---|---|---|
| `HSet(k, v)` | upsert | `replaceOne({_id:k}, v, upsert)` |
| `HSetScoped(k, v, ownerField, ownerVal)` | **原子** scoped upsert;跨主→`ErrForbidden` | `updateOne({_id, owner:ownerVal}, {$set}, upsert)` + dup-key→403 |
| `HSetNX(k, v)` | 不存在才插 | `updateOne({_id:k}, {$setOnInsert}, upsert)` |
| `Save(v)` | 从 `v` 的主键字段推导 k 再 upsert | 同 HSet |
| `HGet(k)` | 取值,无则 `ErrNoDoc` | `findOne({_id:k})` |
| `HMGet(k…)` | 批量,缺失项=零值,**解码失败=error**(不再静默丢) | `find({_id:{$in}})` |
| `HMSet(map)` | 批量 upsert | `bulkWrite` |
| `HGetAll()` | 全部 k→v | `find({})` |
| `HDel(k…)` / `Del(k)` | 删 | `deleteMany` |
| `HExists(k)` | 是否存在 | `countDocuments` |
| `HKeys()` / `HVals()` / `HLen()` | 键 / 值 / 计数 | 投影 / find / count |
| `HKeysScoped(scope)` / `HLenScoped(scope)` | **只返回调用者本人**的键/计数(不泄漏) | `find(scope)` 取 `_id` |
| `HIncrBy(k, field, n)` | **原子**增数值字段(点路径) | `$inc`(修掉了 redisdb 非原子计数) |
| `Find(filter, opt)` / `FindOne(filter)` | 按字段内容查(KV 模型没有的能力) | `find` + 净化 |

所有方法在**写入时**跑完 modifiers + 时间戳 + 校验(不是读时,不只 HTTP)。

## 写入期 modifiers(`mod:` 标签)

写入时按从左到右执行,落盘前完成。值填充类(default/unixtime/counter/nanoid)默认只在零值时触发,加 `,force` 强制。

| mod | 作用 |
|---|---|
| `trim` / `lowercase` / `uppercase` / `title` | 字符串变换 |
| `default=X` | 零值时填 X(按字段类型解析) |
| `unixtime` / `unixtime=ms` | 零值时填当前秒 / 毫秒 |
| `counter` | 数值 +1 |
| `nanoid` / `nanoid=N` | 零值时填长度 21 / N 的 nanoid |
| 字段名 `CreatedAt`(time.Time) | 零值时填 now |
| 字段名 `UpdatedAt`(time.Time) | 每次写都填 now |

校验:`dopdb.SetValidator(fn)` 装一个进程级校验器,写入时对值调用(可接 go-playground/validator honor `validate:` 标签)。

## 索引(`index:` 标签 → 启动时 EnsureIndex)

| 标签值 | 含义 |
|---|---|
| `1` / `asc` | 升序单字段索引 |
| `-1` / `desc` | 降序 |
| `text` | 全文 |
| `2dsphere` | 地理 |
| `unique` | 唯一(可组合,如 `1,unique`) |

前端驱动的查询在真数据库上没有索引就退化成全表扫描,所以 dopdb 把索引做成**类型定义的一部分**。

## 查询与净化(Mongo 的超能力,但闭合)

```go
adults, _ := users.Find(dopdb.M{"age": dopdb.M{"$gte": 18}}, dopdb.FindOpt{Limit: 50})
```

KV 模型只能按主键取;Mongo 能按字段内容查。但**任意 Mongo 查询面=注入风险**(`$where` 跑 JS、`$function`、跨集合 `$lookup`)。所以**每个 `Find` 都过 `SanitizeFilter`**:

- **白名单算子**:比较(`$eq/$ne/$gt/$gte/$lt/$lte/$in/$nin`)、逻辑(`$and/$or/$nor/$not`)、元素(`$exists/$type`)、数组(`$all/$elemMatch/$size`)、`$regex/$options/$mod`。
- **黑名单(直接拒)**:`$where/$function/$accumulator/$expr/$lookup/$graphLookup/$unionWith/$merge/$out/$facet`。
- 递归走查、最大深度 12、字段路径里禁止藏 `$`。

从 HTTP 层进来的 `Find` 走同一个净化器——前端拿不到任意查询面。字段级作用域(哪些字段可查、强制 `owner==@uid`)由 HTTP 层叠加,见 `02-http.md`。

## 迁移提示(从 redisdb 结构体)

1. `msgpack:"alias:x"` → `bson:"x"`(可脚本批量替换)。
2. 给每个字段补 `json:"x"`,与 `bson` 同名。
3. 主键字段若是非字符串又打了 `_id`,改为外部主键或换 string。
4. `data.New[K,V]()` → `dopdb.New[K,V]()`,方法名不变。

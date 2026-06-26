# 01 · 数据层(Collection · 方法 · modifiers · 索引 · 查询 · 数据源)

数据层是可信面:服务端代码直接读写,无 owner-scope、无 JWT。Go 是泛型 `Collection[K, V]`,TS 是 `serverDb(schema, db)` 返回的带类型 `db`。

## 直连 MongoDB(无 Store 抽象)

dopdb **不再有 `Store`/`Codec` 抽象**,也没有 `memstore`/`mongostore`。根包直接用 `go.mongodb.org/mongo-driver/v2`:`mongo.go` 里的具体 `mongoBackend{db *mongo.Database}` 内联了原 mongostore 的全部 driver-v2 写法(ReplaceOne/UpdateOne upsert、FindOne.Raw、Find 游标、CountDocuments、BulkWrite、唯一索引、`IsDuplicateKeyError` 等)。

编解码直接走 `bson.Marshal`/`bson.Unmarshal`;字符串键即文档 `_id`。

## 定义集合

```go
type User struct {
    Name  string `json:"name"  bson:"name"`
    Email string `json:"email" bson:"email" index:"unique"`
    Age   int    `json:"age"   bson:"age"   index:"1"`
    Loc   []float64 `json:"loc" bson:"loc"  index:"2dsphere"`
}
users := dopdb.New[string, *User](dopdb.WithCollection("users"))
```

`New[K,V]` 选项:`WithCollection(name)`(= `WithKey`)、`WithDB(name)`(绑定数据源,用于原生方法;HTTP 侧由 `?ds=` 决定)。`New` 只登记索引规格,不建立连接;首次实际操作某数据源时按需建索引(每数据源一次)。

索引来自结构体 tag `index:"..."`:`"unique"` 唯一、`"1"`/`"-1"` 升/降序、`"text"` 文本、`"2dsphere"` 地理(走 `IndexSpec.Geo`)、TTL 等。

## 原生方法(签名)

```
HSet(k, v) / Save(v)                 upsert(Save 从 v._id 取键)
HSetNX(k, v) (bool, err)             不存在才写
HSetScoped(k, v, ownerField, val)    过滤 upsert(行级隔离的底座)
HGet(k) (V, err)                     取一条(无 → ErrNoDoc)
HMGet(...k) ([]V, err)               批量取(对齐,缺失为零值)
HMSet(map[K]V)                       批量写
HGetAll() (map[K]V, err)             全部
HDel(...k) / Del(k)                  删
HExists(k) (bool, err)
HKeys() ([]K, err) / HVals() ([]V, err) / HLen() (int64, err)
HIncrBy(k, fieldPath, int64)         原子整数 $inc
HIncrByFloat(k, fieldPath, float64)  原子浮点 $inc
Find(filter M, opt FindOpt) ([]V, err)
FindOne(filter M) (V, err)
```

TS 端等价方法名一致(`hget/hset/.../find/findone/watch` + `get/set/save` 别名)。

## 写入修饰器(modifiers)

`modifiers.go` 在写入前对值做处理:填充时间戳(如 `createdAt`/`updatedAt`)、按 `@`-绑定填充服务端字段(身份等)。可信路径默认信任传入值;HTTP 路径会先剥除客户端 `@`-键再填充。

## 查询与消毒

`Find`/`FindOne`/`Count` 接受 Mongo 风格过滤器(`dopdb.M`)。`FindOpt`:`SortKeys []SortKey{Field, Asc}`(多键有序)、`Sort`(单键 map)、`Limit`、`Skip`、`Projection`。

所有外来过滤器先过 `sanitize.go`:拒绝把 `$`-运算符当作字段键注入(防注入),`$in`/`$and` 等作为值的运算符按需放行。

## 多数据源

`mongo.go` 维护 `Datasources` 注册表(名 → `*mongo.Database`,缺省名 `default`):

```go
ds := dopdb.NewDatasources()
ds.Add("default", client.Database("appdb"))
ds.Add("analytics", client.Database("analytics"))
dopdb.SetDatasources(ds)
// 或从配置一次性连接:
ds, _ := dopdb.ConnectDatasources(ctx, []dopdb.DatasourceConfig{{Name:"default", URI:uri, DB:"appdb"}})
```

原生方法用集合绑定的数据源(`WithDB`,缺省 `default`);HTTP 请求用 `?ds=<name>` 选择,缺省 `default`,**请求参数对 HTTP 优先**。

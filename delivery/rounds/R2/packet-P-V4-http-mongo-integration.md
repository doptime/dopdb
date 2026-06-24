# 包: P-V4-http-mongo-integration · 回合 R2(2026-06-24 · 真实 Mongo 上的 HTTP 全栈端到端)

分级: 🔴 需判断/承重。**硬判据(本地据此自裁)**:设了 `DOPTIME_TEST_MONGO_URI` 时,`go test -run TestHTTPMongo -v ./httpserve` **退出 0 且输出含 `INTEGRATION OK`、无 `SKIP`**,即 done;无 URI/连不上 → skip 报因 → 终态 B,记 **suspend**;某断言不过 → failed,记关键数字 + 异常发现,**不改测试/不改代码**,suspend 交云端。
上游: R1 已封存(V3:mongostore 数据层在真 Mongo 验过)。
回执写到: `delivery/rounds/R2/receipt-P-V4-http-mongo-integration.md`(模板见 delivery/kit/00-protocol.md §3)

全景: STATUS.md 甘特 R2 的 V4,本回合**唯一承重里程碑**。它验过之前不进 H* 硬化。

任务一句话: 新建 `httpserve/http_integration_test.go`(逐字按本包),用真实 `mongostore` 后端跑通 httpserve 全栈端到端——JWT → `@`-绑定 → 权限 → 数据命令 + `/api/<name>` → BSON 往返,并坐实 JSON-进/BSON-落盘/JSON-出 的字段映射(V3 遗留)。

## 1 背景 · 现在是什么情况

R1 的 V3 坐实了**数据层**(`mongostore` 契约)。它**上面那层**——`httpserve` 的 JWT 验证、`@`-绑定注入、`command::collection` 权限、命令派发、api 流水线——至今只在 **memstore(JSON codec)** 上验过(见 `serve_test.go`),**从未在真 Mongo + BSON 往返上端到端跑过**。尤其 V3 明确留坑:HTTP 入口 **JSON 解码**、`mongostore` **BSON 落盘**,`bson:"_id"`/`json:"_id"`/`json:"@uid"` 这套字段在「JSON 进 → BSON 存 → 取回」全链路是否对齐、`@uid` 是否正确从 JWT 注入且伪造被剥离——本包抓这个 facade。

测试只调公开 API(`httpserve` 的 Handler、`mongostore.New`/`BSONCodec`、`dopdb.New`/`RegisterHttp`/`SetOwnerScope`、`api.Api`),外加一个独立驱动 client 直读 Mongo 验 BSON-at-rest。每次跑用唯一库名,跑完 drop。

## 2 意图 · 为什么做、什么算好

把「框架在真 Mongo 上 HTTP 全栈正确」从假设变证据。完成的精确定义见硬判据 + §4。

红线: RL1–RL8 + PRL1–PRL4 全适用。**RL2/RL5/RL6 重点**:断言不过就如实 failed,绝不改测试/门槛/被测代码让它过;退出 0 还要看断言语义;skip 是诚实负结果。**PRL3 尤其**:`@uid` 只能来自 JWT,客户端伪造(query 或 body)必须被剥离——本包专测此项。
修改令: 允许**新建** `httpserve/http_integration_test.go`;不改任何现有文件(`serve_test.go` 等测试、`*.go` 逻辑、L0 冻结件)。

## 3 任务 · 具体做什么

### 单元 1 · 创建集成测试文件

新建 `httpserve/http_integration_test.go`,内容如下(逐字)。它与 `serve_test.go` 同包,复用其辅助(`do`/`tokenFor`/`decodeObj`/`testSecret`/`Profile`/`Order`):

```go
package httpserve

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/doptime/dopdb"
	"github.com/doptime/dopdb/api"
	"github.com/doptime/dopdb/mongostore"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Run against a disposable MongoDB by setting DOPTIME_TEST_MONGO_URI. Without it
// the test skips (honest terminal B), never a false pass. Each run uses a unique
// database that is dropped on cleanup.
//
// This exercises the full httpserve stack on a real Mongo backend: JWT -> @-binding
// -> permission -> data commands + /api/<name> -> BSON round-trip, and verifies the
// JSON-in / BSON-at-rest / JSON-out field mapping (the codec concern V3 deferred).

// CodecProfile uses identical bson/json field names; timestamps fill by Go field
// name (CreatedAt/UpdatedAt); role defaults on write.
type CodecProfile struct {
	UID       string    `bson:"_id" json:"_id"`
	Name      string    `bson:"name" json:"name" mod:"trim"`
	Role      string    `bson:"role" json:"role" mod:"default=member"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

type NoteDoc struct {
	ID   string `bson:"_id" json:"_id"`
	Text string `bson:"text" json:"text"`
}

// SaveNote: @uid is injected from the JWT through the api pipeline; note from body.
type SaveNote struct {
	UID  string `json:"@uid"`
	Note string `json:"note"`
}
type NoteOut struct {
	Saved string `json:"saved"`
	Owner string `json:"owner"`
}

var httpMongoAPIOnce sync.Once

func registerHTTPMongoAPIs() {
	notes := dopdb.New[string, *NoteDoc](dopdb.WithCollection("Notes"))
	api.Api(func(in *SaveNote) (*NoteOut, error) {
		if err := notes.HSet(in.UID, &NoteDoc{ID: in.UID, Text: in.Note}); err != nil {
			return nil, err
		}
		got, err := notes.HGet(in.UID)
		if err != nil {
			return nil, err
		}
		return &NoteOut{Saved: got.Text, Owner: in.UID}, nil
	}, api.WithName("savenote"))
}

func freshHandlerMongo() *Handler {
	return NewHandler(NewServer(testSecret), NewPermissions(true /* AutoAuth: dev */))
}

func keysSorted(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// craftNoneToken builds a forged "alg":"none" JWT (no signature) that must be rejected.
func craftNoneToken(uid string) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"uid":%q,"exp":%d}`, uid, time.Now().Add(time.Hour).Unix())))
	return hdr + "." + pl + "."
}

func TestHTTPMongo(t *testing.T) {
	uri := os.Getenv("DOPTIME_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DOPTIME_TEST_MONGO_URI not set — skipping real-Mongo http e2e (terminal B)")
	}
	ctx := context.Background()
	dbName := "dopdb_http_it_" + time.Now().UTC().Format("20060102T150405")

	st, err := mongostore.New(ctx, uri, dbName)
	if err != nil {
		t.Fatalf("mongostore.New: %v", err)
	}
	dopdb.SetDefaultStore(st)
	dopdb.SetDefaultCodec(mongostore.BSONCodec{})
	dopdb.SetValidator(nil)
	dopdb.RegisterHttp(dopdb.New[string, *Profile](dopdb.WithCollection("Profile")))
	dopdb.RegisterHttp(dopdb.New[string, *Order](dopdb.WithCollection("Order")))
	dopdb.RegisterHttp(dopdb.New[string, *CodecProfile](dopdb.WithCollection("CodecProfile")))
	dopdb.SetOwnerScope("Order", "owner", "uid")
	httpMongoAPIOnce.Do(registerHTTPMongoAPIs)

	// Separate driver client for raw-BSON inspection + teardown (drops the DB).
	cli, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("driver connect: %v", err)
	}
	db := cli.Database(dbName)
	t.Cleanup(func() {
		_ = db.Drop(ctx)
		_ = cli.Disconnect(ctx)
	})

	// 1) JWT: rejection paths -> 401, valid -> 200.
	t.Run("1-jwt", func(t *testing.T) {
		h := freshHandlerMongo()
		if rr := do(h, "GET", "/HGET-Order?f=o1", "", ""); rr.Code != http.StatusUnauthorized {
			t.Errorf("no-token (scoped) status=%d want 401", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Profile?f=@uid", "", "not.a.token"); rr.Code != http.StatusUnauthorized {
			t.Errorf("malformed-token status=%d want 401", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Order?f=o1", "", craftNoneToken("u1")); rr.Code != http.StatusUnauthorized {
			t.Errorf("alg:none token status=%d want 401", rr.Code)
		}
		if rr := do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Jwt"}`, tokenFor(t, "u1")); rr.Code != http.StatusOK {
			t.Errorf("valid-token HSET status=%d want 200 (body=%s)", rr.Code, rr.Body.String())
		}
	})

	// 2) @-binding from JWT + forgery stripped (query and body).
	t.Run("2-at-binding-forgery", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		if rr := do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"  Alice  "}`, u1); rr.Code != http.StatusOK {
			t.Fatalf("HSET status=%d body=%s", rr.Code, rr.Body.String())
		}
		do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Bob"}`, tokenFor(t, "u2"))
		obj := decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid", "", u1))
		if obj["_id"] != "u1" {
			t.Errorf("_id=%v want u1 (key bound from JWT)", obj["_id"])
		}
		if obj["name"] != "Alice" {
			t.Errorf("name=%v want Alice (trim on write)", obj["name"])
		}
		// u1 smuggles @uid=u2 in the query -> must still read u1's own.
		obj = decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid&@uid=u2", "", u1))
		if obj["name"] != "Alice" {
			t.Errorf("query forgery not blocked: name=%v want Alice", obj["name"])
		}
	})

	// 3) Permission whitelist: explicit Deny -> 403; AutoAuth first-use -> allowed.
	t.Run("3-permission", func(t *testing.T) {
		h := freshHandlerMongo()
		h.Perms.Deny("HGETALL", "Profile")
		if rr := do(h, "GET", "/HGETALL-Profile", "", tokenFor(t, "u1")); rr.Code != http.StatusForbidden {
			t.Errorf("denied HGETALL status=%d want 403", rr.Code)
		}
		h2 := freshHandlerMongo()
		do(h2, "POST", "/HSET-Profile?f=@uid", `{"name":"x"}`, tokenFor(t, "u1"))
		if rr := do(h2, "GET", "/HGET-Profile?f=@uid", "", tokenFor(t, "u1")); rr.Code != http.StatusOK {
			t.Errorf("auto-auth HGET status=%d want 200", rr.Code)
		}
	})

	// 4) Data commands @ real Mongo: HSET -> HGET -> HDEL -> HGET(404).
	t.Run("4-data-commands", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		do(h, "POST", "/HSET-Profile?f=@uid", `{"name":"Alice"}`, u1)
		obj := decodeObj(t, do(h, "GET", "/HGET-Profile?f=@uid", "", u1))
		if obj["name"] != "Alice" || obj["_id"] != "u1" {
			t.Errorf("HGET obj=%v want name=Alice _id=u1", obj)
		}
		if rr := do(h, "POST", "/HDEL-Profile?f=@uid", "", u1); rr.Code != http.StatusOK {
			t.Errorf("HDEL status=%d want 200", rr.Code)
		}
		if rr := do(h, "GET", "/HGET-Profile?f=@uid", "", u1); rr.Code != http.StatusNotFound {
			t.Errorf("HGET after HDEL status=%d want 404", rr.Code)
		}
	})

	// 5) /api/<name> @ real Mongo: handler writes+reads a collection; @uid from JWT
	// (forged body @uid stripped); verify the doc actually landed in Mongo.
	t.Run("5-api-at-mongo", func(t *testing.T) {
		h := freshHandlerMongo()
		rr := do(h, "POST", "/api/savenote", `{"note":"hello","@uid":"u2"}`, tokenFor(t, "u1"))
		if rr.Code != http.StatusOK {
			t.Fatalf("api status=%d body=%s", rr.Code, rr.Body.String())
		}
		obj := decodeObj(t, rr)
		if obj["saved"] != "hello" {
			t.Errorf("saved=%v want hello", obj["saved"])
		}
		if obj["owner"] != "u1" {
			t.Errorf("owner=%v want u1 (forged body @uid must be stripped)", obj["owner"])
		}
		raw := bson.M{}
		if err := db.Collection("Notes").FindOne(ctx, bson.M{"_id": "u1"}).Decode(&raw); err != nil {
			t.Fatalf("note not persisted to Mongo: %v", err)
		}
		if raw["text"] != "hello" {
			t.Errorf("persisted text=%v want hello", raw["text"])
		}
	})

	// 6) Codec field mapping (V3 deferred): HTTP JSON <-> BSON-at-rest <-> JSON.
	t.Run("6-codec-mapping", func(t *testing.T) {
		h := freshHandlerMongo()
		u1 := tokenFor(t, "u1")
		want := []string{"_id", "createdAt", "name", "role", "updatedAt"}

		// layer 1 — HTTP JSON in -> HTTP JSON out
		if rr := do(h, "POST", "/HSET-CodecProfile?f=@uid", `{"name":"  Alice  ","role":""}`, u1); rr.Code != http.StatusOK {
			t.Fatalf("HSET status=%d body=%s", rr.Code, rr.Body.String())
		}
		obj := decodeObj(t, do(h, "GET", "/HGET-CodecProfile?f=@uid", "", u1))
		if got := keysSorted(obj); !sameSet(got, want) {
			t.Errorf("HTTP field set=%v want %v", got, want)
		}
		if obj["_id"] != "u1" || obj["name"] != "Alice" || obj["role"] != "member" {
			t.Errorf("HTTP values: _id=%v name=%v role=%v (want u1/Alice/member)", obj["_id"], obj["name"], obj["role"])
		}
		if s, _ := obj["createdAt"].(string); s == "" {
			t.Errorf("HTTP createdAt empty")
		}
		if s, _ := obj["updatedAt"].(string); s == "" {
			t.Errorf("HTTP updatedAt empty")
		}

		// layer 2 — raw BSON at rest
		raw := bson.M{}
		if err := db.Collection("CodecProfile").FindOne(ctx, bson.M{"_id": "u1"}).Decode(&raw); err != nil {
			t.Fatalf("raw find: %v", err)
		}
		if got := keysSorted(map[string]any(raw)); !sameSet(got, want) {
			t.Errorf("BSON field set=%v want %v", got, want) // layer 3: same set as HTTP => aligned
		}
		if id, ok := raw["_id"].(string); !ok || id != "u1" {
			t.Errorf("BSON _id=%v (%T) want string \"u1\"", raw["_id"], raw["_id"])
		}
		if raw["role"] != "member" {
			t.Errorf("BSON role=%v want member (default persisted through BSON)", raw["role"])
		}
		if _, isStr := raw["createdAt"].(string); isStr {
			t.Errorf("BSON createdAt stored as string, want a Date")
		}
		if raw["createdAt"] == nil {
			t.Errorf("BSON createdAt missing")
		}
	})

	t.Logf("INTEGRATION OK against %s/%s", uri, dbName)
}
```

### 单元 2 · 运行

```bash
DOPTIME_TEST_MONGO_URI="mongodb://localhost:27017" \
  go test -count=1 -run TestHTTPMongo -v ./httpserve 2>&1 | tee delivery/rounds/R2/http_mongo.txt
echo "EXIT: $?"
grep -E "SKIP|--- PASS|--- FAIL|INTEGRATION OK" delivery/rounds/R2/http_mongo.txt
```

照硬判据自裁:无 URI → 输出含 `SKIP` → 终态 B、suspend;有 URI 且退出 0 且无 `SKIP` 且含 `INTEGRATION OK` → done(把各子测试现况 + 关键数字抄进回执);有断言 FAIL → failed + 关键数字 + 异常发现,suspend。

## 4 验收 · 怎么算完成

- [ ] `httpserve/http_integration_test.go` 已按上文**逐字**创建
- [ ] `go test -run TestHTTPMongo ./httpserve` 退出 0
- [ ] 输出含 `INTEGRATION OK`(证真跑,非 skip)——**或** 明确含 `SKIP` 且回执标 suspend(终态 B)
- [ ] 关键数字抄:六个子测试各自 PASS/FAIL;子测试 6 的 HTTP 字段集 与 BSON 字段集(应相等 = `[_id createdAt name role updatedAt]`);`_id` 在 BSON 中是否为 string;`role` 两层是否均 `member`;`@uid` 伪造是否被剥离(子测试 2/5)
- [ ] `http_mongo.txt` 留痕;进度账落 `delivery/rounds/R2/progress.md`

## 5 边界 · 不要做什么

可写:**新建** `httpserve/http_integration_test.go`、`delivery/rounds/R2/`。
禁改:`serve_test.go` 与任何现有测试、`httpserve/*.go` 逻辑、`store.go`/`sanitize.go`/`mongostore.go` 等、L0 冻结件。**绝不为让断言过而改被测代码或删/改断言**(RL2/PRL2/PRL4)。越界登记 `delivery/rounds/R2/oob.md`。

## 6 预算与换法 · 决策表

| 情况 | 动作 |
|---|---|
| URI 已设、连得上、六子测试全过、含 INTEGRATION OK | done;抄字段集等关键数字 |
| 无 URI / 连不上 | 测试 skip;终态 B;回执 suspend,写「无测试 Mongo,承重件证不了」 |
| 连接超时/认证失败 | 重试 1 次;仍失败 → 当无 Mongo,终态 B、suspend,记错误一行 |
| 子测试 6 字段集不符(HTTP ≠ BSON,或 ≠ 预期 5 字段) | **不改测试/代码**;failed,抄两个字段集实际值,异常发现写「JSON/BSON 字段映射串了——根因疑在 store.go 键编解码或 mongostore BSON 落盘」,suspend |
| `_id` 在 BSON 非 string,或 `createdAt` 落成 string | failed,异常发现写「codec 类型映射有误(_id 该恒 string / 时间戳该 Date)」,suspend |
| `@uid` 伪造未被剥离(owner=u2 落了盘) | **严重(PRL3)**;failed + 异常发现,suspend |
| 权限未按预期(deny 非 403 / 首用未授予) | 记关键数字 + 异常;按现象判 failed/suspend |
| 编译期报**驱动 API** 不匹配(`mongo.Connect`/`FindOne().Decode`/`db.Drop`/`Disconnect` 在 v2.7.0 形参不同) | **仅**机械适配这几处驱动调用为 v2.7.0 当前签名(参照已跑通的 `mongostore/mongostore.go`),记一行;不动测试断言与框架逻辑 |
| 编译期报**框架/辅助** 不匹配(`do`/`Perms.Deny`/`RegisterHttp` 等) | 说明云端把测试写错了——记 failed + 错误原文,suspend,**不改框架** |
| 测试需改框架才能过 | 那是真 bug——suspend 交云端,不改 |

整包 ~10 分钟。**唯一允许的代码改动**是上表「驱动 API 不匹配」那行的机械签名适配(仅限那 4 类驱动调用),其余一律不动。

## 7 收尾

按协议 §3 写回执;**关键数字必抄**:① 六子测试各 PASS/FAIL;② 子测试 6 的 HTTP 字段集与 BSON 字段集(逐字);③ `_id` 是否 string、`createdAt` 是否非 string(Date);④ `role` 两层是否 `member`;⑤ `@uid` 伪造剥离 yes/no(子测试 2 query、子测试 5 body);⑥ 测试 ran 还是 skip。「异常发现」必写:① 字段映射串;② codec 类型不符;③ `@uid` 伪造泄漏;④ 权限语义不符;⑤ 任何「退出 0 但语义不像完成」;⑥ 你被诱导去改框架/测试/断言的任何点。

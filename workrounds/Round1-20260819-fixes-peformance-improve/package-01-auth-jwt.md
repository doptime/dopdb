# package-01 · JWT 认证：算法钉死与 exp 失败关闭

> 严重度 🔴 **本轮最严重的一条**。对应审计 S2-1、S2-16、S2-35。

## 问题

### 1.1 算法混淆（S2-1）

两端都从**令牌自己的 header** 取验签算法：

```go
// httpserve/jwt.go（修复前）
switch hdr.Alg {
case "HS256":
    mac := hmac.New(sha256.New, []byte(secret))   // secret 可能是 RS256 公钥 PEM
```

RS256 部署下，配置的 secret 是**公钥**——按定义是公开材料，发布在 JWKS、经常直接
下发给前端。攻击者拿 PEM 原文当 HMAC 密钥，自铸 `{"alg":"HS256"}` 令牌，claims 里
填任意 `uid`：

- HS256 分支用同一段 PEM 文本做 HMAC 校验 → **签名通过**
- `uid` 是 owner-scope 信任的身份来源 → **行级隔离一并绕过**

这是 CVE-2016-10555 同族。后果是**完全认证绕过 + 跨租户任意读写**，且攻击材料是
公开的。

### 1.2 exp 失败开放（S2-35）

- Go：`if exp > 0 && exp < now` —— `exp=0` 或负数**跳过检查**，等于永不过期
- Go/TS：无法解析的 exp（如 `"not-a-number"`）被静默忽略，同样等于永不过期
- TS：`typeof exp === "number"` —— 字符串形式的 exp 完全不检查，而 Go 检查
  → **同一个令牌，两端结论相反**

### 1.3 畸形令牌 → 500（S2-16）

TS 的 `JSON.parse(header)` / `JSON.parse(payload)` 无 try/catch，SyntaxError 逸出到
兜底 500。客户端错误被报成服务端故障，监控上表现为服务异常；Go 同样输入返回 401。

### 1.4 401 语义（S2-35）

- Go 的 401 响应 `code` 是 `"validation"` —— 客户端无法按 code 分支
- 401 响应体带 `no secret configured` / `bad signature` 原文 —— 只对探测者有用

## 修复

**算法由配置的密钥形态决定，header 只有"同意"的份。**

```go
// 能 parse 成公钥 → 这是 RS256 部署，HS256 令牌一律拒绝
// 普通字符串     → 这是 HS256 部署，RS256 令牌一律拒绝
kind := classifySecret(secret)   // 按 secret 缓存，不重复解析 PEM
```

其余：
- `alg: "none"`/空 一律拒绝（大小写不敏感）
- exp **存在即校验**：`exp <= now` 即过期；无法解析视为畸形令牌而非无限期
- exp 接受数字与数字字符串，**两端一致**
- TS 全部解析路径包 try/catch → 401
- 401 统一 `code: "unauthorized"`，对外统一 `invalid token`，不再泄漏内部原因

## 涉及文件

| 文件 | 类型 |
|---|---|
| `httpserve/jwt.go` | 修改 |
| `httpserve/serve.go` | 修改（401 的 code 与消息） |
| `ts/src/server.ts` | 修改（verifyJWT 重写 + 测试导出） |
| `httpserve/jwt_algconfusion_test.go` | **新增** |
| `ts/test/jwt.test.ts` | **新增** |

## 验证

```bash
go test -count=1 -run 'TestJWT' -v ./httpserve/
cd ts && node --import tsx --test test/jwt.test.ts
```

两侧测试都是**真实攻击复现**，不是行为断言：生成 RSA 密钥对 → 用公钥 PEM 签一个
HS256 令牌 → 断言必须被拒绝，同时断言真正的 RS256 令牌仍然通过。

期望：Go 3 个用例全 PASS，TS 4 个用例全 PASS。

## 验收标准

1. RS256 部署下，用公钥 PEM 签的 HS256 令牌被拒绝。
2. RS256 部署下，正常 RS256 令牌仍然通过，claims 完整。
3. HS256 部署下，RS256 header 的令牌被拒绝；正常 HS256 令牌通过。
4. exp 的七种形态（未来/过去/0/负数/数字字符串未来/数字字符串过去/不可解析）
   在两端结论一致，且只有"未来"通过。
5. 畸形令牌在两端都是 401，**不是 500**。
6. 401 响应的 `code` 是 `unauthorized`，响应体不含 `bad signature` 之类内部原因。

## 风险与影响

- **行为变更**：如果有部署把 RS256 公钥配成 secret 却在签 HS256 令牌（本身就是
  误配），升级后这些令牌会被拒。这是修复的目的，不是副作用。
- **行为变更**：`exp: 0` 曾被当作永不过期，现在会被拒。签发方若用 0 表示"不过期"，
  应改为省略 exp（省略仍然放行）。
- 性能：`classifySecret` 按 secret 缓存，PEM 解析每个 secret 只做一次。

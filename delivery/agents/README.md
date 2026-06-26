# delivery/agents/ — 本地两层 agent 的模型绑定(omp 格式)

本目录是 dopdb 工作流里**本地自动**两层的 agent 定义,跟随 omp 的 agent frontmatter 约定。两层**必须跑在不同模型上**——审计层(GLM)独立于执行层(Qwen),否则"同模型代跑"= 无独立审计(2026-06-26 审计中的 facade 正是这么溜过去的)。

| Agent | 角色 | 模型 | 工具 |
|---|---|---|---|
| `glm-auditor.md` | 运营层 GLM(承重门三层审计 + 封存起草 + 对账 + 出包) | **`moyu/glm-5.2`** | read, search, find, bash, edit |
| `qwen-executor.md` | 执行层 Qwen(照包执行 + 自检 + 回执) | 本地执行模型(占位 `moyu/qwen3-coder`,**≠ glm-5.2**) | read, search, find, bash, edit, write |

> 意图层 Opus(强模型、人工上传、远端)**不在此目录**——它不跑本地循环,由人把整仓 zip 上传到 Opus 会话,无需 agent 定义。

## 怎么把审计绑定到 moyu/glm-5.2(三选一,优先级 CLI > config.yml > frontmatter)

**① Agent frontmatter(本目录已写死,推荐)**
```yaml
# glm-auditor.md 头部
model: moyu/glm-5.2
```
覆盖 bundled 的同名 agent 定义,强制审计用 glm-5.2。

**② config.yml 的 modelRoles(按角色绑定)**
```yaml
modelRoles:
  task: moyu/glm-5.2      # 审计/运营这类"任务"角色走 glm-5.2
# 注:角色键名以 omp 实际 schema 为准(task / reviewer / auditor 等)
```

**③ CLI 临时指定(单次跑)**
```bash
omp --model moyu/glm-5.2
```

## 放置位置
omp 从 `~/.omp/agent/agents/` 读 agent 定义。把本目录的两个 `.md` **拷贝或软链**过去:
```bash
mkdir -p ~/.omp/agent/agents
cp delivery/agents/glm-auditor.md delivery/agents/qwen-executor.md ~/.omp/agent/agents/
# 或软链,保持与仓库同步:
# ln -sf "$PWD"/delivery/agents/*.md ~/.omp/agent/agents/
```
仓库内保留这两个文件是单一真相源;`~/.omp/...` 下是 omp 实际加载的副本。

## 两个模型参数(按 GLM-5.2 实际能力调)
- `contextWindow: 131072` — 上下文窗口(128K)。GLM-5.2 若官方支持更大(128K+),按实际上调。
- `maxTokens: 16384` — 单次输出上限。模型支持更长输出可适当加大(审计回合常要逐条对照,留足输出有好处)。
> 这两个参数在 omp 里可能属**模型定义**(模型注册表/providers 段)而非 agent frontmatter,键名以 omp 的 schema 为准;若 frontmatter 不识别,移到对应模型定义处即可。本目录的 frontmatter 一并写了,识别不到时无害。

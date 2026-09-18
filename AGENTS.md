## 项目概览

**Agent Router** —— Wails v2 桌面应用，本地模型路由网关。React 界面配置 LLM 提供商（OpenAI、Anthropic、Gemini）、模型映射、本地 API key；本地 HTTP 服务（`127.0.0.1:9400`）暴露 OpenAI 兼容的 `/v1/chat/completions`、Anthropic 的 `/v1/messages`，以及 `/v1/responses`（Codex CLI 的线协议，内部转成 Chat Completions 转发），按映射转发到上游提供商。

- Go 后端在仓库根目录与 `backend/`（模块 `agent-router`，go 1.25.0，sqlite3 需 cgo）；Codex 的 `config.toml` 模板用 `github.com/pelletier/go-toml/v2` 的 `unstable` 包定位行号，写回仍是行级 splice
- React 18 + TypeScript + Vite 前端在 `frontend/`
- Wails v2.14.0 粘合二者：`main.go` 嵌入 `frontend/dist`，绑定 `*App`

`AGENTS.md` 有更完整的中文版架构说明，两者需同步维护。

## 开发命令

包管理器：**npm**。需要 Wails CLI、cgo（mattn/go-sqlite3）。

```bash
wails dev                  # 开发（热重载 Go + Vite）
wails build                # 构建（tsc -b + vite build → go build，嵌入 frontend/dist）
go build ./...             # 纯 Go 构建；需先存在 frontend/dist
go test ./...              # Go 测试
go test ./backend/proxy/ -run TestXxx   # 单个测试
cd frontend && npm run dev        # 仅前端 vite
cd frontend && npm run build      # tsc -b && vite build（类型检查门禁）
cd frontend && npm run formatter  # 前端格式化（改前端后必须跑）
```

无 lint 配置、无 CI、无 Makefile。

## 架构与数据流

分层 Go 后端，单向依赖流（叶子 → 门面）：

```
storage（SQLite 叶子层）  ← 以 *sql.DB 注入给消费者
provider / config / apikey / usage  ← 通过构造函数消费 *sql.DB
credential  ← 消费 *sql.DB + secret.Store，独占上游凭据池
secret（Keychain）+ agent（内存）+ envcfg + templates  ← 不依赖业务 DB
proxy（后端顶层）  ← 依赖 provider、config、credential、usage
app.go（package main）  ← 组合根，组装所有组件
main.go  ← Wails 引导，绑定 *App；tray_darwin.go / tray_windows.go / tray_other.go 提供各平台托盘（窗口关闭=隐藏到托盘）
```

数据流：

```
React UI → App 方法（Wails 绑定）
  → SQLite 存储（providers/mappings/apikeys/usage/provider_credentials）  |  Keychain（上游 API 密钥）
  → proxy.Server（127.0.0.1:9400）→ credential.Pool 选 Key → Adapter → 上游 LLM
  → usage.SQLiteTracker → SQLite（含 credential_id/credential_name/credential_mask）
```

**必须保留的架构决策：**

- **上游凭据绝不持久化到 SQLite。** 真实 token 仅存 macOS Keychain（`security` 命令，服务名 `com.agentrouter.credentials`）；`provider_credentials` 行只存不透明 `secret_ref`。参见 `app.go` 的凭据绑定与 `backend/secret/keychain.go`。`providers.api_key_ref` 收窄为遗留字段：仅作为升级前单 Key 的载体，由 `credential.Pool` 在首次使用时认领为第一条凭据，新逻辑不再读它选 Key。本网关自身的 key（`backend/apikey/`）存 SQLite，仅写入用户新终端环境的 `AGENT_ROUTER_API_KEY`（`backend/envcfg/`）供 CLI 客户端认证。
- **掩码是唯一的例外，且只允许派生形式。** `credential.MaskSecret` 产出 `ss****sfg`（首 2 尾 3），落库在 `provider_credentials.mask` 与 `request_logs.credential_mask`，供 UI 与日志辨识是哪个 Key，无需回读 Keychain，且凭据删除后历史日志仍可读。它刻意短到不可用。派生点都在明文已在手处（`Add`/`Adopt`/`ReplaceSecret`/`Acquire`/`adoptLegacyLocked`），因此不增加 Keychain 往返；`backfillMasks` 在 `NewPool` 时补写存量行，每个凭据至多一次。**除此之外任何 secret 片段都不得落库**——尾 4 位这类"提示"曾被明确否决，掩码是经用户确认后重开的口子，不要再扩大它。
- **凭据池是上游 Key 的唯一 owner：`backend/credential`。** 一个 provider 可有多条凭据，按 `credential_mode` 选择：`session`（默认，会话粘性，顺带命中上游按 Key 隔离的 prompt cache）、`round_robin`、`least_used`（按请求数/权重）、`random`。健康状态（冷却、永久失效）只在内存，重启即清空；失效状态落库，因为被拒的 Key 不会自愈，需手动重置。`proxy` 四处取 Key 点（`chatCompletions`、`messagesHandler` 的 `anthropicPassthrough` 与 `anthropicViaOpenAI`、`responses`）统一经 `withCredential` 闭包，因此 failover 不改变 `Adapter` 契约。
- **`/v1/responses` 是纯转换层，不做 Responses↔Anthropic 双向转换。** Codex CLI 只支持 Responses 线协议（`wire_api` 唯一取值），所以网关把它转成 Chat Completions 发上游、再把响应（含流式 SSE、工具调用、推理内容）转回 Responses 形状。映射到 `anthropic` 类 provider 时返回 501。三条来自 Codex 源码的硬约束：工具调用的参数只能靠 `response.output_item.done` 里一份完整 item 交付（Codex 忽略 `function_call_arguments.delta/done`）；正文/推理的 `delta` 必须归属一个已用 `output_item.added` 宣告过的 item，否则 Codex 报「OutputTextDelta without active item」并丢弃 delta，流式逐字显示失效；`response.completed` 的 `id` 与 `usage.{input_tokens,output_tokens,total_tokens}` 是非 Option 字段，缺一个整条事件解析失败。Codex 切片的 `custom`（freeform，如 `apply_patch`）工具包成单字符串入参的 function 双向转换，`web_search`/`tool_search`/`namespace` 等 hosted 工具丢弃并记进请求日志。
- **为可测试性保留的接口边界：** `secret.Store`（Keychain vs 内存）与 `proxy.Adapter`（`Compatible` OpenAI 线协议 vs `Anthropic`）。Adapter 实现 `Do(ctx, provider, secret, Request) (*http.Response, error)`。
- **Codex 的模型切换靠 profile 文件，不靠映射表。** 网关的模型映射与 Codex 配置无关：Codex 只把模型名当字符串放进请求体，网关按自己的映射表解析，所以 `gpt-5.6-sol` 之类不需要写进任何 Codex 配置。模型多起来后，切换用 `<CODEX_HOME>/<名>.config.toml` + `codex --profile <名>`（实测 CLI 0.154.0：profile 只写 `model` 与 `model_provider` 两行即可，provider 表从主 `config.toml` 合并，不必重复）。三条实测约束：`--profile` 的名字只接受 `[A-Za-z0-9_-]`（带点如 `gpt-5.6-sol` 直接被拒），所以模型名里的 `.` 与 `/` 必须消毒成 `-`（`codexProfileName`），且消毒有损、名字必须在**全量**可路由列表上去重（`codexProfilePlan`），否则同一模型改勾选后会换名字、在磁盘留下重复文件；生成的 profile 只增不删——目录里还有其它工具留下的成百个文件，按「不在本次选中集合里」去删会误伤它们，认领磁盘文件要靠内容（`model_provider` 指向本网关）而非文件名。
- **同一个 `multiProvider` 勾选列表在两类工具上语义不同，默认值必须由后端分派。** ai-sdk/pi 类是「配置里列出哪些候选模型」，默认全选才有意义；codex 类是「生成哪些 profile 文件」，默认全选会在用户第一次打开面板时写出几十份文件。因此 `Generator.SelectedModels` 按 shape 分派：codex 的默认取磁盘现状（`codexProfileModels`），一份都没有时才退到主配置的默认模型，用户显式勾过（`selected` 非 nil）则完全按勾选、包括清空。这与 `SlotBaseline` 同一原则——重开面板必须显示上次写入的结果，否则每次写入都像丢了选择。前端不得自己假定「全选」，要从 `Preview.SelectedModels` 起步。
- **SQLite 内存镜像。** `provider.Registry`、`config.MappingStore`、`apikey.Store`、`credential.Pool` 等用 `sync.RWMutex`/`sync.Mutex` 保护的 map 与 SQLite 同步；`agent.Store` 纯内存（无 DB）。
- **schema 唯一来源是 `backend/storage/sqlite.go`**（内联 DDL，无迁移框架）；`usage/tracker.go` 是废弃样例代码，勿用。

## 关键目录

| 路径 | 用途 |
|---|---|
| `main.go` / `app.go` | Wails 引擎 / `App` 门面（全部 Wails 绑定 + 组合根） |
| `backend/provider/` | Provider 类型、`Registry`、内嵌 `catalog.json` 内置目录 |
| `backend/config/` | `ModelMapping` + `MappingStore`（模型 → 提供商解析） |
| `backend/apikey/` | 网关自身 key 的 `Store`（客户端 `ar-` 前缀密钥认证） |
| `backend/envcfg/` | 把网关 key 写入用户 shell 环境（`AGENT_ROUTER_API_KEY`），跨平台 |
| `backend/templates/` | 为 CLI 工具（opencode/mimocode/pi/claude/omp/codex）生成网关接入配置；内嵌 `catalog.json` 声明式目录（`configRel` 配置路径、`skillsRel` 该 CLI 的 skills 目录）；读取用户现有配置文件并合并（保留原字段顺序），JSON/YAML/TOML 三种 shape。Codex 额外为每个选中模型写一份 `<名>.config.toml` profile（`codex.go`），用 `codex --profile <名>` 切换 |
| `backend/skills/` | 扫描/管理 skills 目录（纯文件系统，无 SQLite 镜像）；`link.go` 把 app 管理的 skill 以 symlink 发布进某个 CLI 的 skills 目录（见 `skillsRel`） |
| `backend/storage/` | SQLite `Open()`，schema 唯一来源（唯一调用 mattn/go-sqlite3 之处） |
| `backend/credential/` | 上游凭据池：多 Key 选择策略（`session`/`round_robin`/`least_used`/`random`）、运行期健康状态（冷却/永久失效）、遗留单 Key 认领。`pool.go` 管存储与状态，`selector.go` 管策略与会话指纹 |
| `backend/proxy/` | HTTP `Server`、`Adapter`、OpenAI 线协议类型；`credential_exec.go` 是取 Key + failover 的执行器 |
| `backend/secret/` | `secret.Store` 接口 + macOS Keychain 实现 |
| `backend/usage/` | `SQLiteTracker`（在用）；`tracker.go` 废弃勿用 |
| `backend/agent/` | `agent.Preset` + 内存 `Store` |
| `frontend/src/` | React SPA 源码 |
| `frontend/wailsjs/` | Wails 生成绑定（重新生成，勿手改） |

## 代码约定

**Go**

- 构造函数注入 —— 每个包暴露 `New(...)`/`NewXStore(...)`；无全局状态；`app.go` 的 `NewApp()` 是唯一组合根。
- 错误包装 `fmt.Errorf("context: %w", err)`；启动期致命错误在 `NewApp`/`Startup` 中 `panic(fmt.Errorf(...))`（桌面应用惯例）。
- `context.Context` 从 Wails `Startup` 流入 `App.ctx`，贯穿 HTTP handler 与 adapter。

**TypeScript/React**

- **无 router、无 store、无 context。** `App.tsx` 持有全部状态（`useState`/`useEffect`）；导航是 `Page` 联合类型切换。
- 后端调用只经 `frontend/src/lib/api.ts`，直接访问 `(window as any).go?.main?.App` 并带浏览器端 demo 回退——**不**导入生成的 `wailsjs` 模块。
- `frontend/src/lib/types.ts` 手工镜像 Go 结构体（`Provider`、`ModelMapping`、`AgentPreset`、`Usage`、`Bootstrap`），需手动与 Go 侧保持同步。
- 样式：HeroUI + Tailwind v3，但外观集中在手写 `frontend/src/styles.css`（语义化 kebab-case 类名，对 HeroUI 用 `!important` 覆盖）；新 UI 沿用该文件的语义类名模式。

## 测试与 QA

- Go 测试与包同目录（`*_test.go`），标准 `go test`；改后端后运行 `go test ./...`。
- 无 Jest/Vitest；类型检查由 `npm run build` 中 strict 模式的 `tsc -b` 强制。
- 前端改动后跑 `npm run formatter`。

## 重要文件

- `main.go` —— Wails 引导；`go:embed all:frontend/dist`；绑定 `*App`；1440×900
- `app.go` —— `App` 门面、`NewApp()` 组合、`Bootstrap`、全部 Wails 绑定方法（`GetBootstrap`、`SaveProvider`、`ToggleProvider`、`SetProviderAPIKey`、凭据池绑定 `ListProviderCredentials`/`AddProviderCredential`/`UpdateProviderCredential`/`DeleteProviderCredential`/`ToggleProviderCredential`/`ResetProviderCredentialStatus`/`SetProviderCredentialMode`、`SaveModelMapping`、`SaveAgentPreset`、`ResolveModel`、`ListToolTemplates`、`RenderToolTemplate`、`WriteToolTemplate`、`ExportLocalAPIKeyEnv`、`GatewayHost`、`ListSkills`/`GetSkill`/`ToggleSkill`/`DeleteSkill`/`SaveSkillBody`、`ListSkillLinks`/`SetSkillLink`）
- `backend/credential/pool.go` —— 凭据池存储与健康状态；`selector.go` —— 选择策略与会话指纹
- `backend/proxy/server.go` —— `GET /health`、`GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/messages`、`POST /v1/responses`；监听 `127.0.0.1:9400`
- `backend/proxy/responses.go` —— `/v1/responses` handler 与 Responses→Chat Completions 请求转换（`toChat`）；`responses_stream.go` —— 响应转换（`pipeChatStreamToResponses` 合成 SSE、`chatResponseToResponses` 非流式）
- `backend/proxy/credential_exec.go` —— 取 Key + failover 执行器（`withCredential`）
- `backend/proxy/adapter.go` —— `Adapter` 接口、`Compatible`、`Anthropic`
- `backend/storage/sqlite.go` —— schema 唯一来源
- `backend/templates/tool.go` —— `Tool` 目录加载/解析（`Resolve`、`Tools`）
- `backend/templates/codex.go` —— Codex 的 `config.toml` 渲染（`renderCodexTOML`）与 profile 生成（`codexProfiles`/`WriteProfiles`）；TOML 解析器只用于定位行范围，写回按行 splice，保住用户的注释与嵌套表
- `wails.json` —— Wails 构建/开发接线

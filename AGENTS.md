## 项目概览

**Agent Router** —— Wails v2 桌面应用，本地模型路由网关。React 界面配置 LLM 提供商（OpenAI、Anthropic、Gemini）、模型映射、本地 API key；本地 HTTP 服务（`127.0.0.1:9400`）暴露 OpenAI 兼容的 `/v1/chat/completions`，按映射转发到上游提供商。

- Go 后端在仓库根目录与 `backend/`（模块 `agent-router`，go 1.25.0，sqlite3 需 cgo）
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
secret（Keychain）+ agent（内存）+ envcfg + templates  ← 不依赖业务 DB
proxy（后端顶层）  ← 依赖 provider、config、secret、usage
app.go（package main）  ← 组合根，组装所有组件
main.go  ← Wails 引导，绑定 *App；tray_darwin.go / tray_windows.go / tray_other.go 提供各平台托盘（窗口关闭=隐藏到托盘）
```

数据流：

```
React UI → App 方法（Wails 绑定）
  → SQLite 存储（providers/mappings/apikeys/usage）  |  Keychain（上游 API 密钥）
  → proxy.Server（127.0.0.1:9400）→ Adapter → 上游 LLM
  → usage.SQLiteTracker → SQLite
```

**必须保留的架构决策：**

- **上游凭据绝不持久化到 SQLite。** Provider 行只存不透明 `APIKeyRef`；真实 token 仅存 macOS Keychain（`security` 命令，服务名 `com.agentrouter.credentials`）。参见 `app.go` 的 `SetProviderAPIKey` 与 `backend/secret/keychain.go`。本网关自身的 key（`backend/apikey/`）存 SQLite，仅写入用户新终端环境的 `AGENT_ROUTER_API_KEY`（`backend/envcfg/`）供 CLI 客户端认证。
- **为可测试性保留的接口边界：** `secret.Store`（Keychain vs 内存）与 `proxy.Adapter`（`Compatible` OpenAI 线协议 vs `Anthropic`）。Adapter 实现 `Do(ctx, provider, secret, Request) (*http.Response, error)`。
- **SQLite 内存镜像。** `provider.Registry`、`config.MappingStore`、`apikey.Store` 等用 `sync.RWMutex` 保护的 map 与 SQLite 同步；`agent.Store` 纯内存（无 DB）。
- **schema 唯一来源是 `backend/storage/sqlite.go`**（内联 DDL，无迁移框架）；`usage/tracker.go` 是废弃样例代码，勿用。

## 关键目录

| 路径 | 用途 |
|---|---|
| `main.go` / `app.go` | Wails 引擎 / `App` 门面（全部 Wails 绑定 + 组合根） |
| `backend/provider/` | Provider 类型、`Registry`、内嵌 `catalog.json` 内置目录 |
| `backend/config/` | `ModelMapping` + `MappingStore`（模型 → 提供商解析） |
| `backend/apikey/` | 网关自身 key 的 `Store`（客户端 `ar-` 前缀密钥认证） |
| `backend/envcfg/` | 把网关 key 写入用户 shell 环境（`AGENT_ROUTER_API_KEY`），跨平台 |
| `backend/templates/` | 为 CLI 工具（opencode/mimocode/pi/claude/omp）生成网关接入配置；内嵌 `catalog.json` 声明式目录；读取用户现有配置文件并合并（保留原字段顺序），YAML/JSON 两种 shape |
| `backend/storage/` | SQLite `Open()`，schema 唯一来源（唯一调用 mattn/go-sqlite3 之处） |
| `backend/proxy/` | HTTP `Server`、`Adapter`、OpenAI 线协议类型 |
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
- `app.go` —— `App` 门面、`NewApp()` 组合、`Bootstrap`、全部 Wails 绑定方法（`GetBootstrap`、`SaveProvider`、`ToggleProvider`、`SetProviderAPIKey`、`SaveModelMapping`、`SaveAgentPreset`、`ResolveModel`、`ListToolTemplates`、`RenderToolTemplate`、`WriteToolTemplate`、`ExportLocalAPIKeyEnv`、`GatewayHost`）
- `backend/proxy/server.go` —— `GET /health`、`POST /v1/chat/completions`；监听 `127.0.0.1:9400`
- `backend/proxy/adapter.go` —— `Adapter` 接口、`Compatible`、`Anthropic`
- `backend/storage/sqlite.go` —— schema 唯一来源
- `backend/templates/tool.go` —— `Tool` 目录加载/解析（`Resolve`、`Tools`）
- `wails.json` —— Wails 构建/开发接线

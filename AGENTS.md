# 仓库指南

## 项目概览

**Agent Router** —— 一个 Wails v2 桌面应用，充当本地模型路由代理。用户在 React 界面中配置 LLM 提供商（OpenAI、Anthropic、Google Gemini）、模型映射和 agent 预设；本地 HTTP 服务暴露 OpenAI 兼容的 `/v1/chat/completions` 端点，并将请求转发到所选的上游提供商。

- Go 后端位于仓库根目录及 `backend/`（模块 `agent-router`，go 1.25.0）
- React 18 + TypeScript + Vite 前端位于 `frontend/`
- Wails v2.14.0 将二者粘合：`main.go` 嵌入 `frontend/dist` 并将 `*App` 绑定到 UI

## 架构与数据流

分层 Go 后端，单向依赖流（叶子 → 门面）：

```
storage（SQLite 叶子层）  ← 以 sql.DB 注入给消费者
provider / config / usage  ← 通过构造函数消费 *sql.DB
secret（Keychain）+ agent（内存）  ← 不依赖 DB
proxy（后端顶层）  ← 依赖 provider、config、secret、usage
app.go（package main）  ← 组装根（composition root），串联所有组件
main.go  ← Wails 引导，绑定 *App
```

数据流：

```
React UI → App 方法（Wails 绑定）
  → SQLite 存储（providers/mappings/usage）  |  Keychain（API 密钥）
  → proxy.Server（127.0.0.1:9400）→ Adapter → 上游 LLM
  → usage.SQLiteTracker → SQLite
```

**必须保留的架构决策：**

- **凭据绝不持久化到 SQLite。** Provider 行只存储不透明的 `APIKeyRef` 字符串；真实 token 仅存于 macOS Keychain（`security` 命令，服务名 `com.agentrouter.credentials`）。参见 `app.go` 的 `SetProviderAPIKey` 和 `backend/secret/keychain.go`。
- **为可测试性保留的接口边界：** `secret.Store`（Keychain vs 内存）与 `proxy.Adapter`（OpenAI 兼容 vs Anthropic）。Adapter 实现 `Do(ctx, provider, secret, Request) (*http.Response, error)`。
- **SQLite 的内存镜像。** `provider.Registry`、`config.MappingStore` 维护由 `sync.RWMutex` 保护、与 SQLite 同步的 map；`agent.Store` 纯内存（无 DB）。

## 关键目录

| 路径 | 用途 |
|---|---|
| `main.go` / `app.go` | Wails 入口 / `App` 门面（绑定 + 组装根） |
| `backend/provider/` | Provider 类型、`Registry`、内嵌 `catalog.json` 内置目录 |
| `backend/config/` | `ModelMapping` + `MappingStore`（模型 → 提供商解析） |
| `backend/storage/` | SQLite `Open()`、内联 DDL 建表（唯一调用 mattn/go-sqlite3 之处） |
| `backend/proxy/` | HTTP `Server`、`Adapter`（Compatible/Anthropic）、OpenAI 线协议类型、测试 |
| `backend/secret/` | `secret.Store` 接口 + macOS Keychain 实现 |
| `backend/usage/` | `SQLiteTracker`（在用）+ `tracker.go`（废弃的内存样例代码，未使用） |
| `backend/agent/` | `agent.Preset` + 内存 `Store` |
| `frontend/src/` | React SPA 源码 |
| `frontend/wailsjs/` | Wails 生成的绑定（重新生成，勿手改） |

## 开发命令

包管理器：**npm**（无 bun，无 `packageManager` 字段）。Go 需要 **cgo**（mattn/go-sqlite3）。

```bash
# 安装依赖（wails.json 中同样如此配置）
npm install               # 在 frontend/ 下执行

# 开发（热重载 Go + Vite 开发服务器）
wails dev

# 构建（tsc -b + vite build → go build，嵌入 frontend/dist）
wails build
go build ./...            # 纯 Go 构建；需先存在 frontend/dist

# 仅前端
cd frontend && npm run dev        # vite
cd frontend && npm run build      # tsc -b && vite build
cd frontend && npm run formatter
```

仓库中**没有** lint 配置、没有格式化配置（默认 gofmt）、没有 CI、没有 Makefile、没有 README。

## 代码约定与常见模式

**Go**

- 构造函数注入 —— 每个包暴露 `New(...)`/`NewXStore(...)`；永不使用全局状态。`app.go` 的 `NewApp()` 是唯一组装根。
- 错误包装：`fmt.Errorf("context: %w", err)`；启动期致命错误在 `NewApp`/`Startup` 中用 `panic(fmt.Errorf(...))`（此处为桌面应用惯例）。
- Store 结构体用 `sync.RWMutex` 保护其内存 map，与 SQLite 镜像。
- `context.Context` 从 Wails `Startup` 流入 `App.ctx`，并贯穿 HTTP 处理函数与 adapter。

**TypeScript/React**

- **无 router、无 store、无 context。** `App.tsx` 持有全部状态（`useState`/`useEffect`）；导航是 `Page` 联合类型（`'overview' | 'providers' | 'mappings' | 'agents'`），用三元表达式切换。
- 后端调用只经 `frontend/src/lib/api.ts`，它直接访问 `(window as any).go?.main?.App`，并带浏览器端 demo 回退 —— 它**不**导入生成的 `wailsjs` 模块。
- `frontend/src/lib/types.ts` 手工镜像 Go 结构体（`Provider`、`ModelMapping`、`AgentPreset`、`Usage`、`Bootstrap`）—— 需手动保持同步；生成的 `wailsjs/go/models.ts` 未被 `src` 使用。
- 样式：HeroUI（`@heroui/react`）+ Tailwind v3，但大部分外观集中在手写的 `frontend/src/styles.css`（语义化 kebab-case 类名、对 HeroUI 用 `!important` 覆盖）。新 UI 应沿用该文件中的语义类名模式。

## 重要文件

- `main.go` —— Wails 引导；`go:embed all:frontend/dist`；绑定 `*App`；1440×900
- `app.go` —— `App` 门面、`NewApp()` 组装、`Bootstrap`、所有 Wails 绑定方法（`GetBootstrap`、`SaveProvider`、`ToggleProvider`、`SetProviderAPIKey`、`SaveModelMapping`、`SaveAgentPreset`、`ResolveModel`）
- `backend/proxy/server.go` —— 端点：`GET /health`、`POST /v1/chat/completions`；监听 `127.0.0.1:9400`
- `backend/proxy/adapter.go` —— `Adapter` 接口、`Compatible`（OpenAI 线协议）、`Anthropic`（与 OpenAI 结构互转）
- `backend/storage/sqlite.go` —— schema 唯一来源（内联 `CREATE TABLE IF NOT EXISTS` DDL；一次手工 `ALTER TABLE` icon 迁移；无 `.sql` 文件、无迁移框架）
- `backend/provider/catalog.json` —— 内嵌内置提供商目录（openai、anthropic、google-gemini）
- `wails.json` —— Wails 构建/开发接线（`npm install` / `npm run build` / `npm run dev`）
- `frontend/package.json` —— 构建链 `tsc -b && vite build`

## 运行时 / 工具链偏好

- **运行时：** Go 1.25.0（sqlite3 需 cgo）、Node（npm）、Wails v2.14.0 CLI
- **前端：** Vite 8、TypeScript 5.6（`strict: true`，无路径别名）、Tailwind 3、HeroUI、React 18.3
- **仅 macOS 凭据：** 经 `security` 命令使用 Keychain；非 darwin 构建回退到 `backend/secret/keychain.go` 中的内存存储

## 测试与 QA

- 前端代码需要使用 npm run formatter 格式化
- **无 Go 后端测试** 无覆盖率配置、无 CI、无 lint 门禁。
- **无 TS/Jest/Vitest 测试。** 无覆盖率配置、无 CI、无 lint 门禁。
- 类型检查由 `npm run build` 中的 `tsc -b`（strict 模式）强制执行。

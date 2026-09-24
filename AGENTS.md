## 项目概览

**Agent Router** —— Wails v2 桌面应用，本地模型路由网关。React 界面配置 LLM 提供商（OpenAI、Anthropic、Gemini）、模型映射、本地 API key；本地 HTTP 服务（`127.0.0.1:9400`）暴露 OpenAI 兼容的 `/v1/chat/completions`、Anthropic 的 `/v1/messages`，以及 `/v1/responses`（Codex CLI 的线协议，内部转成 Chat Completions 转发），按映射转发到上游提供商。

- Go 后端在仓库根目录与 `backend/`（模块 `agent-router`，go 1.25.0，SQLite 用纯 Go 驱动 `modernc.org/sqlite`，无需 cgo）；Codex 的 `config.toml` 模板用 `github.com/pelletier/go-toml/v2` 的 `unstable` 包定位行号，写回仍是行级 splice
- React 18 + TypeScript + Vite 前端在 `frontend/`
- Wails v2.14.0 粘合二者：`main.go` 嵌入 `frontend/dist`，绑定 `*App`

`AGENTS.md` 有更完整的中文版架构说明，两者需同步维护。

## 开发命令

包管理器：**npm**。需要 Wails CLI。

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
secret（OS 密钥库）+ agent（内存）+ envcfg + templates  ← 不依赖业务 DB
proxy（后端顶层）  ← 依赖 provider、config、credential、usage
app.go（package main）  ← 组合根，组装所有组件
main.go  ← Wails 引导，绑定 *App；tray_darwin.go / tray_windows.go / tray_other.go 提供各平台托盘（窗口关闭=隐藏到托盘）
```

数据流：

```
React UI → App 方法（Wails 绑定）
  → SQLite 存储（providers/mappings/apikeys/usage/provider_credentials）  |  OS 密钥库（上游 API 密钥）
  → proxy.Server（127.0.0.1:9400）→ credential.Pool 选 Key → Adapter → 上游 LLM
  → usage.SQLiteTracker → SQLite（含 credential_id/credential_name/credential_mask）
```

**必须保留的架构决策：**

- **上游凭据绝不持久化到 SQLite。** 真实 token 只进 OS 密钥库：macOS 用 Keychain（`security`，服务名 `com.agentrouter.credentials`），Windows 用 Credential Manager（`advapi32` 的 `CredWriteW`/`CredReadW`/`CredDeleteW`，target 前缀同名），Linux 等无原生实现的平台退回进程内存（重启即失，与旧 Windows 行为相同）。`provider_credentials` 行只存不透明 `secret_ref`。参见 `app.go` 的凭据绑定与 `backend/secret/`（`keychain.go` 接口 + 按 `GOOS` 分文件实现）。`providers.api_key_ref` 收窄为遗留字段：仅作为升级前单 Key 的载体，由 `credential.Pool` 在首次使用时认领为第一条凭据，新逻辑不再读它选 Key。本网关自身的 key（`backend/apikey/`）存 SQLite，仅写入用户新终端环境的 `AGENT_ROUTER_API_KEY`（`backend/envcfg/`）供 CLI 客户端认证。
- **掩码是唯一的例外，且只允许派生形式。** `credential.MaskSecret` 产出 `ss****sfg`（首 2 尾 3），落库在 `provider_credentials.mask` 与 `request_logs.credential_mask`，供 UI 与日志辨识是哪个 Key，无需回读 Keychain，且凭据删除后历史日志仍可读。它刻意短到不可用。派生点都在明文已在手处（`Add`/`Adopt`/`ReplaceSecret`/`Acquire`/`adoptLegacyLocked`），因此不增加 Keychain 往返；`backfillMasks` 在 `NewPool` 时补写存量行，每个凭据至多一次。**除此之外任何 secret 片段都不得落库**——尾 4 位这类"提示"曾被明确否决，掩码是经用户确认后重开的口子，不要再扩大它。
- **凭据池是上游 Key 的唯一 owner：`backend/credential`。** 一个 provider 可有多条凭据，按 `credential_mode` 选择：`session`（默认，会话粘性，顺带命中上游按 Key 隔离的 prompt cache）、`round_robin`、`least_used`（按请求数/权重）、`random`。健康状态（冷却）只在内存，重启即清空；失效状态落库——上游 401/403 拒绝的 Key 不会自愈，需手动 Reset；secret 读不到导致的失效则在下次 `NewPool` 时自愈（见下条）。`proxy` 四处取 Key 点（`chatCompletions`、`messagesHandler` 的 `anthropicPassthrough` 与 `anthropicViaOpenAI`、`responses`）统一经 `withCredential` 闭包，因此 failover 不改变 `Adapter` 契约。
- **`/v1/responses` 是纯转换层，不做 Responses↔Anthropic 双向转换。** Codex CLI 只支持 Responses 线协议（`wire_api` 唯一取值），所以网关把它转成 Chat Completions 发上游、再把响应（含流式 SSE、工具调用、推理内容）转回 Responses 形状。映射到 `anthropic` 类 provider 时返回 501。三条来自 Codex 源码的硬约束：工具调用的参数只能靠 `response.output_item.done` 里一份完整 item 交付（Codex 忽略 `function_call_arguments.delta/done`）；正文/推理的 `delta` 必须归属一个已用 `output_item.added` 宣告过的 item，否则 Codex 报「OutputTextDelta without active item」并丢弃 delta，流式逐字显示失效；`response.completed` 的 `id` 与 `usage.{input_tokens,output_tokens,total_tokens}` 是非 Option 字段，缺一个整条事件解析失败。Codex 切片的 `custom`（freeform，如 `apply_patch`）工具包成单字符串入参的 function 双向转换，只有 `web_search`/`tool_search` 这类 hosted 工具丢弃并记进请求日志。
- **`namespace` 工具必须展开，不能丢弃；Responses Lite 的工具表在 `input` 里。** Codex 的 `ToolName` 是 `(namespace, name)` 二元组而非 `"ns.tool"` 字符串（`codex-rs/protocol/src/tool_name.rs`），Chat 上游又只收一个按 `^[a-zA-Z0-9_-]{1,64}$` 校验的扁平函数名，所以网关用 `toolPlan` 做双向改名：`functions` 里的成员保持裸名，其余前置 `namespace__`，撞名追加 `_2`/`_3`；响应侧查回 `(namespace, name)`，非默认 namespace 才带 `namespace` 字段（省略即等于 `functions`），custom 成员照旧回 `custom_tool_call` 的原始字符串 `input`；input 历史里的 `function_call`/`custom_tool_call` 也按同一张表换回上游函数名。`use_responses_lite: true` 的模型（`gpt-6-astra`、`gpt-5.6-*`）不下发顶层 `tools`/`instructions`，全部工具声明裹在 `input[0]` 的 `{"type":"additional_tools","role":"developer","tools":[...]}` 里——它必须当工具表解析、且不能当消息转发，漏掉就等于整份工具表蒸发，模型只能把调用当正文吐出来（`<tool_call><function=functions.exec>`，实测 qwen/deepseek/glm 都这样）。`text.format` 的 json_schema 也要转成 Chat 的嵌套 `{"json_schema":{name,schema,strict}}`，否则上游按未知类型拒掉。
- **为可测试性保留的接口边界：** `secret.Store`（Keychain / Credential Manager vs 内存）与 `proxy.Adapter`（`Compatible` OpenAI 线协议 vs `Anthropic`）。Adapter 实现 `Do(ctx, provider, secret, Request) (*http.Response, error)`。
- **secret 读不到导致的 invalid 会在下次启动自愈；上游 401/403 导致的 invalid 不会。** `Acquire` 读不到 OS 密钥库时写 `last_error = "secret not found in keychain"` 并标 `invalid`；`NewPool.healMissingSecrets` 只对这一种 `last_error` 且 secret 已可再读的行清回 `active`。上游拒绝仍写自己的 `last_error`（如 `upstream rejected the key with status 401`），保持需手动 Reset。没有这层自愈时，Windows 在接入 Credential Manager 之前重启会把整池打成 invalid，代理一直报 `no API key configured for …`，即使密钥已重新可读。
- **Codex 的模型切换靠 profile 文件，不靠映射表。** 网关的模型映射与 Codex 配置无关：Codex 只把模型名当字符串放进请求体，网关按自己的映射表解析，所以 `gpt-5.6-sol` 之类不需要写进任何 Codex 配置。模型多起来后，切换用 `<CODEX_HOME>/<名>.config.toml` + `codex --profile <名>`（实测 CLI 0.154.0：profile 只写 `model` 与 `model_provider` 两行即可，provider 表从主 `config.toml` 合并，不必重复）。三条实测约束：`--profile` 的名字只接受 `[A-Za-z0-9_-]`（带点如 `gpt-5.6-sol` 直接被拒），所以模型名里的 `.` 与 `/` 必须消毒成 `-`（`codexProfileName`），且消毒有损、名字必须在**全量**可路由列表上去重（`codexProfilePlan`），否则同一模型改勾选后会换名字、在磁盘留下重复文件；生成的 profile 只增不删——目录里还有其它工具留下的成百个文件，按「不在本次选中集合里」去删会误伤它们，认领磁盘文件要靠内容（`model_provider` 指向本网关）而非文件名。
- **同一个 `multiProvider` 勾选列表在两类工具上语义不同，默认值必须由后端分派。** ai-sdk/pi/omp 类是「配置里列出哪些候选模型」：默认全选只在配置里还没有本网关条目（首次使用）时成立，之后一律从磁盘回读已写入的子集（`Generator.WithBaseline` 读 `provider(s).agent-router.models`，显式清空的空列表也原样保留），否则重开面板退回全选、下一次写入还会把收窄过的清单改回全量；OMP 已存在文件走字节拼接的 `gatewayBody` 也必须用 `g.models()` 而非 `g.routable`，否则拼接路径会静默扩成全量。codex 类是「生成哪些 profile 文件」，默认全选会在用户第一次打开面板时写出几十份文件，因此默认取磁盘现状（`codexProfileModels`），一份都没有时才退到主配置的默认模型，用户显式勾过（`selected` 非 nil）则完全按勾选、包括清空。这与 `SlotBaseline` 同一原则——重开面板必须显示上次写入的结果，否则每次写入都像丢了选择。前端不得自己假定「全选」，要从 `Preview.SelectedModels` 起步。
- **同一个客户端模型名可以挂多家提供商，构成一条 failover 链。** `model_mappings.client_model` **没有** UNIQUE 约束（旧库在 `storage.OpenPath` 里整表重建去掉，幂等且保留原行与别名），`MappingStore.ResolveAll` 按配置顺序返回整条链，`proxy.resolveRoutes` 把它配上各自的 provider，`Server.withRoute` 依次尝试：一家返回 401/403/429/5xx 就转下一家，健康的那家立即短路（不会重复计费），非可重试的 4xx 描述的是请求本身所以不换家。每家先在链内跑自己那套凭据 failover（`withCredential`，最多 2 把 Key），因此一条 N 家的链最多打 2N 次上游。日志与用量记的是**最终应答的那条路由**（provider、upstream_model、credential），不是链首——同名链上「谁服务了这次请求」必须看得出来。链顺序由 `effectiveMappings` 的稳定排序（`ClientModel` 再 `ID`）保证可复现，UI 卡片上的「同名 1/2」就是这个顺位。`/v1/models`、`EffectiveMappings` 与生成的 CLI 配置按名字**去重**，只暴露一个模型名：几家提供商做后备是内部细节。`Resolve` 只返回链首，留给「一个名字一条映射」的旧调用方（UI 的 `ResolveModel`）。`/v1/messages` 的链按链首的 `Kind` 过滤（Anthropic 透传与 OpenAI 转换两条路径发的是不同线格式），`/v1/responses` 则剔掉链里的 Anthropic 路由，全链都是 Anthropic 才报 501。
- **已保存的映射行也必须落在提供商当前勾选的模型里。** `effectiveMappings` 过滤已保存行时要求 `slices.Contains(p.Models, mapping.UpstreamModel)`：上游模型被取消勾选后，这条路由要从 `/v1/models`、Agent 配置清单（`Routable()`）与路由里**一并消失**，否则会出现映射页看不见（那页按 `p.models` 分组）、Agent 配置里却还能选中写入的分裂。自动映射本就生成自 `p.models`，不受影响；回归测试 `TestEffectiveMappingsHonorsProviderModelSelection`，前端 `ensuredMappings` 镜像同一条规则（Playground / 日志筛选）。
- **链级策略只有起点两种：failover（默认）与 round_robin（轮转分摊）。** 默认 failover 下链首健康就永远只打链首——同名多家不会自动负载均衡，这是刻意的：请求体可能已在上游落账，跨家重试要避免重复计费。要让 N 家真正分摊流量，把链策略设为 `round_robin`：`proxy.resolveRoutes` 每次把起点向后挪一位（`Server.chainCursor`，按**声明的** `client_model` 计数，所以所有别名共享同一轮换而不是各自漂移），起点之后的顺序不变，因此某一家 429/5xx 时仍沿 failover 往下走。轮转只改起点、不改链成员，也不保证严格均等（依赖按名声明的计数器，多进程/重启后从头开始）。策略存 `model_chain_modes(client_model, mode)` 而非映射行上：它描述整条链，不是某一家。UI 只在链首卡片显示下拉框，非链首只显示顺位。
- **别名的可见性边界：命中路由，但不出现在任何模型列表。** 一条映射可以有任意多个别名（`model_mappings.aliases_json`），`clientModel` 与其别名在 `ResolveAll` 与 `proxy.resolveRoutes` 里完全等价，`client_model` 始终是 `request_logs.client_model` 的取值（用量按映射聚合，不随调用方用的是哪个名字漂移）。反过来，`/v1/models`、`templates.Generator` 的 `Routable()` 与 Codex profile 计划都**只**遍历 `ClientModel`，别名一个都不列——这是刻意的：别名是给「已经知道这个名字」的调用方图省事的，不是网关对外的第二个模型。别名是自由字符串，不是「该提供商已启用的模型」的子集。同名跨提供商是刻意支持的，但**完全相同的路由**（同名字 + 同 provider + 同 upstream_model，含「别名撞上别人这条路由」）仍被 `checkDuplicateRouteLocked` 拒绝：它只会让 failover 把同一个请求发两遍。名字在 `Save` 里统一 `TrimSpace` 后入库，比对用原值，因此别名内的空白不会被当成不同名字。
- **使用情况页的三条聚合必须走覆盖索引，且按「维度 × 路由」出粒度再算费。** `request_logs` 实测单库 2.9GB（3930 行 × 平均 517KB 请求体 / 57KB 响应体），body 溢出页主导这张表。费用按映射单价在读取时计算（`backend/usage/cost.go`：输入 `(input-cached)*inputPrice/1e6`、输出 `output*outputPrice/1e6`、缓存 `cached*cacheReadPrice/1e6`，三者之和为总价），同名模型在 failover 链上单价可以不同，所以 SQL 必须带出 `provider_id`/`client_model` 再在 Go 里按 `MappingStore.PriceFor` 计价上卷——先上卷 token 再乘单价会把整条链按错价计。索引因此加宽为：`idx_usage_events_route(provider_id, model, …)`、`idx_request_logs_token(token_id, token_name, provider_id, client_model, …)`、`idx_request_logs_credential(credential_id, credential_name, credential_mask, provider_id, client_model, …)`；`CREATE INDEX IF NOT EXISTS` 对已存在的窄索引无效，迁移里先 `DROP INDEX IF EXISTS` 再重建。两条硬约束：一是 credential 查询的 WHERE 必须写成可索引形式（`credential_id <> '' OR credential_name <> ''`），`COALESCE(...) <> ''` 会让 SQLite 放弃覆盖索引、裸扫全表（实测同一查询 0.4s vs 2ms）；二是这些索引只能建在补列迁移**之后**——老库重建前没有 `credential_id`，写进内联 DDL 会在建表后立刻执行而报 `no such column`。防止退化的回归测试是 `TestUsageBreakdownAvoidsFullTableScan`（断言三条查询计划都含 `COVERING INDEX`），计费口径由 `TestUsageBreakdownPricedPerRoute` 锁定。
- **请求日志页的过滤组合会退化成裸扫，因此前端 loading 是必需而非装饰。** 无过滤与单条件查询都命中索引（`ORDER BY created_at DESC,id DESC LIMIT 15` 走 `COVERING INDEX idx_request_logs_created_at`；顶部统计与费用走 `GROUP BY provider_id,client_model`，命中 `COVERING INDEX idx_request_logs_token`，实测 0–30ms）；但 `success = ?` 叠加 `LIKE '%…%'` 文本过滤时执行计划是裸 `SCAN request_logs`（索引以 `token_id` 为前导列，`provider_name/client_model` 上不存在的 `success` 值配合无谓词约束的 LIKE 无法收敛），2.9GB 库上把 body 溢出页全读一遍。实测 0.35s（热）/ 8.5s（冷页缓存，注意冷值不可复现，热值是真实稳态）。因此 `RequestLogs` 的 loading 不是可选体验优化，而要有 **180ms 延迟门帘**（`LOG_SLOW_MS`）：正常查询全程无遮罩、不闪烁，退化查询与冷启动才点亮。列表查询与详情查询（`getRequestLog` 拉完整 body，单条可达数百 KB）各有一个 `useRef` 序号守卫，因为查询条件变化或连续点两行时旧请求可能后返回，必须只让最新一次落状态；详情弹窗关闭时同样作废在途请求，否则迟到响应会把已关的弹窗重新打开。**统计费用与 token 同源同一 WHERE**：`ListRequestLogs` 在一次 `GROUP BY provider_id,client_model` 里同时上卷 token 与按 `PriceFor` 计的四段费用（`inputCost`/`outputCost`/`cacheCost`/`totalCost`），统计卡跟随当前过滤条件；回归测试 `TestListRequestLogsCostsFollowFilter`、`TestListRequestLogsStatsAvoidsFullTableScan`。
- **SQLite 内存镜像。** `provider.Registry`、`config.MappingStore`、`apikey.Store`、`credential.Pool` 等用 `sync.RWMutex`/`sync.Mutex` 保护的 map 与 SQLite 同步；`agent.Store` 纯内存（无 DB）。
- **schema 唯一来源是 `backend/storage/sqlite.go`**（内联 DDL，无迁移框架）；`usage/tracker.go` 是废弃样例代码，勿用。

## 关键目录

| 路径 | 用途 |
|---|---|
| `main.go` / `app.go` | Wails 引擎 / `App` 门面（全部 Wails 绑定 + 组合根） |
| `backend/provider/` | Provider 类型、`Registry`、内嵌 `catalog.json` 内置目录；`modelsdev.go` 缓存 models.dev `api.json`（`ListDevCatalog`/`RefreshDevCatalog`，原文件落盘，解析时丢弃 `models`）与 `models.json`（`ListDevModels`/`RefreshDevModels`，映射抽屉的模型能力选择） |
| `backend/config/` | `ModelMapping` + `MappingStore`（模型 → 提供商解析；`ClientModel` 与 `Aliases` 等价命中，别名不对外列出。`ResolveAll` 返回同名多提供商的整条 failover 链，`Resolve` 只返回链首；`ChainModeFor`/`SetChainMode` 管链级起点策略；`PriceFor(provider, name)` 按路由取映射单价供用量页算费） |
| `backend/apikey/` | 网关自身 key 的 `Store`（客户端 `ar-` 前缀密钥认证） |
| `backend/envcfg/` | 把网关 key 写入用户 shell 环境（`AGENT_ROUTER_API_KEY`），跨平台 |
| `backend/templates/` | 为 CLI 工具（opencode/mimocode/pi/claude/omp/codex）生成网关接入配置；内嵌 `catalog.json` 声明式目录（`configRel` 配置路径、`skillsRel` 该 CLI 的 skills 目录）；读取用户现有配置文件并合并（保留原字段顺序），JSON/YAML/TOML 三种 shape。Codex 额外为每个选中模型写一份 `<名>.config.toml` profile（`codex.go`），用 `codex --profile <名>` 切换 |
| `backend/skills/` | 扫描/管理 skills 目录（纯文件系统，无 SQLite 镜像）；`link.go` 把 app 管理的 skill 以 symlink 发布进某个 CLI 的 skills 目录（见 `skillsRel`） |
| `backend/storage/` | SQLite `Open()`，schema 唯一来源（唯一调用 `modernc.org/sqlite` 之处） |
| `backend/credential/` | 上游凭据池：多 Key 选择策略（`session`/`round_robin`/`least_used`/`random`）、运行期健康状态（冷却/永久失效）、遗留单 Key 认领。`pool.go` 管存储与状态，`selector.go` 管策略与会话指纹 |
| `backend/proxy/` | HTTP `Server`、`Adapter`、OpenAI 线协议类型；`credential_exec.go` 是取 Key + failover 的执行器（`withCredential` 同一家换 Key，`withRoute` 跨提供商换家）；`resolveRoutes` 的同名链在轮转模式下换起点（逐请求轮转，无会话粘性） |
| `backend/secret/` | `secret.Store` 接口 + macOS Keychain / Windows Credential Manager 实现 |
| `backend/usage/` | `SQLiteTracker`（在用）：请求日志、按「维度 × 路由」的用量聚合与 `cost.go` 计费；`tracker.go` 废弃勿用 |
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
- **组件优先用 HeroUI（`@heroui/react`）**：HeroUI 已有的组件直接用，不要自己实现；没有现成组件时，优先考虑用 HeroUI 基础组件组合出新组件，而不是从零手写。

## 测试与 QA

- Go 测试与包同目录（`*_test.go`），标准 `go test`；改后端后运行 `go test ./...`。
- 无 Jest/Vitest；类型检查由 `npm run build` 中 strict 模式的 `tsc -b` 强制。
- 前端改动后跑 `npm run formatter`。

## 重要文件

- `main.go` —— Wails 引导；`go:embed all:frontend/dist`；绑定 `*App`；1440×900
- `app.go` —— `App` 门面、`NewApp()` 组合、`Bootstrap`、全部 Wails 绑定方法（`GetBootstrap`、`SaveProvider`、`ToggleProvider`、`SetProviderAPIKey`、凭据池绑定 `ListProviderCredentials`/`AddProviderCredential`/`UpdateProviderCredential`/`DeleteProviderCredential`/`ToggleProviderCredential`/`ResetProviderCredentialStatus`/`SetProviderCredentialMode`、`ListDevProviders`/`RefreshDevProviders`、`ListDevModels`/`RefreshDevModels`、`SaveModelMapping`、`SaveAgentPreset`、`ResolveModel`、`ListToolTemplates`、`RenderToolTemplate`、`WriteToolTemplate`、`ExportLocalAPIKeyEnv`、`GatewayHost`、`ListSkills`/`GetSkill`/`ToggleSkill`/`DeleteSkill`/`SaveSkillBody`、`ListSkillLinks`/`SetSkillLink`）
- `backend/credential/pool.go` —— 凭据池存储与健康状态；`selector.go` —— 选择策略与会话指纹
- `backend/proxy/server.go` —— `GET /health`、`GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/messages`、`POST /v1/responses`；监听 `127.0.0.1:9400`
- `backend/proxy/responses.go` —— `/v1/responses` handler 与 Responses→Chat Completions 请求转换（`toChat`、`toolPlan` 工具名双向映射、`responsesToolsToChat`/`flattenNamespace` 展开 namespace 与 `additional_tools`）；`responses_stream.go` —— 响应转换（`pipeChatStreamToResponses` 合成 SSE、`chatResponseToResponses` 非流式）
- `backend/proxy/credential_exec.go` —— 取 Key + failover 执行器（`withCredential`）
- `backend/proxy/adapter.go` —— `Adapter` 接口、`Compatible`、`Anthropic`
- `backend/storage/sqlite.go` —— schema 唯一来源
- `backend/templates/tool.go` —— `Tool` 目录加载/解析（`Resolve`、`Tools`）
- `backend/templates/codex.go` —— Codex 的 `config.toml` 渲染（`renderCodexTOML`）与 profile 生成（`codexProfiles`/`WriteProfiles`）；TOML 解析器只用于定位行范围，写回按行 splice，保住用户的注释与嵌套表
- `wails.json` —— Wails 构建/开发接线

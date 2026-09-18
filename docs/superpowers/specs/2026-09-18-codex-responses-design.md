# 设计：网关支持 OpenAI Responses 协议（Codex CLI 接入）

日期：2026-09-18
状态：待评审

## 目标

让本网关（`127.0.0.1:9400`）接受 `POST /v1/responses`——OpenAI 的 Responses API，
也是 Codex CLI 唯一支持的线协议——使 Codex CLI 能通过本网关路由到任意
OpenAI 兼容上游（Chat Completions 线协议）。

两件事一起做：

1. **协议转换层**：网关收 Responses 请求，转成 Chat Completions 调上游，
   再把上游的响应（含流式 SSE、工具调用、推理内容）转回 Responses 形状。
2. **接入模板**：UI 一键把 `[model_providers.agent-router]` 写进用户已有的
   `~/.codex/config.toml`，其余配置原样保留。

目标上游范围：Chat Completions 类上游（`openai` / `gemini` / `compatible`）。
映射到 `anthropic` 类 provider 时返回 501，不做 Responses↔Anthropic 转换。

## 背景：为什么必须做转换层

Codex CLI 的 provider 配置只有一种线协议可用。官方配置参考
（<https://developers.openai.com/codex/config-reference>）对
`model_providers.<id>.wire_api` 的说明是：

> Protocol used by the provider. `responses` is the only supported value, and it
> is the default when omitted.

即 Codex 无法配置成走 `/v1/chat/completions`。要接进本网关，网关必须自己
说 Responses 协议。

## 协议参考（来自 Codex 源码，非推测）

以下事实读自 `openai/codex` 仓库 `main` 分支源码，是本次实现的依据。

### 客户端请求

`codex-rs/codex-api/src/common.rs` 的 `ResponsesApiRequest`：

```rust
pub struct ResponsesApiRequest {
    pub model: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub instructions: String,
    pub input: Vec<ResponseItem>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tools: Option<ResponsesApiTools>,
    pub tool_choice: String,
    pub parallel_tool_calls: bool,
    pub reasoning: Option<Reasoning>,
    pub store: bool,
    pub stream: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream_options: Option<StreamOptions>,
    pub include: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub service_tier: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub prompt_cache_key: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub text: Option<TextControls>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub client_metadata: Option<HashMap<String, String>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub access_programs: Option<AccessPrograms>,
}
```

实际取值（`codex-rs/core/src/client.rs` 构造请求处）：

- `tool_choice = "auto"`、`store = false`、`stream = true`、`include = ["reasoning.encrypted_content"]`
- `prompt_cache_key` **总是发送**（会话指纹）
- `parallel_tool_calls = prompt.parallel_tool_calls && !model_info.use_responses_lite`
- `reasoning = Some(Reasoning { effort, summary, context })`，`effort` 取
  `none|minimal|low|medium|high|xhigh|max|ultra|persistent`，`summary` 取
  `auto|concise|detailed|none`
- 自定义 provider（`env_key` + `base_url`）下请求体**不含**
  `previous_response_id`（HTTP 路径的 `ResponsesApiRequest` 根本没有这个字段；
  只有 WebSocket 的 `ResponseCreateWsRequest` 有）
- 请求压缩（zstd）只在 OpenAI 官方后端 + ChatGPT 登录时启用
  （`client.rs` 的 `responses_request_compression`），自定义 provider 走明文

Headers（`codex-rs/codex-api/src/requests/headers.rs` 与
`codex-rs/codex-api/src/endpoint/responses.rs`）：

- `Authorization: Bearer <env_key 的值>`
- `Accept: text/event-stream`
- `session-id`、`thread-id`、`x-client-request-id`（值为 thread id）
- `x-openai-subagent`（仅子 agent）

`session-id` 与 `thread-id` 是本网关可用的会话线索。

### `input` 条目的形状

`codex-rs/protocol/src/models.rs`。`ResponseItem` 与 `ResponseInputItem` 都是
`#[serde(tag = "type", rename_all = "snake_case")]`，即**扁平**的带 `type` 标签
对象（不是 Chat Completions 的嵌套形状）。实现需要处理的变体：

| `type` | 字段 | 说明 |
|---|---|---|
| `message` | `role`、`content: [ContentItem]`、`id?`、`phase?` | `content` 内是 `input_text` / `input_image` / `input_audio` / `output_text` |
| `function_call` | `name`、`arguments`（JSON 字符串）、`call_id`、`id?` | 历史里的助手工具调用 |
| `function_call_output` | `call_id`、`output`（字符串或结构化数组） | 工具结果 |
| `custom_tool_call` | `name`、`input`（原始字符串）、`call_id` | freeform 工具调用 |
| `custom_tool_call_output` | `call_id`、`output` | freeform 工具结果 |
| `reasoning` | `summary: [ {type:"summary_text",text} ]`、`content?`、`encrypted_content?` | 推理项 |
| `local_shell_call` / `web_search_call` / `image_generation_call` / `tool_search_*` / `compaction*` / `configuration_update` | — | 本网关忽略 |

`ContentItem` 的变体：`InputText{text}`、`InputImage{image_url|file_id, detail?}`、
`InputAudio{audio_url}`、`OutputText{text}`。

### `tools` 的形状

`codex-rs/tools/src/tool_spec.rs` 的 `ToolSpec` 是 `#[serde(tag = "type")]` 枚举，
序列化后是**扁平**工具定义：

```json
{"type":"function","name":"shell","description":"...","strict":false,"parameters":{...}}
{"type":"custom","name":"apply_patch","description":"...","format":{"type":"grammar","syntax":"lark","definition":"..."}}
{"type":"web_search","external_web_access":true}
{"type":"tool_search","execution":"...","description":"...","parameters":{...}}
{"type":"namespace","name":"...","description":"...","tools":[...]}
```

`ResponsesApiTool` 的字段是 `name` / `description` / `strict` / `parameters` /
`defer_loading`——注意 `strict` 与 `parameters` 是**平级**的，没有 `function` 包装。

`apply_patch` 是 `custom`（freeform，带 lark grammar），不是 `function`——这一点
决定了工具转换必须支持 custom，否则 Codex 最核心的改代码能力失效。

### 响应侧：Codex 解析的事件

`codex-rs/codex-api/src/sse/responses.rs` 的 `process_responses_event`。
逐条列举它显式处理的事件与所需字段：

| 事件 | 必需字段 | Codex 的行为 |
|---|---|---|
| `response.created` | `response.id`（可缺，缺则 `response_id = None`） | `ResponseEvent::Created` |
| `response.output_item.done` | `item`（完整 `ResponseItem`） | `ResponseEvent::OutputItemDone`——**正文与工具调用的权威来源** |
| `response.output_item.added` | `item`（完整 `ResponseItem`） | `ResponseEvent::OutputItemAdded` |
| `response.output_text.delta` | `delta` | `ResponseEvent::OutputTextDelta`（流式显示） |
| `response.reasoning_summary_part.added` | `summary_index` | 开启推理段 |
| `response.reasoning_summary_text.delta` | `delta` + `summary_index` | 流式显示思考 |
| `response.reasoning_summary_text.done` | `item_id` + `text` + `summary_index` | 推理段收尾 |
| `response.reasoning_text.delta` | `delta` + `content_index` | 原始推理内容 delta |
| `response.completed` | `response.id`（**非 Option，缺失即解析失败**） | 收尾并返回 token 用量 |
| `response.failed` | `response.error{type,code,message}` | 转成错误，按 code 分类（限流/上下文超限/过载/…） |
| `response.incomplete` | `response.incomplete_details.reason` | 转成错误 |

明确被**忽略**的类型（同一函数的 match 分支）：

```
codex.response.metadata | response.content_part.added | response.content_part.done
| response.custom_tool_call_input.done | response.function_call_arguments.delta
| response.function_call_arguments.done | response.in_progress | response.metadata
| response.output_text.done | response.reasoning_summary_part.done
| responsesapi.websocket_timing
```

以及任何以 `.delta` 结尾的未知类型，以及其余未知类型的兜底分支。所以多发无害。

**三条硬约束**：

1. `response.function_call_arguments.delta` / `.done` 被忽略。工具调用的参数只能
   通过 `response.output_item.done` 里一份**完整的** item 交给 Codex。流式过程中
   必须缓存参数增量，结束时一次性发出。
2. `response.completed.response.id` 是 `String`（非 Option）——必须发。
3. `usage` 在 `ResponseCompleted` 里是 `Option`，但一旦出现，其
   `input_tokens` / `output_tokens` / `total_tokens` 三个字段都是非 Option 的
   `i64`，且 `input_tokens_details.cached_tokens` 也是非 Option。给 usage 就必须
   给全这三个数；给了 details 就必须给 `cached_tokens`。

`ResponseCompletedUsage` 结构（同文件）确认了 Codex 读取的缓存与推理计数：

```rust
settings: input_tokens, input_tokens_details{cached_tokens, cache_write_tokens},
output_tokens, output_tokens_details{reasoning_tokens}, total_tokens
```

流终止：Codex 的 SSE 循环读到 `ResponseEvent::Completed` 即 `return`，并在 body
结束时（`Ok(None)`）若无已完成事件则报
`"stream closed before response.completed"`。**不要求 `data: [DONE]`**；发了也没有
匹配分支，会被当作未知类型忽略。

### 错误体

Codex 解析 `{"error": {"type", "code", "message", "plan_type", "resets_at"}}`，
字段全是 `Option`。本网关现有的 `writeError` 输出
`{"error":{"message","type"}}` 形状兼容。

## 架构

### 新增文件

| 文件 | 职责 |
|---|---|
| `backend/proxy/responses.go` | `responses` handler、请求类型、Responses→Chat 转换（请求侧） |
| `backend/proxy/responses_stream.go` | Chat→Responses 转换（响应侧）：SSE 合成与非流式组装 |
| `backend/proxy/responses_test.go` | 端到端与单元测试 |

同时修改 `backend/proxy/server.go`（注册路由）、`backend/templates/catalog.json`、
`backend/templates/render.go`、`go.mod`、`AGENTS.md`。

### 路由

`backend/proxy/server.go` 的 `Start()` 增加一行：

```go
mux.HandleFunc("POST /v1/responses", s.responses)
```

### 数据流

```
Codex CLI
  │ POST /v1/responses  {model, instructions, input[], tools[], reasoning, prompt_cache_key, stream:true}
  ▼
requireLocalKey          ← 复用；Codex 发 Authorization: Bearer
  ▼
decodeBody(&responsesRequest)   ← 复用；32 MiB 上限与 413 文案不变
  ▼
responsesRequest.toChat()       ← 新增：input[] → messages，tools[] → chat tools，
                                    instructions → system message，reasoning.effort → reasoning_effort
  ▼
resolveMapping(model) → registry.Get(providerID)   ← 复用；404 / 503 口径不变
  ▼  provider.Kind == anthropic ? → 501 not_implemented
withCredential(ctx, p, session, run)                ← 复用；Codex 的 prompt_cache_key 优先作会话身份
  ▼  run = Compatible.Do(...)                       ← 复用（路径硬编码 /chat/completions，这里正好对）
上游 OpenAI 兼容 /chat/completions
  ▼  SSE 或 JSON
responses_stream.go 合成 Responses 事件
  ▼
Codex CLI
```

### 认证与会话身份

- 认证复用 `requireLocalKey`（`server.go:1422`）。Codex 发
  `Authorization: Bearer <AGENT_ROUTER_API_KEY>`，与该函数的解析路径一致；
  `/v1/messages` 那条 `x-api-key` 分支在此不需要。
- 会话身份：新增 `responsesSession(r, in responsesRequest)`，优先级为
  1. 请求体 `prompt_cache_key`（Codex 总是发，且天然是会话指纹）
  2. 现有 `sessionIdentity(r, system, firstUser)`（`credential_exec.go:42`）
  3. Codex 的 `session-id` / `thread-id` 请求头

  `prompt_cache_key` 只用于选凭据，**不转发给上游**——它不在现有 `Request`
  结构里，且部分兼容上游对未知字段的容忍度不确定。

### 复用清单（零改动）

| 需求 | 复用点 |
|---|---|
| 认证 | `requireLocalKey` |
| 体积上限 / 400 / 413 | `decodeBody` |
| 模型映射 → 404 | `resolveMapping` |
| provider 解析 → 503 | `registry.Get` + `p.Enabled` |
| 取 Key + failover | `withCredential` |
| 无凭据 → 424 | `credentialError` |
| 流式卡死保护 | `stallGuarded` |
| 日志与 token 落库 | `logEvent` 闭包 + `usage.Event`（**不改 schema**） |

## 请求转换：Responses → Chat Completions

### 消息

`instructions` 非空时作为第一条 `role: "system"` 消息，内容为纯字符串。

`input[]` 逐条映射：

| Responses item | Chat 消息 |
|---|---|
| `message`，`content` 全是 `input_text`/`output_text` | 该 role 的消息，`content` 为拼接后的字符串 |
| `message`，`content` 含 `input_image` | `content` 改为 parts 数组：`{"type":"text","text":...}` 与 `{"type":"image_url","image_url":{"url":...}}` |
| `function_call` | `role:"assistant"`，带 `tool_calls: [{"id":call_id,"type":"function","function":{"name","arguments"}}]` |
| `custom_tool_call` | 同上，`arguments` 为 `{"input": <原始 input 字符串>}` 的 JSON |
| `function_call_output` / `custom_tool_call_output` | `role:"tool"`，带 `tool_call_id: call_id`，`content` 为 output（结构化数组则拼成文本） |
| `reasoning` | 丢弃 |
| 其他变体 | 丢弃 |

`role` 值 `developer` 与 `system` 原样传递（现有 `/v1/chat/completions` 把
`developer` 改写成 `system`，这里沿用同一规则）。

**已放弃的替代做法**：把每条历史消息压平成纯文本。那样会丢掉 tool 消息的
`tool_call_id` 关联，上游对 tool 结果的处理会失真。逐条映射的代价是要处理
`input_image` 的 parts 形状，但保真度值得。

### 工具

| Responses 工具 | Chat 工具 |
|---|---|
| `{"type":"function","name","description","strict","parameters"}` | `{"type":"function","function":{"name","description","parameters"}}`（丢弃 `strict`、`defer_loading`） |
| `{"type":"custom","name","description","format"}` | `{"type":"function","function":{"name","description","parameters":{"type":"object","properties":{"input":{"type":"string","description":"Raw "+syntax+" payload"}},"required":["input"],"additionalProperties":false}}}` |
| `{"type":"web_search",...}` / `{"type":"tool_search",...}` / `{"type":"namespace",...}` | 丢弃；把名字收集进 `droppedTools`，写进请求日志的 error_message 供排查 |

custom 工具的识别集合（名字 → true）要传给响应侧，用于把上游 `tool_calls`
还原成 `custom_tool_call` item 而非 `function_call`。

### 其余字段

| Responses | Chat `Request` |
|---|---|
| `model` | 先经映射，替换为 `mapping.UpstreamModel` |
| `reasoning.effort` | `reasoning_effort`（原样字符串，含 `xhigh` 等 Codex 专有值） |
| `reasoning.summary` / `reasoning.context` | 丢弃（chat 协议无对应） |
| `text.verbosity` | `verbosity` |
| `text.format`（json_schema） | `response_format` |
| `tool_choice` | `tool_choice`：字符串原样；`{"type":"function","name"}` 转成 chat 的 `{"type":"function","function":{"name"}}` |
| `parallel_tool_calls` | `parallel_tool_calls` |
| `max_output_tokens` | `max_tokens` |
| `temperature` / `top_p` | 原样（Codex 不发，其他客户端可能发） |
| `stream` | `stream` |
| `store` / `include` / `stream_options` / `service_tier` / `client_metadata` / `access_programs` / `prompt_cache_key` / `previous_response_id` | 丢弃（见下文说明） |

流式为 `true` 时，网关强制 `stream_options = {"include_usage": true}`，否则上游
不报用量、日志恒为 0 token——与 `anthropicViaOpenAI` 的处理一致（`server.go:629`）。

`store` 丢弃：本网关无状态，不保存 response 供 `previous_response_id` 续接。
Codex 在自定义 provider 下恒发 `false`，且不发 `previous_response_id`，所以这个
限制不影响 Codex；其他客户端若依赖续接会拿不到历史，会在文档里写明。

## 响应转换：Chat Completions → Responses

### 输出 item 组装

上游 `choices[0].message`（非流式）或累积的 delta（流式）转换成 Responses item：

```json
{"type":"message","id":"msg_<hex>","role":"assistant","content":[{"type":"output_text","text":"..."}]}
{"type":"function_call","id":"fc_<hex>","call_id":"call_<hex>","name":"shell","arguments":"{\"command\":\"ls\"}"}
{"type":"custom_tool_call","id":"ctc_<hex>","call_id":"call_<hex>","name":"apply_patch","input":"*** Begin Patch\n..."}
{"type":"reasoning","id":"rs_<hex>","summary":[{"type":"summary_text","text":"..."}]}
```

- `message` 仅在正文非空时产出。
- `function_call` 与 `reasoning` 都只在有内容时产出。
- 工具调用名在 custom 集合里 → `custom_tool_call`，`arguments` 解出 `input`
  字段作为 `input`；否则 → `function_call`。
- `reasoning` item 同时要求 `summary` 存在（`Vec`，无默认值，必须给数组，
  可以为空）与可选的 `encrypted_content`。无推理内容时不产出该 item。
- `phase` 不发（可选字段）。

### 流式事件序列

按上游 chunk 顺序实时发出：

```
data: {"type":"response.created","response":{"id":"resp_x","object":"response","created_at":<unix>,"status":"in_progress","model":"<客户端模型名>","output":[]}}

（推理内容出现时，一次）
data: {"type":"response.reasoning_summary_part.added","summary_index":0,"item_id":"rs_x"}
data: {"type":"response.reasoning_summary_text.delta","delta":"...","summary_index":0,"item_id":"rs_x"}
（正文出现时，逐 delta）
data: {"type":"response.output_text.delta","delta":"...","item_id":"msg_x","content_index":0}
...
（上游流结束，逐 item）
data: {"type":"response.output_item.added","item":{...}}
data: {"type":"response.output_item.done","item":{...}}
...
data: {"type":"response.completed","response":{"id":"resp_x","object":"response","created_at":<unix>,"status":"completed","model":"...","output":[...],"usage":{"input_tokens":N,"output_tokens":N,"total_tokens":N,"input_tokens_details":{"cached_tokens":N},"output_tokens_details":{"reasoning_tokens":N}}}}
```

设计取舍与理由：

- **正文与推理的 item 在第一条 delta 之前就宣告**（发空 content/summary 的
  `response.output_item.added` 骨架），收尾时只补 `done`，且两者用同一个 item id。
  这是端到端实测出来的硬要求，不是可选项：最初的实现在收尾才发 `added`，真实
  Codex 立刻报 `OutputTextDelta without active item` 并**丢弃所有正文 delta**，
  流式逐字显示失效（只剩 `output_item.done` 里那份完整内容兜底，所以最终文本仍
  正确，缺陷单测测不出来）。用一个直连的探针上游对比两种事件顺序，确认「先 added
  后 delta」无报错、反序必报错。
- **工具调用相反：`added` 与 `done` 都留到收尾**。工具调用的参数只有在上游流结束
  时才完整，提前发 `added` 只能带空参数，而 Codex 忽略
  `function_call_arguments.delta`，空参数期间它拿不到任何补全手段。
- **`response.completed.response.output` 带完整 item 数组**，与 `output_item.done`
  重复。真实 API 如此，Codex 两条路径都会消费，重复是幂等的（`OutputItemDone`
  是历史追加，与 `Completed` 的 output 数组解析独立）。
- **不发 `data: [DONE]`**。Codex 以 `response.completed` 为终止，`[DONE]` 没有匹配
  分支。是否发送对 Codex 无影响；不发送可避免让其他严格解析 Responses 的客户端
  产生歧义。
- `model` 字段填**客户端请求的模型名**（映射前），与 `/v1/chat/completions`
  日志的口径一致。

### 非流式

`stream: false` 时返回单个 JSON：

```json
{"id":"resp_x","object":"response","created_at":<unix>,"status":"completed",
 "model":"<客户端模型名>","output":[...],"usage":{...}}
```

Codex 恒发 `stream: true`，这条路径服务于 curl 调试与其他客户端。

## 失败与边界

| 情形 | 处理 |
|---|---|
| 无本地 key / key 无效 | 401 `authentication_error`（复用） |
| 请求体超 32 MiB | 413 `invalid_request_error`（复用） |
| 请求体解码失败 | 400 `invalid_request_error`（复用） |
| 模型无映射 | 404 `model_not_found`（复用） |
| provider 不存在或禁用 | 503 `provider_unavailable`（复用） |
| provider 无凭据 | 424 `provider_credentials_missing`（复用） |
| provider 是 `anthropic` 类 | **501 `not_implemented`**，消息说明本端点只覆盖 Chat Completions 类上游 |
| 上游 4xx/5xx | 原样透传上游状态码与 body（与 `chatCompletions` 一致，Codex 能解析 `{"error":{...}}`） |
| 上游 HTTP 传输错误 | 502 `upstream_error`（复用） |
| 流中途断掉（scanner 报错，或下游提前断开） | **不发** `response.completed`，改发 `response.failed`；日志 `success=false` + `upstream stream ended early: ...` |
| 流正常结束但从未收到 `finish_reason` | 判失败，发 `response.failed`，日志 `success=false` |
| `store: true` | 忽略（无状态） |
| `previous_response_id` 存在 | 忽略（不参与路由或历史恢复） |

「截断时不发收尾事件」沿用 `pipeOpenAIStreamToAnthropic` 的既有判断
（`server.go:1201-1228`）：宁可让客户端知道交换坏了并重试，也不能让它误以为
这是正常的回合结束。

## 模板：Codex 接入

### catalog 条目

`backend/templates/catalog.json` 新增：

```json
{
  "id": "codex",
  "name": "Codex",
  "cli": "codex",
  "configRel": ".codex/config.toml",
  "multiProvider": true,
  "shape": "codex-toml",
  "modelSlots": [{ "key": "MODEL", "label": "默认模型" }]
}
```

无 `skillsRel`：Codex 没有 skills 目录，`Resolve` 对空串不设 `SkillsDir`，UI 自动
隐藏 Skills 入口。

`multiProvider: true` 复用「模型勾选列表」做**profile 选择器**——这份勾选决定要为
哪些模型生成 `--profile` 文件，而不是往配置里塞一个模型清单（Codex 的配置只有一个
顶层 `model` 标量，清单无处落地）。单 slot 决定主配置的默认模型。

**但默认值必须与 ai-sdk 类工具不同，且不能由前端假定。** 同一个勾选列表在两类工具
上语义相反：ai-sdk/pi 是「配置里列出哪些候选模型」，默认全选才有意义；codex 是
「生成哪些文件」，默认全选会在用户第一次打开面板时写出几十份 profile。所以默认值
由后端 `Generator.SelectedModels` 按 shape 分派，前端从 `Preview.SelectedModels`
起步：

| 情形 | codex 默认勾选 |
|---|---|
| 磁盘上已有本网关生成的 profile | 这些 profile 对应的模型（重开面板显示上次结果） |
| 一份都没有 | 主配置的默认模型（至少产出与 `model =` 对应的那一份） |
| 用户显式勾过（`selected` 非 nil） | 完全按勾选，**包括清空** |

认领磁盘文件靠内容而非文件名：目录里有其它工具留下的成百个 profile，只有
`model_provider` 指向本网关且模型可路由的那些才归我们管。默认取自磁盘这一点与
`SlotBaseline` 同一原则——重开面板必须显示上次写入的结果，否则每次写入都像丢了
选择。

### 生成内容

主配置：

```toml
model = "<slot 选中的 client model，未选则取第一个可路由模型>"
model_provider = "agent-router"

[model_providers.agent-router]
name = "Agent Router"
base_url = "http://127.0.0.1:9400/v1"
wire_api = "responses"
env_key = "AGENT_ROUTER_API_KEY"
```

形状与本机现有的 `[model_providers.omniroute]` 一致（同类工具已这么写）。`env_key`
沿用 `templates` 包的 `envFor(g.providerName)`，产出 `AGENT_ROUTER_API_KEY`，与
`backend/envcfg/` 写入用户 shell 的变量名相同——用户装完 key 后无需再手工导出。

### profile 文件（每个选中模型一份）

```
<CODEX_HOME>/<消毒名>.config.toml     # 例：gpt-5-6-sol.config.toml
```

```toml
model = "gpt-5.6-sol"
model_provider = "agent-router"
```

用户用 `codex --profile gpt-5-6-sol` 切换。三条依据都是实测得出（CLI 0.154.0）：

1. **只写两行。** provider 表从主 `config.toml` 合并进来，实测确认
   （`base_url` 与 `env_key` 都生效），所以改一次网关地址只需改一处。
2. **名字必须消毒。** `--profile` 只接受 `[A-Za-z0-9_-]`（`codex-rs
   config_types.rs` 的 `ProfileV2Name::from_str`）；实测 `--profile gpt-5.6-sol`
   直接报 `invalid --profile value`。所以模型名里的 `.` 与 `/` 折成 `-`：
   `Ybl/deepseek-v4.1-flash` → `Ybl-deepseek-v4-1-flash`。折换有损，必须去重
   （`a/b` 与 `a-b` 都变 `a-b`），否则两个模型共用一个名字会有一个静默选中另一个。
3. **只增不删。** 收窄选择时先前生成的 profile 留在原地：`<CODEX_HOME>` 下还有
   其它工具留下的成百个 profile 文件，按「不在本次选中集合里」去删会误伤它们。
   残留文件若指向已不可路由的模型，运行时网关回 404，不会静默走错。

### 写回策略

新增 `github.com/pelletier/go-toml/v2` 依赖，**只用于读取定位**；写回仍是行级
splice，与现有 `mergeOMPBytes`（`render.go:63`）同一思路。

`renderFor` 增加分支（`render.go:21` 的 `if t.Shape == "omp"` 旁边）。`mergeProvider`
**不加** `case "codex-toml"`：那是 JSON 专用路径，`renderFor` 已提前返回，加进去
会是死代码（已在注释里说明）。

`renderCodexTOML(g, t)` 的行为：

1. 文件不存在或读失败 → 直接输出上面那份完整文本。
2. 文件存在 → `toml.Unmarshal` 进 `map[string]any` 只用来**校验语法**（实现时发现
   `go-toml/v2` 的公开 API 不提供位置信息，`BurntSushi/toml` 也不提供；只有
   `go-toml/v2/unstable` 的 AST 给出 `Node.Raw{Offset,Length}`，所以定位走它）→
   - `unstable.Parser` 逐表达式走 AST，用 `nodeKey` 取点分键名与行号。表头节点自身
     的 Raw 范围为空，位置只能从它的键子节点读。
   - 定位 `[model_providers.<providerName>]` 的行区间（区间末回退掉末尾空行，把分隔
     空行留给下一段），替换成新的 provider 块；不存在则追加到文件末尾。
   - 顶层 `model` 与 `model_provider`：已存在则原地替换该行；不存在则插入到第一个
     表头之前——TOML 语法要求顶层标量在表头之前，插到表里会变成那张表的键。两处编辑
     落在同一行时（provider 表恰好是第一个表头）按 `order` 排序后合并成一次替换。
   - 其余字节原样保留：注释、空行、`[desktop]` / `[plugins."…"]` 等用户的嵌套表
     都不动。实测本机那份 11,977 字节的配置：只改动 1 行（顶层 `model`），其余逐字
     保留，且幂等。
3. 解析失败（TOML 语法错误）→ 不覆盖。`Render` 返回原文件内容不变，由 UI 呈现
   「生成内容 == 当前内容」即无差异，用户能看出没生效。**绝不能**像 JSON 分支那样
   回退成「新建文档」，那会把用户的 TOML 覆盖成 JSON。

`readSlotBaseline` 对 `codex-toml` 读现有 `model` 值作为 slot 基线，使 UI 打开时
能显示用户当前选中的模型，且重复渲染幂等。

### 前端

工具列表由 `ListToolTemplates()` 动态返回，配置对比是纯文本行级 diff（`renderDiff`
只做 `split("\n")` 与行字符串多重集比较，不解析 JSON，传入 TOML 正常工作），图标
`toolIcons` 未命中时回落 `<Bot>`（与 `kilo` 一样）。

本次改动加了 profile 区块：`Preview.Profiles` 把将生成的 `--profile` 名与对应模型
传给 UI，在「模型配置」面板末尾列出可用的 `codex --profile <名>`。前端不 import
Wails 生成的 App 绑定（只用 `wailsjs/runtime`），走运行时反射，所以加字段不必重新
生成绑定；`frontend/src/lib/types.ts` 是手工镜像，需同步加 `profiles`。

可选（本次不做）：为 codex 加一个图标资源。

## 测试

新增 `backend/proxy/responses_test.go`，沿用现有惯例：`newTestServer` +
`httptest.NewServer` 假上游 + 直接调 `s.responses(recorder, req)`。

请求侧转换（单元）：

1. `instructions` → 首条 system 消息
2. `input` 里 `message` / `function_call` / `function_call_output` / `custom_tool_call`
   各自的映射结果；`reasoning` 被丢弃
3. `input_image` 转成 chat 的 parts 数组
4. 扁平 `function` 工具 → 嵌套 chat 工具；`custom` 工具 → wrapper schema；
   `web_search` / `namespace` 被丢弃且名字进了日志
5. `reasoning.effort` → `reasoning_effort`；`text.verbosity` → `verbosity`；
   `text.format` → `response_format`；`max_output_tokens` → `max_tokens`
6. `tool_choice` 的字符串与对象两种形状

响应侧转换（单元）：

7. 流式：`response.created` 为首；正文 delta 顺序与内容；`response.completed` 为末且
   `response.id` 存在、`usage` 三个字段齐全
8. 工具调用：上游 `tool_calls` → 一份完整 `function_call` item 出现在
   `output_item.done` 与 `completed.output`；`apply_patch` 走 `custom_tool_call`
   且 `input` 是原始字符串（不是 JSON 包装）
9. 推理：上游 `reasoning_content` → `reasoning_summary_part.added` +
   `reasoning_summary_text.delta`，且 `completed.output` 含 `reasoning` item
10. 非流式：单个 JSON 的 `status` / `output` / `usage`

端到端（`httptest` 假上游）：

11. 认证：无 key → 401；有效 key → 通过
12. 模型映射：断言上游收到 `/chat/completions` 且 body 的 `model` 是 upstream 名
13. 凭据 failover 在 Responses 路径同样生效（照抄
    `credential_failover_test.go:62` 的套路）
14. 上游流截断（复用 `server_test.go:1076` 的 channel + `AfterFunc` 模式，并缩短
    `s.stallTimeout`）→ 响应体不含 `response.completed`，日志 `success=false`
15. Anthropic 类 provider → 501
16. 用量落库：`usage.SQLiteTracker.Summary()` 与 `GetRequestLog` 的 token 数与
    `credential_mask` 正确

模板（`backend/templates/`）：

17. `Render(codex)` 对不存在的文件产出完整文本
18. 对本机真实 `config.toml` 的结构做成 fixture（含 `[desktop]` 嵌套表、注释、
    `[model_providers.omniroute]`），断言只改动 provider 块与顶层两行，其余行
    逐字节不变
19. 幂等：连续渲染两次结果相同
20. TOML 语法错误的输入 → 输出等于输入（不被覆盖）

端到端实测（非单测）：

21. 用本机 `codex` CLI 0.154.0 真跑一次——起网关 + 假上游，用 `codex exec` 配
    `CODEX_HOME` 指向本网关，确认 Codex 真能完成一个带工具调用的回合。单测绿不等于
    协议对，这一步是本次交付的验收条件。

    **已执行，并因此发现一个单测测不出的真实缺陷。** 结果：Codex 完成两轮工具往返
    （`exec_command` 真执行了 `echo hi-from-e2e` 并拿回输出），零 ERROR。修复前的
    实测日志报 `OutputTextDelta without active item` 与 `unsupported call: shell`
    ——后者是本机测试假上游写错了工具名（Codex 实际注册的是 `exec_command`），前者
    是实现的真实缺陷，见上文事件序列一节。一旦修好，重新实测零报错。

    网关侧日志同时确认：上游收到的是 `/chat/completions` 且 `model` 已换成
    `upstream-model`；第二轮请求出现 `assistant` + `tool` 角色，说明
    `function_call_output` 正确转成了带 `tool_call_id` 的 `role:"tool"` 消息；用量
    正确落库（42 input / 7 output / 5 cached，与假上游给的一致）；hosted 工具的丢弃
    记录也写进了 `error_message`。

## 文档同步

`AGENTS.md` 需要相应更新（它记录了这些内容）：

- 路由清单：`backend/proxy/server.go` 那条从两条路由更新为四条（含
  `/v1/responses` 与 `/v1/messages`）
- 「`proxy` 三处取 Key 点」更新为四处
- `backend/templates/` 的 CLI 目录列表加上 codex
- 依赖说明：新增 TOML 库
- 「重要文件」表加上 `backend/proxy/responses.go` 与 `responses_stream.go`

## 明确不做

- Responses↔Anthropic 转换（映射到 anthropic 类 provider 时 501）
- `previous_response_id` 续接与 `store: true` 的服务端会话（网关无状态）
- WebSocket 传输（`supports_websockets`）；Codex 的 HTTP SSE 是默认且必需的路径
- hosted 工具（`web_search` / `image_generation` / `tool_search`）的真实执行——上游
  是 chat 上游，本来就没有这些能力
- Codex 的 zstd 请求压缩（自定义 provider 下 Codex 不启用）
- 前端图标资源与 UI 改动
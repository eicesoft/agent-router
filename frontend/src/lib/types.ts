export type Provider = {
  id: string;
  name: string;
  kind: string;
  baseUrl: string;
  apiKeyRef: string;
  icon: string;
  modelPrefix: string;
  enabled: boolean;
  models: string[];
  availableModels: AvailableModel[];
  // How the gateway picks among this provider's keys. Empty means the default
  // ("session").
  credentialMode: string;
  // models.dev 目录 id（提供商选择器写入）。空表示未关联，保存时不同步价格。
  devId: string;
  updatedAt: string;
};
// models.dev 目录条目。models 字段在后端丢弃，不进入 UI。
export type DevProvider = {
  id: string;
  env: string[];
  npm: string;
  api: string;
  name: string;
  doc: string;
};
// models.dev 模型能力元数据（models.json 本地缓存），供映射抽屉回填能力字段。
export type DevModel = {
  id: string;
  name: string;
  description: string;
  reasoning: boolean;
  toolCall: boolean;
  structuredOutput: boolean;
  releaseDate: string;
  modalities: { input: string[]; output: string[] };
  limit: { context: number; output: number };
};
// One upstream API key in a provider's pool. The secret itself never leaves the
// OS Keychain, so this carries no key material at all. Mask is the derived
// display form (ss****sfg); it is the only secret-derived value that is stored.
export type ProviderCredential = {
  id: string;
  providerId: string;
  name: string;
  mask: string;
  enabled: boolean;
  weight: number;
  // "active" or "invalid"; invalid means the upstream rejected the key.
  status: string;
  lastError: string;
  createdAt: string;
  updatedAt: string;
};
export type CredentialMode =
  "session" | "round_robin" | "least_used" | "random";
export type AvailableModel = { id: string; created: number };
export type ModelMapping = {
  id: string;
  clientModel: string;
  providerId: string;
  upstreamModel: string;
  // 与 clientModel 等价的额外客户端模型名。仅用于命中路由，不会出现在
  // /v1/models 与生成的 CLI 配置中。
  aliases: string[];
  enabled: boolean;
  // 模型元数据，仅供配置与列表展示，网关路由不消费。
  // 大小单位为 tokens，0 表示未设置。
  inputTypes: string[];
  inputContextSize: number;
  outputSize: number;
  // 单价仅配置与展示，单位 USD / 百万 tokens，默认 0（未设置）；网关不据此计费。
  inputPrice: number;
  outputPrice: number;
  cacheReadPrice: number;
};
export type LocalAPIKey = {
  id: string;
  name: string;
  key: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
};
export type EnvStatus = {
  varName: string;
  value: string;
  set: boolean;
  matches: boolean;
  written: boolean;
  os: string;
  profilePath: string;
  error: string;
};
export type AgentPreset = {
  id: string;
  name: string;
  description: string;
  model: string;
  systemPrompt: string;
  tools: string[];
};
export type ModelSlot = { key: string; label: string };
export type RoutableModel = { id: string; name: string };
export type ToolPreview = {
  id: string;
  name: string;
  cli: string;
  installed: boolean;
  configPath: string;
  skillsPath: string;
  exists: boolean;
  current: string;
  content: string;
  multiProvider: boolean;
  modelSlots: ModelSlot[];
  routable: RoutableModel[];
  slotModels: Record<string, string>;
  selectedModels: string[];
  profiles: ToolProfile[];
};
// ToolProfile 是一次写入会同时生成的 Codex --profile 文件（一个模型一份）。
export type ToolProfile = {
  name: string;
  model: string;
  path: string;
  exists: boolean;
  current: string;
  content: string;
};
export type Usage = {
  requests: number;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number;
  reasoningOutputTokens: number;
  costUsd: number;
  successRate: number;
};
export type UsageStat = {
  key: string;
  name?: string;
  // Display form of an upstream key (ss****sfg); set only for the per-credential
  // breakdown.
  mask?: string;
  requests: number;
  successes: number;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number;
  reasoningOutputTokens: number;
};
export type UsageBreakdown = {
  providers: UsageStat[];
  models: UsageStat[];
  keys: UsageStat[];
  credentials: UsageStat[];
};
export type RequestLog = {
  id: number;
  createdAt: string;
  tokenId: string;
  tokenName: string;
  providerId: string;
  providerName: string;
  clientModel: string;
  upstreamModel: string;
  userAgent: string;
  requestBody: string;
  responseBody: string;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number;
  reasoningOutputTokens: number;
  success: boolean;
  latencyMs: number;
  errorMessage: string;
  // Which pooled upstream key served the request. credentialMask is the display
  // form (ss****sfg); credentialName is the operator's label for it.
  credentialId: string;
  credentialName: string;
  credentialMask: string;
};
export type RequestLogPage = {
  items: RequestLog[];
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  stats: RequestLogStats;
};
export type RequestLogStats = {
  requests: number;
  successes: number;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number;
  reasoningOutputTokens: number;
};
export type RequestLogFilter = {
  token: string;
  model: string;
  provider: string;
  status: string;
  from: string;
  to: string;
};
export type Bootstrap = {
  providers: Provider[];
  mappings: ModelMapping[];
  agents: AgentPreset[];
  usage: Usage;
  apiKeys: LocalAPIKey[];
  proxyRunning: boolean;
  settings: AppSettings;
  // 同名链的起点策略，按客户端模型名索引。缺省即 "failover"。
  chainModes: Record<string, ChainMode>;
};

// failover：永远从链首开始，健康就短路。round_robin：每次请求把起点向后挪
// 一家，让链上各家分摊流量；起点之后仍是 failover。
export type ChainMode = "failover" | "round_robin";
export type AppSettings = {
  host: string;
  port: number;
  theme: string;
};
export type PlaygroundChunk = { runId: string; data: string; done?: boolean };
export type PlaygroundResult = { status: number; latencyMs: number };
export type PlaygroundMessage = { role: string; content: string };

export type SkillRoot = { path: string; source: string };
export type Skill = {
  name: string;
  description: string;
  dir: string;
  root: string;
  source: string;
  enabled: boolean;
  files: string[];
  updatedAt: string;
  missingFrontmatter: boolean;
  tokenEstimate: number;
};
export type SkillDetail = Skill & { body: string };
export type SkillSummary = {
  roots: SkillRoot[];
  skills: Skill[];
  conflicts: string[];
};
// How a skill relates to a CLI's skills directory: published by this app
// ("linked"), linkable ("missing"), a skill deleted after linking ("broken"),
// or an entry this app did not create ("external").
export type SkillLinkState = "linked" | "missing" | "external" | "broken";
export type SkillLink = {
  name: string;
  dir: string;
  target: string;
  state: SkillLinkState;
};
export type ToolSkillLinks = {
  targetDir: string;
  links: SkillLink[];
};

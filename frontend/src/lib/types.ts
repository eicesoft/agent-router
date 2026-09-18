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
  updatedAt: string;
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
  enabled: boolean;
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
};
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

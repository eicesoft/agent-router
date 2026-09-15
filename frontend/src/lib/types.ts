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
  updatedAt: string;
};
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
  exists: boolean;
  current: string;
  content: string;
  multiProvider: boolean;
  modelSlots: ModelSlot[];
  routable: RoutableModel[];
  slotModels: Record<string, string>;
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
};
export type Bootstrap = {
  providers: Provider[];
  mappings: ModelMapping[];
  agents: AgentPreset[];
  usage: Usage;
  apiKeys: LocalAPIKey[];
  proxyRunning: boolean;
};

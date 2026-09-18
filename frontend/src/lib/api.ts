import type {
  AppSettings,
  Bootstrap,
  EnvStatus,
  LocalAPIKey,
  ModelMapping,
  PlaygroundMessage,
  PlaygroundResult,
  Provider,
  ProviderCredential,
  RequestLog,
  RequestLogFilter,
  RequestLogPage,
  Skill,
  SkillDetail,
  SkillSummary,
  ToolPreview,
  ToolSkillLinks,
  UsageBreakdown,
} from "./types";

// Wails injects window.go.main.App at runtime. The sample preserves a useful
// browser-only preview so the dashboard can be designed without Go running.
const demo: Bootstrap = {
  providers: [
    {
      id: "openai",
      name: "OpenAI",
      kind: "openai",
      baseUrl: "https://api.openai.com/v1",
      apiKeyRef: "",
      icon: "openai",
      modelPrefix: "",
      enabled: true,
      models: ["gpt-4.1", "gpt-4o-mini"],
      availableModels: [
        { id: "gpt-4.1", created: 0 },
        { id: "gpt-4o-mini", created: 0 },
      ],
      credentialMode: "session",
      updatedAt: "",
    },
    {
      id: "anthropic",
      name: "Anthropic",
      kind: "anthropic",
      baseUrl: "https://api.anthropic.com",
      apiKeyRef: "",
      icon: "anthropic",
      modelPrefix: "",
      enabled: false,
      models: ["claude-sonnet-4-5"],
      availableModels: [{ id: "claude-sonnet-4-5", created: 0 }],
      credentialMode: "session",
      updatedAt: "",
    },
  ],
  mappings: [
    {
      id: "default-gpt",
      clientModel: "gpt-4.1",
      providerId: "openai",
      upstreamModel: "gpt-4.1",
      enabled: true,
    },
  ],
  agents: [
    {
      id: "code-review",
      name: "Code Reviewer",
      description: "Review code changes with a focused checklist.",
      model: "gpt-4.1",
      systemPrompt: "",
      tools: ["filesystem", "terminal"],
    },
  ],
  usage: {
    requests: 1284,
    inputTokens: 842310,
    outputTokens: 218401,
    cachedInputTokens: 0,
    reasoningOutputTokens: 0,
    costUsd: 14.82,
    successRate: 99.6,
  },
  apiKeys: [
    {
      id: "key-demo",
      name: "本地开发",
      key: "ar-demo-key",
      enabled: true,
      createdAt: "",
      updatedAt: "",
    },
  ],
  proxyRunning: true,
  settings: { host: "127.0.0.1", port: 9400, theme: "light" },
};
export async function bootstrap(): Promise<Bootstrap> {
  const app = (window as any).go?.main?.App;
  return app ? app.GetBootstrap() : demo;
}
export async function setProxyRunning(enabled: boolean): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.SetProxyRunning(enabled);
}

export async function saveSettings(
  settings: AppSettings,
): Promise<AppSettings> {
  const app = (window as any).go?.main?.App;
  if (!app) return { ...demo.settings, ...settings };
  return app.SaveSettings(settings);
}
export async function getUsageBreakdown(): Promise<UsageBreakdown> {
  const app = (window as any).go?.main?.App;
  if (!app) return { providers: [], models: [], keys: [], credentials: [] };
  return app.GetUsageBreakdown();
}
export async function listToolTemplates(): Promise<ToolPreview[]> {
  const app = (window as any).go?.main?.App;
  if (!app) return [];
  return app.ListToolTemplates();
}
export async function renderToolTemplate(
  id: string,
  slotModels: Record<string, string> = {},
  modelIDs?: string[],
): Promise<ToolPreview> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持写入");
  return app.RenderToolTemplate(id, slotModels, modelIDs);
}
export async function writeToolTemplate(
  id: string,
  slotModels: Record<string, string> = {},
  modelIDs?: string[],
): Promise<string> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持写入");
  return app.WriteToolTemplate(id, slotModels, modelIDs);
}
export async function toggleProvider(
  id: string,
  enabled: boolean,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.ToggleProvider(id, enabled);
}
export async function saveProvider(input: Provider): Promise<Provider> {
  const app = (window as any).go?.main?.App;
  return app ? app.SaveProvider(input) : input;
}
export async function deleteProvider(id: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.DeleteProvider(id);
}
export async function setProviderAPIKey(
  providerId: string,
  apiKey: string,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app && apiKey) await app.SetProviderAPIKey(providerId, apiKey);
}

export async function listProviderCredentials(
  providerId: string,
): Promise<ProviderCredential[]> {
  const app = (window as any).go?.main?.App;
  if (!app) return [];
  return app.ListProviderCredentials(providerId);
}

export async function addProviderCredential(
  providerId: string,
  name: string,
  apiKey: string,
): Promise<ProviderCredential> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持写入凭据");
  return app.AddProviderCredential(providerId, name, apiKey);
}

export async function updateProviderCredential(
  id: string,
  name: string,
  weight: number,
): Promise<ProviderCredential> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持写入凭据");
  return app.UpdateProviderCredential(id, name, weight);
}

export async function deleteProviderCredential(id: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.DeleteProviderCredential(id);
}

export async function toggleProviderCredential(
  id: string,
  enabled: boolean,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.ToggleProviderCredential(id, enabled);
}

export async function resetProviderCredentialStatus(id: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.ResetProviderCredentialStatus(id);
}

export async function setProviderCredentialMode(
  providerId: string,
  mode: string,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.SetProviderCredentialMode(providerId, mode);
}

export async function saveModelMapping(
  input: ModelMapping,
): Promise<ModelMapping> {
  const app = (window as any).go?.main?.App;
  return app ? app.SaveModelMapping(input) : input;
}

export async function saveLocalAPIKey(
  input: LocalAPIKey,
): Promise<LocalAPIKey> {
  const app = (window as any).go?.main?.App;
  if (!app) return input;
  return app.SaveLocalAPIKey(input);
}

export async function toggleLocalAPIKey(
  id: string,
  enabled: boolean,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.ToggleLocalAPIKey(id, enabled);
}

export async function deleteLocalAPIKey(id: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.DeleteLocalAPIKey(id);
}

export async function localAPIKeyEnvStatus(id: string): Promise<EnvStatus> {
  const app = (window as any).go?.main?.App;
  if (!app) {
    return {
      varName: "AGENT_ROUTER_API_KEY",
      value: "",
      set: false,
      matches: false,
      written: false,
      os: "browser",
      profilePath: "",
      error: "",
    };
  }
  return app.LocalAPIKeyEnvStatus(id);
}

export async function exportLocalAPIKeyEnv(id: string): Promise<EnvStatus> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持写入环境变量");
  return app.ExportLocalAPIKeyEnv(id);
}

export async function fetchProviderModels(
  providerId: string,
  kind: string,
  baseUrl: string,
  apiKey: string,
): Promise<import("./types").AvailableModel[]> {
  const app = (window as any).go?.main?.App;
  if (!app) return [];
  return app.FetchProviderModels(providerId, kind, baseUrl, apiKey);
}

export async function listRequestLogs(
  page: number,
  pageSize = 20,
  filter: RequestLogFilter = {
    token: "",
    model: "",
    provider: "",
    status: "",
    from: "",
    to: "",
  },
): Promise<RequestLogPage> {
  const app = (window as any).go?.main?.App;
  if (!app) {
    return { items: [], page, pageSize, total: 0, totalPages: 0 };
  }
  return app.ListRequestLogs(page, pageSize, filter);
}

export async function getRequestLog(id: number): Promise<RequestLog> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持查看日志详情");
  return app.GetRequestLog(id);
}

export async function playgroundChat(
  runId: string,
  model: string,
  messages: PlaygroundMessage[],
  // off/low/medium/high — 归一成后端的 reasoning_effort，off 不发该字段。
  reasoningLevel = "off",
): Promise<PlaygroundResult> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持演练场");
  return app.PlaygroundChat(runId, model, messages, reasoningLevel);
}

export async function cancelPlayground(): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.CancelPlayground();
}

export async function fetchProviderIcon(baseUrl: string): Promise<string> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("请在桌面应用中获取网站图标");
  return app.FetchProviderIcon(baseUrl);
}

const demoSkills: SkillSummary = {
  roots: [{ path: "~/.agents/skills", source: "user" }],
  skills: [
    {
      name: "code-review",
      description: "Review the changes since a fixed point along two axes.",
      dir: "~/.agents/skills/code-review",
      root: "~/.agents/skills",
      source: "user",
      enabled: true,
      files: ["SKILL.md"],
      updatedAt: "",
      missingFrontmatter: false,
      tokenEstimate: 320,
    },
  ],
  conflicts: [],
};
export async function listSkills(): Promise<SkillSummary> {
  const app = (window as any).go?.main?.App;
  return app ? app.ListSkills() : demoSkills;
}
export async function getSkill(dir: string): Promise<SkillDetail> {
  const app = (window as any).go?.main?.App;
  if (!app) {
    const demo = demoSkills.skills[0];
    return { ...demo, body: "---\nname: code-review\n---\n\n(演示内容)" };
  }
  return app.GetSkill(dir);
}
export async function toggleSkill(
  dir: string,
  enabled: boolean,
): Promise<Skill> {
  const app = (window as any).go?.main?.App;
  if (!app) {
    return {
      ...demoSkills.skills[0],
      dir,
      enabled,
    };
  }
  return app.ToggleSkill(dir, enabled);
}
export async function deleteSkill(dir: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (app) await app.DeleteSkill(dir);
}
export async function saveSkillBody(dir: string, body: string): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持保存 skill");
  await app.SaveSkillBody(dir, body);
}
export async function listSkillLinks(toolId: string): Promise<ToolSkillLinks> {
  const app = (window as any).go?.main?.App;
  if (!app) return { targetDir: "", links: [] };
  return app.ListSkillLinks(toolId);
}
export async function setSkillLink(
  toolId: string,
  skillDir: string,
  linked: boolean,
): Promise<void> {
  const app = (window as any).go?.main?.App;
  if (!app) throw new Error("浏览器预览模式不支持修改 skill 链接");
  await app.SetSkillLink(toolId, skillDir, linked);
}

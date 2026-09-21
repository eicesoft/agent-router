import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Card,
  Checkbox,
  Chip,
  Input,
  Label,
  Modal,
  Pagination,
  Separator,
  Switch,
  Tabs,
  Tag,
  TagGroup,
  TextField,
  Tooltip,
} from "@heroui/react";
import {
  Activity,
  ArrowDownRight,
  ArrowUpRight,
  Bot,
  BrainCircuit,
  type LucideIcon,
  Box,
  ChevronDown,
  Copy,
  Eye,
  EyeOff,
  Folder,
  FolderSymlink,
  GitCompare,
  Globe2,
  KeyRound,
  Link,
  Loader2,
  Orbit,
  Pencil,
  Plus,
  Radio,
  Sparkles,
  SquareTerminal,
  Trash2,
  Variable,
} from "lucide-react";
import {
  bootstrap,
  deleteLocalAPIKey,
  deleteProvider,
  exportLocalAPIKeyEnv,
  getRequestLog,
  getUsageBreakdown,
  listRequestLogs,
  listSkills,
  addProviderCredential,
  listToolTemplates,
  listSkillLinks,
  localAPIKeyEnvStatus,
  renderToolTemplate,
  saveLocalAPIKey,
  saveModelMapping,
  saveProvider,
  setChainMode,
  setProviderAPIKey,
  setProxyRunning,
  toggleLocalAPIKey,
  toggleProvider,
  writeToolTemplate,
} from "./lib/api";
import type {
  Bootstrap,
  ChainMode,
  EnvStatus,
  LocalAPIKey,
  ModelMapping,
  Provider,
  RequestLog,
  RequestLogFilter,
  RequestLogPage,
  ToolPreview,
  UsageBreakdown,
  UsageStat,
} from "./lib/types";
import { defaultClientModel } from "./lib/naming";
import { ClipboardSetText } from "../wailsjs/runtime/runtime";
import opencodeIcon from "./assets/opencode.svg";
import piIcon from "./assets/pi.svg";
import mimocodeIcon from "./assets/mimocode.png";
import claudeIcon from "./assets/claude.png";
import ompIcon from "./assets/omp.svg";
import { Sidebar, type Page } from "./components/Sidebar";
import { SettingsModal } from "./components/SettingsModal";
import {
  ProviderDrawer,
  type ProviderFormState,
} from "./components/ProviderDrawer";
import {
  MappingDrawer,
  type MappingDraft,
  type MappingFormState,
} from "./components/MappingDrawer";
import { FieldSelect } from "./components/FieldSelect";
import { Playground } from "./components/Playground";
import { DateRangeField } from "./components/DateRangeField";
import { SkillsPanel } from "./components/SkillsPanel";
import { SkillsLinkModal } from "./components/SkillsLinkModal";
import { formatTokenCount, num } from "./lib/format";
function TokenValue({
  value,
  as = "strong",
}: {
  value: number;
  as?: "strong" | "span";
}) {
  const Value = as;
  return (
    <Tooltip>
      <Tooltip.Trigger>
        <Value className="usage-token-value">{formatTokenCount(value)}</Value>
      </Tooltip.Trigger>
      <Tooltip.Content>{num.format(value)} Tokens</Tooltip.Content>
    </Tooltip>
  );
}
const requestLogPageSize = 15;
// 查询慢于此阈值才点亮 loading：命中覆盖索引后单页通常几十毫秒返回，
// 立即显示遮罩只会造成一帧闪烁，反而像卡顿。
const LOG_SLOW_MS = 180;
const LOG_COLUMNS = [
  "状态",
  "密钥",
  "客户端",
  "模型",
  "提供商",
  "上游 Key",
  "输入",
  "输出",
  "Tokens",
  "耗时",
  "时间",
];
const LOG_COLUMN_WIDTHS = [72, 76, 150, 136, 90, 150, 200, 200, 140, 72, 168];
const pageMeta: Record<Page, { title: string; description: string }> = {
  overview: {
    title: "控制台概览",
    description: "查看本地网关的运行状态、用量与快速接入方式。",
  },
  providers: {
    title: "提供商连接",
    description: "管理上游服务与可用于路由的模型。",
  },
  keys: {
    title: "本地密钥",
    description:
      "本地网关 127.0.0.1:9400 的 /v1/* 接口需要携带已启用的密钥（Authorization: Bearer 密钥）。",
  },
  mappings: {
    title: "模型映射",
    description: "启用提供商的激活模型会自动生成映射，可编辑名称或启停。",
  },
  logs: {
    title: "请求日志",
    description: "查看经本地代理转发的请求记录。",
  },
  usage: {
    title: "使用情况",
    description: "查看 Token 消耗、提供商与模型的用量分布。",
  },
  agents: {
    title: "Agent",
    description: "为各 Agent CLI 生成指向本地网关的配置模板。",
  },
  skills: {
    title: "Skills",
    description:
      "集中查看和管理本机的 agent skills（~/.agents/skills 与插件目录）。",
  },
  playground: {
    title: "演练场",
    description: "选择模型后直接对话，实时查看流式输出。",
  },
};

async function copyToClipboard(value: string): Promise<boolean> {
  try {
    return await ClipboardSetText(value);
  } catch {
    try {
      await navigator.clipboard.writeText(value);
      return true;
    } catch {
      return false;
    }
  }
}
// applyTheme mirrors the saved preference onto <html>; HeroUI derives its
// palette from the dark class / data-theme attribute on the root element.
function applyTheme(theme: string) {
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.dataset.theme = theme;
}

export default function App() {
  const [page, setPage] = useState<Page>("overview");
  const [collapsed, setCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(300);
  const resizing = useRef(false);
  // 会话清空按钮在页面头部，但状态归 Playground 持有；它挂载时把清空回调
  // 注册到这里，头部按钮直接调用。
  const playgroundReset = useRef<(() => void) | null>(null);
  const [playgroundHasContent, setPlaygroundHasContent] = useState(false);
  const [data, setData] = useState<Bootstrap | null>(null);
  // Skills 页自取数据（文件系统扫描），这里只留一个用于侧栏的可用计数，
  // 面板每次 reload 后回填，保证开关/删除后数字跟着变。
  const [skillCount, setSkillCount] = useState(0);
  // Agent 页自取模板列表，侧栏数字单独拉一次（只用到 installed 标志）。
  const [agentCount, setAgentCount] = useState(0);
  const [requestLogs, setRequestLogs] = useState<RequestLogPage | null>(null);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logFilter, setLogFilter] = useState<RequestLogFilter>({
    token: "",
    model: "",
    provider: "",
    status: "",
    from: "",
    to: "",
  });
  const [proxyRunning, setProxyRunningState] = useState(true);
  const [togglingProxy, setTogglingProxy] = useState(false);
  const [editingProvider, setEditingProvider] = useState<
    Provider | null | undefined
  >(undefined);
  const [mappingDraft, setMappingDraft] = useState<
    { mapping: ModelMapping | null; initial?: MappingDraft } | undefined
  >(undefined);
  const [providerToDelete, setProviderToDelete] = useState<Provider | null>(
    null,
  );
  const [deletingProvider, setDeletingProvider] = useState(false);
  const [keyToDelete, setKeyToDelete] = useState<LocalAPIKey | null>(null);
  const [deletingKey, setDeletingKey] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  useEffect(() => {
    bootstrap().then(setData);
  }, []);
  useEffect(() => {
    listSkills()
      .then((s) => setSkillCount(s.skills.filter((x) => x.enabled).length))
      .catch(() => {});
  }, []);
  useEffect(() => {
    listToolTemplates()
      .then((tools) => setAgentCount(tools.filter((t) => t.installed).length))
      .catch(() => {});
  }, []);
  useEffect(() => {
    if (data) setProxyRunningState(data.proxyRunning);
  }, [data]);
  useEffect(() => {
    if (data) applyTheme(data.settings.theme);
  }, [data]);
  // 每次查询自增序号：查询条件变化时旧请求可能后返回，只有最新一次的结果
  // 允许落到状态里，否则面板会显示上一次筛选的数据。
  const logQuerySeq = useRef(0);
  const logSlowTimer = useRef<number | null>(null);
  const loadRequestLogs = useCallback(
    async (nextPage: number, nextFilter: RequestLogFilter) => {
      const seq = ++logQuerySeq.current;
      // 命中索引后这页通常几十毫秒就返回，立刻点亮遮罩只会闪一下。延迟到
      // 阈值之后才显示，快查询全程无遮罩，慢查询（大库、宽日期范围）照样有反馈。
      if (logSlowTimer.current !== null)
        window.clearTimeout(logSlowTimer.current);
      logSlowTimer.current = window.setTimeout(() => {
        if (seq === logQuerySeq.current) setLogsLoading(true);
      }, LOG_SLOW_MS);
      try {
        const result = await listRequestLogs(
          nextPage,
          requestLogPageSize,
          nextFilter,
        );
        if (seq !== logQuerySeq.current) return;
        setRequestLogs(result);
      } catch (error) {
        // 失败时保留上一次的结果，只解除遮罩；查询按钮仍可重试。
        if (seq === logQuerySeq.current)
          console.error("加载请求日志失败", error);
      } finally {
        // 被更新的一次查询取代时不要解锁，否则新查询的遮罩会被旧请求提前撤掉。
        if (seq === logQuerySeq.current) {
          if (logSlowTimer.current !== null) {
            window.clearTimeout(logSlowTimer.current);
            logSlowTimer.current = null;
          }
          setLogsLoading(false);
        }
      }
    },
    [],
  );
  useEffect(
    () => () => {
      if (logSlowTimer.current !== null)
        window.clearTimeout(logSlowTimer.current);
    },
    [],
  );
  useEffect(() => {
    if (page !== "logs") return;
    void loadRequestLogs(1, logFilter);
    // 只在切到日志页时取数：筛选条件由「查询」按钮显式提交，不随输入实时触发。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!resizing.current) return;
      e.preventDefault();
      setSidebarWidth(Math.min(480, Math.max(190, e.clientX)));
    };
    const onUp = () => {
      resizing.current = false;
      document.body.classList.remove("pane-resizing");
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("blur", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("blur", onUp);
    };
  }, []);
  // 自动派生每个可路由模型的映射；用户可在抽屉内改名/启停。
  // 已保存的映射与提供商模型自动派生合并：每个「提供商+上游模型」对优先取
  // 保存条目（含已停用的，用于显式压制自动映射），其余模型补自动映射，
  // 与后端 effectiveMappings 的语义一致。
  const ensuredMappings = useMemo(() => {
    const mappings = data?.mappings ?? [];
    const providers = data?.providers ?? [];
    const byRoute: Record<string, true> = {};
    for (const m of mappings) {
      byRoute[`${m.providerId}\u0000${m.upstreamModel}`] = true;
    }
    const merged = [...mappings];
    for (const auto of deriveAutoMappings(providers)) {
      if (byRoute[`${auto.providerId}\u0000${auto.upstreamModel}`]) continue;
      merged.push(auto);
    }
    // 与后端 effectiveMappings 同一套排序：同名的一条 failover 链按 ID 固定顺序，
    // 所以列表显示的顺位就是请求实际尝试的顺位。ID 比较用裸字符串序，与 Go 的
    // `<` 一致；localeCompare 对连字符与数字的处理不同，会让顺位与后端漂移。
    return merged.sort(
      (a, b) =>
        a.clientModel.localeCompare(b.clientModel) ||
        (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
    );
  }, [data?.mappings, data?.providers]);
  if (!data) return <main className="loading">正在加载 Agent Router…</main>;
  const setProvider = async (id: string, enabled: boolean) => {
    await toggleProvider(id, enabled);
    setData({
      ...data,
      providers: data.providers.map((p) =>
        p.id === id ? { ...p, enabled } : p,
      ),
    });
  };
  const removeProvider = async (provider: Provider) => {
    setDeletingProvider(true);
    try {
      await deleteProvider(provider.id);
      setData({
        ...data,
        providers: data.providers.filter((item) => item.id !== provider.id),
        mappings: data.mappings.filter(
          (mapping) => mapping.providerId !== provider.id,
        ),
      });
      setProviderToDelete(null);
      if (editingProvider?.id === provider.id) setEditingProvider(undefined);
    } finally {
      setDeletingProvider(false);
    }
  };
  const setLocalKey = async (id: string, enabled: boolean) => {
    await toggleLocalAPIKey(id, enabled);
    setData({
      ...data,
      apiKeys: data.apiKeys.map((k) => (k.id === id ? { ...k, enabled } : k)),
    });
  };
  const removeLocalKey = async (key: LocalAPIKey) => {
    setDeletingKey(true);
    try {
      await deleteLocalAPIKey(key.id);
      setData({
        ...data,
        apiKeys: data.apiKeys.filter((item) => item.id !== key.id),
      });
      setKeyToDelete(null);
    } finally {
      setDeletingKey(false);
    }
  };
  const createLocalKey = async (name: string) => {
    const saved = await saveLocalAPIKey({
      id: "",
      name: name.trim(),
      key: "",
      enabled: true,
      createdAt: "",
      updatedAt: "",
    });
    setData({
      ...data,
      apiKeys: [...data.apiKeys, saved].sort((a, b) =>
        a.name.localeCompare(b.name),
      ),
    });
    return saved;
  };
  const persistMapping = async (form: MappingFormState) => {
    let saved: ModelMapping;
    try {
      saved = await saveModelMapping({
        id: form.id || `mapping-${Date.now()}`,
        clientModel: form.clientModel.trim() || form.id,
        providerId: form.providerId,
        upstreamModel: form.upstreamModel,
        // 别名在抽屉里已裁掉空白；这里只去重并剔除与客户端模型名相同的项。
        aliases: form.aliases.filter(
          (name) => name && name !== (form.clientModel.trim() || form.id),
        ),
        // 编辑已有映射时保留其启停状态。
        enabled:
          data.mappings.find((item) => item.id === form.id)?.enabled ?? true,
      });
    } catch (err) {
      // 后端会拒绝重复路由；抛回去让抽屉显示原因，否则用户只看到「点了没反应」。
      throw new Error(err instanceof Error ? err.message : String(err));
    }
    setData({
      ...data,
      mappings: [
        ...data.mappings.filter((item) => item.id !== saved.id),
        saved,
      ].sort((a, b) => a.clientModel.localeCompare(b.clientModel)),
    });
  };
  // 链策略只影响「先打哪家」，不改变链的成员，所以本地只更新 chainModes 映射，
  // 不重排 mappings。写失败时把旧值放回去，避免 UI 显示已生效而网关没变。
  const setMappingChainMode = async (clientModel: string, mode: ChainMode) => {
    const previous = data.chainModes[clientModel] ?? "failover";
    const next = { ...data.chainModes };
    if (mode === "failover") delete next[clientModel];
    else next[clientModel] = mode;
    setData({ ...data, chainModes: next });
    try {
      await setChainMode(clientModel, mode);
    } catch (err) {
      const reverted = { ...next };
      if (previous === "failover") delete reverted[clientModel];
      else reverted[clientModel] = previous;
      setData({ ...data, chainModes: reverted });
      throw err;
    }
  };
  const persistProvider = async (form: ProviderFormState) => {
    const id =
      editingProvider?.id ??
      `provider-${
        form.name
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/(^-|-$)/g, "") || Date.now()
      }`;
    const saved = await saveProvider({
      id,
      name: form.name.trim(),
      kind: form.kind,
      baseUrl: form.baseUrl.trim(),
      apiKeyRef: editingProvider?.apiKeyRef ?? "",
      icon: form.icon || form.kind,
      modelPrefix: form.modelPrefix.trim(),
      enabled: editingProvider?.enabled ?? true,
      models: form.models
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
      availableModels: form.availableModels,
      credentialMode: editingProvider?.credentialMode ?? "session",
      updatedAt: editingProvider?.updatedAt ?? "",
    });
    // A new provider's key becomes its first pooled credential; for an existing
    // provider the drawer's field appends, so a typo cannot overwrite a working key.
    if (form.apiKey) {
      if (editingProvider) {
        await addProviderCredential(saved.id, "", form.apiKey);
      } else {
        await setProviderAPIKey(saved.id, form.apiKey);
      }
    }
    setData({
      ...data,
      providers: [
        ...data.providers.filter((item) => item.id !== saved.id),
        saved,
      ].sort((a, b) => a.name.localeCompare(b.name)),
    });
  };
  const currentPage = pageMeta[page];
  const toggleProxy = async (enabled: boolean) => {
    setTogglingProxy(true);
    try {
      await setProxyRunning(enabled);
      setProxyRunningState(enabled);
    } finally {
      setTogglingProxy(false);
    }
  };
  return (
    <div className="app-shell">
      <div className="titlebar-drag" aria-hidden="true" />
      <Sidebar
        page={page}
        setPage={setPage}
        collapsed={collapsed}
        onToggle={() => setCollapsed((value) => !value)}
        providerCount={data.providers.filter((p) => p.enabled).length}
        keyCount={data.apiKeys.filter((k) => k.enabled).length}
        mappingCount={ensuredMappings.filter((m) => m.enabled).length}
        agentCount={agentCount}
        skillCount={skillCount}
        proxyRunning={proxyRunning}
        width={sidebarWidth}
        onOpenSettings={() => setSettingsOpen(true)}
      />
      {!collapsed && (
        <div
          className="resize-handle"
          onMouseDown={(e) => {
            e.preventDefault();
            // 松手落在窗口外时 mouseup 会丢、分割线收不回去；补挂的
            // pointerup/blur 兜底清理（见下方 useEffect 的 onUp）。
            resizing.current = true;
            // body 类驱动拖动中分割线的显隐（见 styles.css 的 resize-handle::after）。
            document.body.classList.add("pane-resizing");
            document.body.style.cursor = "col-resize";
            document.body.style.userSelect = "none";
          }}
        />
      )}
      <main
        className={
          page === "logs"
            ? "content logs-page"
            : page === "playground"
              ? "content chat-page"
              : "content"
        }
      >
        <header>
          <div>
            <h1>{currentPage.title}</h1>
            <p className="subtitle">{currentPage.description}</p>
          </div>
          {page === "overview" && (
            <div className="proxy-toggle">
              <span
                className={proxyRunning ? "online-dot" : "online-dot off"}
              />
              <span>本地代理</span>
              <Switch
                size="sm"
                isSelected={proxyRunning}
                isDisabled={togglingProxy}
                onChange={toggleProxy}
                aria-label="代理运行状态"
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
            </div>
          )}
          {page === "providers" && (
            <Button
              size="sm"
              className="provider-add"
              onPress={() => setEditingProvider(null)}
            >
              <Plus size={16} />
              添加提供商
            </Button>
          )}
          {page === "playground" && (
            <button
              type="button"
              className="playground-clear"
              aria-label="清空对话"
              disabled={!playgroundHasContent}
              onClick={() => playgroundReset.current?.()}
            >
              <Trash2 size={16} />
            </button>
          )}
        </header>
        <div className="page-scroll">
          {page === "overview" ? (
            <Overview data={data} proxyRunning={proxyRunning} />
          ) : page === "usage" ? (
            <UsagePanel />
          ) : page === "providers" ? (
            <Providers
              providers={data.providers}
              onToggle={setProvider}
              onEdit={(provider) => setEditingProvider(provider)}
              onDelete={(provider) => setProviderToDelete(provider)}
            />
          ) : page === "mappings" ? (
            <Mappings
              providers={data.providers}
              mappings={ensuredMappings}
              onEdit={(m) => setMappingDraft({ mapping: m })}
              chainModes={data.chainModes}
              onChainModeChange={setMappingChainMode}
            />
          ) : page === "keys" ? (
            <LocalKeys
              keys={data.apiKeys}
              onToggle={setLocalKey}
              onCreate={createLocalKey}
              onDelete={(key) => setKeyToDelete(key)}
            />
          ) : page === "skills" ? (
            <SkillsPanel
              onSummary={(s) =>
                setSkillCount(s.skills.filter((x) => x.enabled).length)
              }
            />
          ) : page === "playground" ? (
            <Playground
              onRegisterReset={(reset) => {
                playgroundReset.current = reset;
              }}
              onHasContentChange={setPlaygroundHasContent}
              models={ensuredMappings.filter((m) => m.enabled)}
              providers={data.providers}
              keys={data.apiKeys}
              proxyRunning={proxyRunning}
            />
          ) : page === "logs" ? (
            <RequestLogs
              logs={requestLogs}
              loading={logsLoading}
              tokens={data.apiKeys}
              models={ensuredMappings}
              providers={data.providers}
              filter={logFilter}
              onFilterChange={setLogFilter}
              onSearch={() => void loadRequestLogs(1, logFilter)}
              onReset={() => {
                const empty = {
                  token: "",
                  model: "",
                  provider: "",
                  status: "",
                  from: "",
                  to: "",
                };
                setLogFilter(empty);
                void loadRequestLogs(1, empty);
              }}
              onPageChange={(nextPage) =>
                void loadRequestLogs(nextPage, logFilter)
              }
            />
          ) : (
            <AgentTemplates />
          )}
        </div>
      </main>
      <ProviderDrawer
        provider={editingProvider ?? null}
        isOpen={editingProvider !== undefined}
        onClose={() => setEditingProvider(undefined)}
        onSave={persistProvider}
      />
      <MappingDrawer
        providers={data.providers}
        mapping={mappingDraft?.mapping ?? null}
        initial={mappingDraft?.initial ?? null}
        isOpen={mappingDraft !== undefined}
        onClose={() => setMappingDraft(undefined)}
        onSave={persistMapping}
      />
      <SettingsModal
        isOpen={settingsOpen}
        settings={data.settings}
        onClose={() => setSettingsOpen(false)}
        onSaved={(next) => {
          setData({ ...data, settings: next });
          applyTheme(next.theme);
        }}
      />
      <Modal
        isOpen={providerToDelete !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen && !deletingProvider) setProviderToDelete(null);
        }}
      >
        <Modal.Backdrop>
          <Modal.Container size="sm">
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>删除提供商？</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  将删除“{providerToDelete?.name}”及其模型映射和本地 API
                  Key。此操作不可撤销。
                </p>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  size="sm"
                  variant="outline"
                  onPress={() => setProviderToDelete(null)}
                  isDisabled={deletingProvider}
                >
                  取消
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  isDisabled={deletingProvider}
                  onPress={() => {
                    if (providerToDelete) void removeProvider(providerToDelete);
                  }}
                >
                  {deletingProvider ? "删除中…" : "删除"}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
      <Modal
        isOpen={keyToDelete !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen && !deletingKey) setKeyToDelete(null);
        }}
      >
        <Modal.Backdrop>
          <Modal.Container size="sm">
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>删除本地密钥？</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  将删除“{keyToDelete?.name}
                  ”。使用该密钥的客户端将无法再访问本地网关。此操作不可撤销。
                </p>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  size="sm"
                  variant="outline"
                  onPress={() => setKeyToDelete(null)}
                  isDisabled={deletingKey}
                >
                  取消
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  isDisabled={deletingKey}
                  onPress={() => {
                    if (keyToDelete) void removeLocalKey(keyToDelete);
                  }}
                >
                  {deletingKey ? "删除中…" : "删除"}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
function requestPreview(body: string, field: "input" | "output") {
  if (!body) return "—";
  try {
    const parsed = JSON.parse(body);
    if (field === "input") {
      const messages = Array.isArray(parsed.messages) ? parsed.messages : [];
      return messages
        .map((message: { content?: unknown }) => {
          const content = message.content;
          return typeof content === "string"
            ? content
            : JSON.stringify(content);
        })
        .join(" ");
    }
    const content = parsed.choices?.[0]?.message?.content;
    return typeof content === "string"
      ? content
      : JSON.stringify(content ?? parsed);
  } catch {
    return body.replace(/\s+/g, " ");
  }
}

function formatLatency(latencyMs: number) {
  return latencyMs > 2000
    ? `${(latencyMs / 1000).toFixed(3)} s`
    : `${latencyMs} ms`;
}

function RequestLogs({
  logs,
  loading,
  tokens,
  models,
  providers,
  filter,
  onFilterChange,
  onSearch,
  onReset,
  onPageChange,
}: {
  logs: RequestLogPage | null;
  loading: boolean;
  tokens: LocalAPIKey[];
  models: ModelMapping[];
  providers: Provider[];
  filter: RequestLogFilter;
  onFilterChange: (filter: RequestLogFilter) => void;
  onSearch: () => void;
  onReset: () => void;
  onPageChange: (page: number) => void;
}) {
  const [colWidths, setColWidths] = useState(LOG_COLUMN_WIDTHS);
  const drag = useRef<{
    index: number;
    startX: number;
    startWidth: number;
  } | null>(null);
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const active = drag.current;
      if (!active) return;
      e.preventDefault();
      const width = Math.max(60, active.startWidth + e.clientX - active.startX);
      setColWidths((prev) =>
        prev.map((value, i) => (i === active.index ? width : value)),
      );
    };
    const onUp = () => {
      drag.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);
  const tableWidth = colWidths.reduce((sum, width) => sum + width, 0);
  const [selectedPayload, setSelectedPayload] = useState<{
    content: string;
    model: string;
    kind: "输入" | "输出";
  } | null>(null);
  // 详情弹窗先以列表里的 50 字符预览打开，再异步换成完整正文；这段时间要有
  // 反馈，否则大 body（单条可达数百 KB）拉取期间看起来像内容被截断了。
  const [payloadLoading, setPayloadLoading] = useState(false);
  // 详情也按序号守卫：连续点两行的眼睛时，先返回的旧请求不能覆盖新弹窗。
  const payloadSeq = useRef(0);
  const payloadSlowTimer = useRef<number | null>(null);
  const openPayload = (
    id: number,
    initial: string,
    model: string,
    kind: "输入" | "输出",
  ) => {
    const seq = ++payloadSeq.current;
    setSelectedPayload({ content: initial, model, kind });
    // 与列表查询同一套延迟门帘：加载条插在正文之上，快查询时立刻显示会造成
    // 一次布局跳动，慢查询（数百 KB body）才需要这条提示。
    if (payloadSlowTimer.current !== null)
      window.clearTimeout(payloadSlowTimer.current);
    payloadSlowTimer.current = window.setTimeout(() => {
      if (seq === payloadSeq.current) setPayloadLoading(true);
    }, LOG_SLOW_MS);
    void getRequestLog(id)
      .then((full: RequestLog) => {
        if (seq !== payloadSeq.current) return;
        setSelectedPayload({
          content:
            kind === "输入"
              ? full.requestBody
              : full.responseBody || full.errorMessage,
          model: full.clientModel,
          kind,
        });
      })
      .catch((error) => {
        // 预留在弹窗里的截断预览仍然可读，因此只解除 loading 不关闭弹窗。
        if (seq === payloadSeq.current) {
          setSelectedPayload({ content: initial, model, kind });
          console.error("加载日志详情失败", error);
        }
      })
      .finally(() => {
        if (seq !== payloadSeq.current) return;
        if (payloadSlowTimer.current !== null) {
          window.clearTimeout(payloadSlowTimer.current);
          payloadSlowTimer.current = null;
        }
        setPayloadLoading(false);
      });
  };
  // 请求日志按 client_model 过滤，同名多提供商时逐条映射会生成多个相同 value 的
  // 选项（ListBox 的 key 也重复）。名字背后是哪家服务的是日志行的事，筛选只认名字，
  // 所以取链首那条显示提供商即可。
  const filterModels = [
    ...new Map(models.map((m) => [m.clientModel, m])).values(),
  ];
  const [copied, setCopied] = useState(false);
  const headRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1400);
    return () => clearTimeout(timer);
  }, [copied]);
  // 组件卸载（切走日志页）时清掉在途的详情门帘，避免定时器在已卸载组件上触发。
  useEffect(
    () => () => {
      if (payloadSlowTimer.current !== null)
        window.clearTimeout(payloadSlowTimer.current);
    },
    [],
  );
  const copyPayload = async () => {
    if (!selectedPayload) return;
    if (await copyToClipboard(selectedPayload.content)) setCopied(true);
  };

  // 首次进入页面还没有任何结果可显示：整块用加载态占位，而不是先闪一屏空统计。
  if (!logs)
    return (
      <div
        className="loading request-log-loading"
        role="status"
        aria-live="polite"
      >
        <Loader2 size={16} className="spin" aria-hidden="true" />
        正在加载请求日志…
      </div>
    );
  // 统计口径跟随当前查询条件：后端把命中过滤的行聚合成 stats，前端只做展示。
  const stats = logs.stats;
  const successRate =
    stats.requests > 0 ? (stats.successes / stats.requests) * 100 : 0;
  const cacheRate =
    stats.inputTokens > 0
      ? (stats.cachedInputTokens / stats.inputTokens) * 100
      : 0;
  return (
    <section className="request-log-panel" aria-busy={loading}>
      <div
        className={
          loading ? "request-log-stats is-loading" : "request-log-stats"
        }
      >
        <div className="request-log-stat">
          <div className="request-log-stat-head">
            <span>输入 Tokens</span>
            <TokenValue value={stats.inputTokens} />
          </div>
          <small>
            缓存 <TokenValue value={stats.cachedInputTokens} as="span" /> ·
            缓存命中 {cacheRate.toFixed(1)}%
          </small>
        </div>
        <div className="request-log-stat">
          <div className="request-log-stat-head">
            <span>输出 Tokens</span>
            <TokenValue value={stats.outputTokens} />
          </div>
          <small>
            推理 <TokenValue value={stats.reasoningOutputTokens} as="span" />
          </small>
        </div>
        <div className="request-log-stat">
          <div className="request-log-stat-head">
            <span>总 Tokens</span>
            <TokenValue value={stats.inputTokens + stats.outputTokens} />
          </div>
          <small>
            {num.format(stats.cachedInputTokens)} 缓存 / 输入{" "}
            {num.format(stats.inputTokens)}
          </small>
        </div>
        <div className="request-log-stat">
          <div className="request-log-stat-head">
            <span>请求数</span>
            <strong>{num.format(stats.requests)}</strong>
          </div>
          <small>
            成功 {num.format(stats.successes)} · 成功率 {successRate.toFixed(1)}
            %
          </small>
        </div>
      </div>
      <div className="request-log-filters">
        <FieldSelect
          label="Token"
          placeholder="选择 Token"
          isClearable
          popoverClassName="request-log-select-popover"
          value={filter.token || null}
          onChange={(key) => onFilterChange({ ...filter, token: key ?? "" })}
          options={tokens.map((t) => ({ value: t.id, label: t.name }))}
        />
        <FieldSelect
          label="模型"
          placeholder="选择映射模型"
          isClearable
          popoverClassName="request-log-select-popover"
          value={filter.model || null}
          renderValue={(model) => model}
          onChange={(key) => onFilterChange({ ...filter, model: key ?? "" })}
          options={filterModels.map((m) => ({
            value: m.clientModel,
            label: (
              <span className="model-option">
                <span className="model-option-name">{m.clientModel}</span>
                <span className="model-option-provider">
                  {providers.find((p) => p.id === m.providerId)?.name ??
                    "未知提供商"}
                </span>
              </span>
            ),
          }))}
        />
        <FieldSelect
          label="提供商"
          placeholder="选择提供商"
          isClearable
          popoverClassName="request-log-select-popover"
          value={filter.provider || null}
          onChange={(key) => onFilterChange({ ...filter, provider: key ?? "" })}
          options={providers.map((p) => ({
            value: p.id,
            label: p.name,
          }))}
        />
        <FieldSelect
          label="状态"
          placeholder="选择状态"
          isClearable
          popoverClassName="request-log-select-popover"
          value={filter.status || null}
          onChange={(key) => onFilterChange({ ...filter, status: key ?? "" })}
          options={[
            { value: "success", label: "成功" },
            { value: "failed", label: "失败" },
          ]}
        />
        <DateRangeField
          from={filter.from}
          to={filter.to}
          onChange={(from, to) => onFilterChange({ ...filter, from, to })}
        />
        <Button
          size="sm"
          variant="primary"
          onPress={onSearch}
          isDisabled={loading}
        >
          {loading ? (
            <Loader2 size={14} className="spin" aria-hidden="true" />
          ) : null}
          查询
        </Button>
        <Button
          size="sm"
          variant="secondary"
          onPress={onReset}
          isDisabled={loading}
        >
          重置
        </Button>
      </div>
      <div className="request-log-card" aria-busy={loading}>
        {loading && (
          // 面板已有上一次的结果时不换成空态，而是盖一层遮罩：旧数据仍可滚动
          // 查阅（遮罩 pointer-events: none），遮罩只表达「这次查询还在进行」。
          <div className="request-log-overlay" role="status" aria-live="polite">
            <Loader2 size={16} className="spin" aria-hidden="true" />
            正在查询…
          </div>
        )}
        <div className="request-log-head" ref={headRef}>
          <div className="request-log-table-wrap" style={{ width: tableWidth }}>
            <table className="request-log-table">
              <colgroup>
                {colWidths.map((width, index) => (
                  <col key={LOG_COLUMNS[index]} style={{ width }} />
                ))}
              </colgroup>
              <thead>
                <tr>
                  {LOG_COLUMNS.map((label, index) => (
                    <th key={label}>
                      {label}
                      <span
                        className="request-log-col-resize"
                        role="separator"
                        aria-label={`调整${label}列宽`}
                        onMouseDown={(e) => {
                          e.preventDefault();
                          drag.current = {
                            index,
                            startX: e.clientX,
                            startWidth: colWidths[index],
                          };
                          document.body.style.cursor = "col-resize";
                          document.body.style.userSelect = "none";
                        }}
                      />
                    </th>
                  ))}
                </tr>
              </thead>
            </table>
          </div>
        </div>
        <div
          className="request-log-body"
          ref={bodyRef}
          onScroll={(e) => {
            if (headRef.current)
              headRef.current.scrollLeft = e.currentTarget.scrollLeft;
          }}
        >
          <div className="request-log-table-wrap" style={{ width: tableWidth }}>
            <table className="request-log-table">
              <colgroup>
                {colWidths.map((width, index) => (
                  <col key={LOG_COLUMNS[index]} style={{ width }} />
                ))}
              </colgroup>
              <tbody>
                {logs.items.map((log) => (
                  <tr key={log.id}>
                    <td>
                      <Chip
                        size="sm"
                        color={log.success ? "success" : "danger"}
                        variant="soft"
                      >
                        {log.success ? "成功" : "失败"}
                      </Chip>
                    </td>
                    <td className="request-log-compact-cell">
                      {log.tokenName || log.tokenId ? (
                        <Tooltip>
                          <Tooltip.Trigger>
                            <span title={log.tokenName || log.tokenId}>
                              {log.tokenName || log.tokenId}
                            </span>
                          </Tooltip.Trigger>
                          <Tooltip.Content>
                            {log.tokenName || log.tokenId}
                          </Tooltip.Content>
                        </Tooltip>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="request-log-compact-cell">
                      {log.userAgent ? (
                        <Tooltip>
                          <Tooltip.Trigger>
                            <span title={log.userAgent}>{log.userAgent}</span>
                          </Tooltip.Trigger>
                          <Tooltip.Content>{log.userAgent}</Tooltip.Content>
                        </Tooltip>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="request-log-compact-cell">
                      {log.clientModel ? (
                        <Tooltip>
                          <Tooltip.Trigger>
                            <span
                              title={
                                log.upstreamModel &&
                                log.upstreamModel !== log.clientModel
                                  ? `${log.clientModel} → ${log.upstreamModel}`
                                  : undefined
                              }
                            >
                              {log.clientModel}
                            </span>
                          </Tooltip.Trigger>
                          <Tooltip.Content>{log.clientModel}</Tooltip.Content>
                        </Tooltip>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="request-log-compact-cell">
                      {log.providerName || log.providerId ? (
                        <Tooltip>
                          <Tooltip.Trigger>
                            <span title={log.providerName || log.providerId}>
                              {log.providerName || log.providerId}
                            </span>
                          </Tooltip.Trigger>
                          <Tooltip.Content>
                            {log.providerName || log.providerId}
                          </Tooltip.Content>
                        </Tooltip>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="request-log-compact-cell">
                      {log.credentialMask || log.credentialName ? (
                        <Tooltip>
                          <Tooltip.Trigger>
                            <span
                              className="request-log-credential"
                              title={
                                log.credentialName
                                  ? `${log.credentialName} · ${log.credentialMask}`
                                  : log.credentialMask
                              }
                            >
                              {log.credentialMask || log.credentialName}
                            </span>
                          </Tooltip.Trigger>
                          <Tooltip.Content>
                            {log.credentialName
                              ? `${log.credentialName} · ${log.credentialMask}`
                              : log.credentialMask}
                          </Tooltip.Content>
                        </Tooltip>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="request-log-input-cell">
                      <div className="request-log-input">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="request-log-input-action"
                          aria-label="查看完整输入"
                          onPress={() =>
                            openPayload(
                              log.id,
                              log.requestBody,
                              log.clientModel,
                              "输入",
                            )
                          }
                        >
                          <Eye size={15} />
                        </Button>
                        <span className="request-log-input-preview">
                          {requestPreview(log.requestBody, "input")}
                        </span>
                      </div>
                    </td>
                    <td className="request-log-output-cell">
                      <div className="request-log-output">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="request-log-output-action"
                          aria-label="查看完整输出"
                          onPress={() =>
                            openPayload(
                              log.id,
                              log.responseBody || log.errorMessage,
                              log.clientModel,
                              "输出",
                            )
                          }
                        >
                          <Eye size={15} />
                        </Button>
                        <span className="request-log-output-preview">
                          {log.errorMessage ||
                            requestPreview(log.responseBody, "output")}
                        </span>
                      </div>
                    </td>
                    <td className="request-log-tokens-cell">
                      <Tooltip>
                        <Tooltip.Trigger>
                          <span className="request-log-tokens-value">
                            {num.format(log.inputTokens)} /{" "}
                            {num.format(log.outputTokens)}
                          </span>
                        </Tooltip.Trigger>
                        <Tooltip.Content>
                          输入 {num.format(log.inputTokens)} · 输出{" "}
                          {num.format(log.outputTokens)} · 缓存{" "}
                          {num.format(log.cachedInputTokens)} · 推理{" "}
                          {num.format(log.reasoningOutputTokens)}
                        </Tooltip.Content>
                      </Tooltip>
                    </td>
                    <td className="request-log-latency-cell">
                      {formatLatency(log.latencyMs)}
                    </td>
                    <td>{new Date(log.createdAt).toLocaleString()}</td>
                  </tr>
                ))}
                {logs.items.length === 0 && (
                  <tr>
                    <td
                      className="request-log-empty"
                      colSpan={LOG_COLUMNS.length}
                    >
                      暂无请求日志。通过本地代理发起调用后会显示在这里。
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>
      {logs.totalPages > 1 && (
        <div className="request-log-pagination">
          <span>
            第 {logs.page} / {logs.totalPages} 页
          </span>
          <Pagination size="sm" aria-label="请求日志分页">
            <Pagination.Content>
              <Pagination.Item>
                <Pagination.Previous
                  isDisabled={loading || logs.page <= 1}
                  onPress={() => onPageChange(logs.page - 1)}
                >
                  <Pagination.PreviousIcon />
                </Pagination.Previous>
              </Pagination.Item>
              {pageWindow(logs.page, logs.totalPages).map((n) =>
                n === null ? (
                  <Pagination.Item key={`ellipsis-${Math.random()}`}>
                    <Pagination.Ellipsis />
                  </Pagination.Item>
                ) : (
                  <Pagination.Item key={n}>
                    <Pagination.Link
                      isActive={n === logs.page}
                      isDisabled={loading}
                      onPress={() => onPageChange(n)}
                    >
                      {n}
                    </Pagination.Link>
                  </Pagination.Item>
                ),
              )}
              <Pagination.Item>
                <Pagination.Next
                  isDisabled={loading || logs.page >= logs.totalPages}
                  onPress={() => onPageChange(logs.page + 1)}
                >
                  <Pagination.NextIcon />
                </Pagination.Next>
              </Pagination.Item>
            </Pagination.Content>
          </Pagination>
        </div>
      )}
      <Modal
        isOpen={selectedPayload !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen) {
            // 作废在途的详情请求：否则它返回后会把已关闭的弹窗重新打开。
            payloadSeq.current += 1;
            if (payloadSlowTimer.current !== null) {
              window.clearTimeout(payloadSlowTimer.current);
              payloadSlowTimer.current = null;
            }
            setPayloadLoading(false);
            setSelectedPayload(null);
            setCopied(false);
          }
        }}
      >
        <Modal.Backdrop>
          <Modal.Container size="lg" scroll="inside">
            <Modal.Dialog className="request-log-dialog">
              <Modal.Header>
                <Modal.Heading>
                  完整{selectedPayload?.kind} · {selectedPayload?.model}
                </Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                {payloadLoading ? (
                  <div
                    className="request-log-detail-loading"
                    role="status"
                    aria-live="polite"
                  >
                    <Loader2 size={16} className="spin" aria-hidden="true" />
                    正在加载完整内容…
                  </div>
                ) : null}
                <pre className="request-log-json-view">
                  {formatJSON(selectedPayload?.content ?? "")}
                </pre>
              </Modal.Body>
              <Modal.Footer>
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      onPress={() => void copyPayload()}
                      aria-label={`复制完整${selectedPayload?.kind ?? ""}`}
                    >
                      <Copy size={15} />
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    {copied
                      ? "已复制"
                      : `复制完整${selectedPayload?.kind ?? ""}`}
                  </Tooltip.Content>
                </Tooltip>
                <Button
                  size="sm"
                  variant="secondary"
                  onPress={() => setSelectedPayload(null)}
                >
                  关闭
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </section>
  );
}
// 生成分页窗口：首尾固定，当前页附近展开，间断处用 null 表示省略号。
function pageWindow(page: number, total: number): (number | null)[] {
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
  const pages = new Set<number>([1, total]);
  for (let n = page - 1; n <= page + 1; n++) {
    if (n >= 2 && n <= total - 1) pages.add(n);
  }
  const sorted = [...pages].sort((a, b) => a - b);
  const out: (number | null)[] = [];
  sorted.forEach((n, i) => {
    if (i > 0 && n - sorted[i - 1] > 1) out.push(null);
    out.push(n);
  });
  return out;
}
function formatJSON(value: string): string {
  if (!value) return "暂无输出内容";
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}
// Backend returns model stats ordered by request count, so the panel keeps the
// most-used few instead of growing without bound as one-off models accumulate.
const MODEL_USAGE_LIMIT = 6;
function UsagePanel() {
  const [breakdown, setBreakdown] = useState<UsageBreakdown | null>(null);
  useEffect(() => {
    getUsageBreakdown().then(setBreakdown);
  }, []);
  if (!breakdown) return <div className="loading">正在加载使用情况…</div>;
  const totals = breakdown.providers.reduce(
    (acc, p) => ({
      requests: acc.requests + p.requests,
      input: acc.input + p.inputTokens,
      output: acc.output + p.outputTokens,
      cached: acc.cached + p.cachedInputTokens,
      reasoning: acc.reasoning + p.reasoningOutputTokens,
    }),
    { requests: 0, input: 0, output: 0, cached: 0, reasoning: 0 },
  );
  const totalTokens = totals.input + totals.output;
  const cacheRate = totals.input > 0 ? (totals.cached / totals.input) * 100 : 0;
  return (
    <section className="usage-panel">
      <div className="usage-token-cards">
        <Card className="metric">
          <Card.Content>
            <div className="usage-token-head">
              <span>输入 Tokens</span>
              <TokenValue value={totals.input} />
            </div>
            <small>
              缓存 <TokenValue value={totals.cached} as="span" /> · 缓存命中{" "}
              {cacheRate.toFixed(1)}%
            </small>
          </Card.Content>
        </Card>
        <Card className="metric">
          <Card.Content>
            <div className="usage-token-head">
              <span>输出 Tokens</span>
              <TokenValue value={totals.output} />
            </div>
            <small>
              推理 <TokenValue value={totals.reasoning} as="span" /> ·
              随响应生成 的 Token
            </small>
          </Card.Content>
        </Card>
        <Card className="metric">
          <Card.Content>
            <div className="usage-token-head">
              <span>总 Tokens</span>
              <TokenValue value={totalTokens} />
            </div>
            <small>{num.format(totals.requests)} 次请求</small>
          </Card.Content>
        </Card>
      </div>
      <div className="usage-row">
        <Card className="panel usage-panel-half">
          <Card.Content>
            <div className="panel-title">
              <div>
                <h2>提供商用量</h2>
                <p>各提供商请求与 Token 分布</p>
              </div>
            </div>
            {breakdown.providers.length === 0 ? (
              <p className="provider-model-empty">暂无用量数据</p>
            ) : (
              <UsageStatList
                stats={breakdown.providers}
                totalTokens={totalTokens}
              />
            )}
          </Card.Content>
        </Card>
        <Card className="panel usage-panel-half">
          <Card.Content>
            <div className="panel-title">
              <div>
                <h2>模型用量</h2>
                <p>各模型请求与 Token 分布</p>
              </div>
            </div>
            {breakdown.models.length === 0 ? (
              <p className="provider-model-empty">暂无用量数据</p>
            ) : (
              <UsageStatList
                stats={breakdown.models.slice(0, MODEL_USAGE_LIMIT)}
                totalTokens={totalTokens}
              />
            )}
          </Card.Content>
        </Card>
      </div>
      <Card className="panel">
        <Card.Content>
          <div className="panel-title">
            <div>
              <h2>上游 Key 用量</h2>
              <p>各上游 API Key 的请求与 Token 分布（按掩码显示）</p>
            </div>
          </div>
          {breakdown.credentials.length === 0 ? (
            <p className="provider-model-empty">暂无用量数据</p>
          ) : (
            <UsageStatList
              stats={breakdown.credentials}
              totalTokens={totalTokens}
            />
          )}
        </Card.Content>
      </Card>
      <Card className="panel">
        <Card.Content>
          <div className="panel-title">
            <div>
              <h2>密钥用量</h2>
              <p>各本地 API 密钥的请求与 Token 分布</p>
            </div>
          </div>
          {breakdown.keys.length === 0 ? (
            <p className="provider-model-empty">暂无用量数据</p>
          ) : (
            <UsageStatList stats={breakdown.keys} totalTokens={totalTokens} />
          )}
        </Card.Content>
      </Card>
    </section>
  );
}
function UsageStatList({
  stats,
  totalTokens,
}: {
  stats: UsageStat[];
  totalTokens: number;
}) {
  return (
    <div className="usage-stat-list">
      {stats.map((s) => {
        const tokens = s.inputTokens + s.outputTokens;
        const share = totalTokens ? (tokens / totalTokens) * 100 : 0;
        const successRate = s.requests ? (s.successes / s.requests) * 100 : 0;
        const cacheRate =
          s.inputTokens > 0 ? (s.cachedInputTokens / s.inputTokens) * 100 : 0;
        return (
          <div className="usage-stat" key={s.key}>
            <div className="usage-stat-top">
              <b className="usage-stat-name" title={s.key}>
                {s.mask || s.name || s.key}
              </b>
              {s.mask && s.name && (
                <span className="usage-stat-alias">{s.name}</span>
              )}
              <span className="usage-stat-tokens">
                <TokenValue value={tokens} as="span" />
                <span className="usage-stat-token-suffix">Tokens</span>
              </span>
            </div>
            <div
              className="usage-stat-bar"
              role="img"
              aria-label={`${s.key}: ${share.toFixed(0)}%`}
            >
              <span style={{ width: `${(tokens / totalTokens) * 100}%` }} />
            </div>
            <div className="usage-stat-meta">
              <span>
                {num.format(s.requests)} 次请求 · 成功率{" "}
                {successRate.toFixed(0)}%
              </span>
              <span>
                输出 <TokenValue value={s.outputTokens} as="span" /> ·{" "}
                <Tooltip>
                  <Tooltip.Trigger>
                    <span className="usage-token-value">
                      缓存命中 {cacheRate.toFixed(0)}%
                    </span>
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    缓存 {num.format(s.cachedInputTokens)} / 输入{" "}
                    {num.format(s.inputTokens)}
                  </Tooltip.Content>
                </Tooltip>
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
function RouteModelList({
  providerName,
  models,
}: {
  providerName: string;
  models: string[];
}) {
  const clipRef = useRef<HTMLDivElement>(null);
  const [isOverflowing, setIsOverflowing] = useState(false);

  useEffect(() => {
    const clip = clipRef.current;
    if (!clip) return;
    const checkOverflow = () =>
      setIsOverflowing(clip.scrollHeight > clip.clientHeight);
    checkOverflow();
    const observer = new ResizeObserver(checkOverflow);
    observer.observe(clip);
    return () => observer.disconnect();
  }, [models]);

  return (
    <Tooltip>
      <Tooltip.Trigger>
        <div className="route-model-list">
          <div className="route-model-list-clip" ref={clipRef}>
            <TagGroup
              size="sm"
              variant="surface"
              aria-label={`${providerName} 模型列表`}
            >
              <TagGroup.List>
                {models.map((model) => (
                  <Tag key={model} id={model}>
                    {model}
                  </Tag>
                ))}
              </TagGroup.List>
            </TagGroup>
          </div>
          {isOverflowing && (
            <span className="route-model-more" aria-hidden="true">
              ...
            </span>
          )}
        </div>
      </Tooltip.Trigger>
      <Tooltip.Content>{models.join("、")}</Tooltip.Content>
    </Tooltip>
  );
}

function Overview({
  data,
  proxyRunning,
}: {
  data: Bootstrap;
  proxyRunning: boolean;
}) {
  const baseURL = "http://127.0.0.1:9400/v1";
  const [baseURLCopied, setBaseURLCopied] = useState(false);
  const u = data.usage;
  const cacheRate =
    u.inputTokens > 0 ? (u.cachedInputTokens / u.inputTokens) * 100 : 0;
  const enabledProviders = data.providers.filter((p) => p.enabled);
  const firstRoutable = enabledProviders.find((p) => p.models.length > 0);
  const quickStartModel = firstRoutable
    ? (data.mappings.find(
        (m) =>
          m.providerId === firstRoutable.id &&
          m.upstreamModel === firstRoutable.models[0],
      )?.clientModel ??
      defaultClientModel(firstRoutable, firstRoutable.models[0]))
    : "";
  useEffect(() => {
    if (!baseURLCopied) return;
    const timer = setTimeout(() => setBaseURLCopied(false), 1400);
    return () => clearTimeout(timer);
  }, [baseURLCopied]);
  return (
    <>
      {baseURLCopied && (
        <span className="route-toast" role="status">
          已复制 BaseUrl
        </span>
      )}
      <section className="metric-grid">
        {(
          [
            // 副行不再写口径标注（"全部时间"），改为直接给出该卡片的标题。
            // 口径依据仍在：Summary() 对 usage_events 做全表聚合、无时间谓词，
            // 只是这句话不该由每张卡片各写一遍。
            ["请求总数", num.format(u.requests), Activity, "请求总数", "", ""],
            [
              "输入 Tokens",
              formatTokenCount(u.inputTokens),
              ArrowUpRight,
              // 只留裸比例：四个字删掉后这一格与其他三张卡的标题同为短标签，
              // 说明语义由 tooltip 承载（缓存量 / 输入量），不占行宽。
              `${cacheRate.toFixed(1)}%`,
              `缓存 ${num.format(u.cachedInputTokens)} / 输入 ${num.format(u.inputTokens)}`,
              // 裸比例挂在 DOM 里对读屏是无指代对象的数字，补一个不可见的名词。
              // 它不能并进 sub：sub 要参与宽度计算，多四个字就把断点顶回去。
              "缓存命中",
            ],
            [
              "输出 Tokens",
              formatTokenCount(u.outputTokens),
              ArrowDownRight,
              "输出 Tokens",
              `输出 ${num.format(u.outputTokens)}`,
              "",
            ],
            ["成功率", `${u.successRate.toFixed(2)}%`, Radio, "成功率", "", ""],
          ] as Array<[string, string, LucideIcon, string, string, string]>
        ).map(([label, value, Icon, sub, tooltip, srLabel]) => (
          <Card className="metric" key={label}>
            <Card.Content>
              {/* 图标换成数值后已无可见文字承载语义，标题必须留在 DOM 里；
                  但子标题本身已是卡片标题时（三张卡）不能再放一份，
                  否则读屏会把同一句话念两遍。
                  srLabel 是第四张卡的例外：它可见的副标题只剩 "96.2%"，
                  需要一个不可见的名词补上指代。 */}
              {sub !== label && (
                <span className="sr-only">{srLabel || label}</span>
              )}
              <div className="metric-head">
                <span className="metric-icon">
                  <Icon size={19} />
                </span>
                {tooltip ? (
                  <Tooltip>
                    <Tooltip.Trigger>
                      <strong className="metric-token-value">{value}</strong>
                    </Tooltip.Trigger>
                    <Tooltip.Content>{tooltip}</Tooltip.Content>
                  </Tooltip>
                ) : (
                  <strong>{value}</strong>
                )}
                {/* 窄窗口下这一行会被省略号截断（1280px 四列时约 46px 可用），
                    title 让截断的内容仍可在悬停时读到。 */}
                <small title={sub}>{sub}</small>
              </div>
            </Card.Content>
          </Card>
        ))}
      </section>
      <section className="two-col">
        <Card className="panel">
          <Card.Content>
            <div className="panel-title">
              <div>
                <h2>路由状态</h2>
                <p>当前可用的上游连接</p>
              </div>
              <Chip
                size="sm"
                color={proxyRunning ? "success" : "default"}
                variant="soft"
              >
                {proxyRunning ? "运行中" : "已停止"}
              </Chip>
            </div>
            <Separator />
            {enabledProviders.map((p) => (
              <div className="route-row" key={p.id}>
                <div>
                  <b className="route-provider-name">
                    <span
                      className={proxyRunning ? "status-dot" : "status-dot off"}
                    />
                    {p.name} · {p.models.length}个模型
                  </b>
                  <RouteModelList providerName={p.name} models={p.models} />
                </div>
                <Chip
                  size="sm"
                  color={proxyRunning ? "success" : "default"}
                  variant="soft"
                >
                  {proxyRunning ? "已启用" : "网关已停止"}
                </Chip>
              </div>
            ))}
          </Card.Content>
        </Card>
        <Card className="panel">
          <Card.Content>
            <div className="panel-title">
              <div>
                <h2>快速开始</h2>
                <p>将你的客户端接入本地网关</p>
              </div>
            </div>
            <div className="code-box">
              <span>BaseUrl</span>
              <div className="code-box-value">
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      className="code-box-copy"
                      onPress={() =>
                        void copyToClipboard(baseURL).then(setBaseURLCopied)
                      }
                      aria-label="复制 BaseUrl"
                    >
                      <Copy size={14} />
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    {baseURLCopied ? "已复制" : "复制 BaseUrl"}
                  </Tooltip.Content>
                </Tooltip>
                <code>{baseURL}</code>
              </div>
            </div>
            <div className="code-box">
              <span>Model</span>
              <code>{quickStartModel || "暂无可用模型"}</code>
            </div>
          </Card.Content>
        </Card>
      </section>
    </>
  );
}
function ProviderIcon({ provider }: { provider: Provider }) {
  const [failedIcon, setFailedIcon] = useState("");
  if (
    /^(data:image\/|https?:\/\/)/.test(provider.icon) &&
    failedIcon !== provider.icon
  ) {
    return (
      <span className="provider-symbol">
        <img
          src={provider.icon}
          alt=""
          width={22}
          height={22}
          className="object-contain"
          onError={() => setFailedIcon(provider.icon)}
        />
      </span>
    );
  }
  const Icon =
    provider.icon === "anthropic"
      ? BrainCircuit
      : provider.icon === "gemini"
        ? Sparkles
        : provider.icon === "openai"
          ? Orbit
          : provider.icon === "compatible"
            ? Globe2
            : Box;
  return (
    <span className={`provider-symbol ${provider.kind}`}>
      <Icon size={18} />
    </span>
  );
}
function Providers({
  providers,
  onToggle,
  onEdit,
  onDelete,
}: {
  providers: Provider[];
  onToggle: (id: string, e: boolean) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
}) {
  return (
    <section className="provider-list">
      <div className="provider-card-grid">
        {providers.map((p) => (
          <article className="provider-compact-card" key={p.id}>
            <div className="provider-card-top">
              <ProviderIcon provider={p} />
              <div className="provider-card-title">
                <b>{p.name}</b>
                <span>
                  {p.kind === "compatible" ? "OpenAI Compatible" : p.kind}
                </span>
              </div>
              <div className="provider-card-actions">
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      onPress={() => onEdit(p)}
                      aria-label={`编辑 ${p.name}`}
                    >
                      <Pencil size={15} />
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>编辑提供商</Tooltip.Content>
                </Tooltip>
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      onPress={() => onDelete(p)}
                      aria-label={`删除 ${p.name}`}
                    >
                      <Trash2 size={15} />
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>删除提供商</Tooltip.Content>
                </Tooltip>
              </div>
            </div>
            <code className="provider-card-url" title={p.baseUrl}>
              {p.baseUrl}
            </code>
            <div
              className={`provider-card-models${p.models.length > 6 ? " has-more" : ""}`}
              title={p.models.join(", ")}
            >
              {p.models.length ? (
                <TagGroup
                  size="sm"
                  variant="surface"
                  aria-label={`${p.name} 模型`}
                >
                  <TagGroup.List>
                    {p.models.map((model) => (
                      <Tag key={model} id={model}>
                        {model}
                      </Tag>
                    ))}
                  </TagGroup.List>
                </TagGroup>
              ) : (
                <span>尚未配置模型</span>
              )}
            </div>
            <div className="provider-card-footer">
              <Switch
                size="sm"
                isSelected={p.enabled}
                onChange={(e) => onToggle(p.id, e)}
                aria-label={`toggle ${p.name}`}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <span>{p.enabled ? "已启用" : "已停用"}</span>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
function deriveAutoMappings(providers: Provider[]): ModelMapping[] {
  const out: ModelMapping[] = [];
  for (const provider of providers.filter((p) => p.enabled)) {
    for (const model of provider.models) {
      out.push({
        id: `auto-${provider.id}-${model}`,
        clientModel: defaultClientModel(provider, model),
        providerId: provider.id,
        upstreamModel: model,
        aliases: [],
        enabled: true,
      });
    }
  }
  return out.sort((a, b) => a.clientModel.localeCompare(b.clientModel));
}
function Mappings({
  providers,
  mappings,
  onEdit,
  chainModes,
  onChainModeChange,
}: {
  providers: Provider[];
  mappings: ModelMapping[];
  onEdit: (mapping: ModelMapping) => void;
  chainModes: Record<string, ChainMode>;
  onChainModeChange: (clientModel: string, mode: ChainMode) => Promise<void>;
}) {
  const [copiedName, setCopiedName] = useState<string | null>(null);
  const [chainError, setChainError] = useState<string | null>(null);
  useEffect(() => {
    if (!copiedName) return;
    const timer = setTimeout(() => setCopiedName(null), 1400);
    return () => clearTimeout(timer);
  }, [copiedName]);
  const copy = async (id: string, value: string) => {
    const ok = await copyToClipboard(value);
    if (ok) setCopiedName(value);
  };
  // 启用提供商的激活模型构成可路由模型表。
  // 启用提供商的激活模型构成可路由模型表，按提供商分组（原生 details 折叠）。
  const groups = providers
    .filter((p) => p.enabled && p.models.length > 0)
    .map((provider) => ({
      provider,
      routes: provider.models.map((model) => ({
        model,
        mapping: mappings.find(
          (m) => m.providerId === provider.id && m.upstreamModel === model,
        ) ?? {
          id: `auto-${provider.id}-${model}`,
          clientModel: defaultClientModel(provider, model),
          providerId: provider.id,
          upstreamModel: model,
          aliases: [],
          enabled: true,
        },
      })),
    }));
  // 同名多提供商时，卡片要标出自己在 failover 链里的顺位，否则用户看不出
  // 两家提供商的同名模型是什么关系。顺位取自 mappings 的顺序（已按后端同一规则排）。
  const chainRank = new Map<string, { index: number; total: number }>();
  const chains = new Map<string, string[]>();
  for (const m of mappings) {
    if (!m.enabled) continue;
    const key = m.clientModel;
    chains.set(key, [...(chains.get(key) ?? []), m.id]);
  }
  for (const ids of chains.values()) {
    if (ids.length < 2) continue;
    ids.forEach((id, index) =>
      chainRank.set(id, { index: index + 1, total: ids.length }),
    );
  }
  const rankOf = (mapping: ModelMapping) => chainRank.get(mapping.id);
  // 链策略挂在链首卡片上：它描述整条链的起点规则，逐张卡片各放一个会让人以为
  // 每家可以单独设。非链首卡片只显示顺位。
  const chainModeOf = (mapping: ModelMapping): ChainMode =>
    chainModes[mapping.clientModel] ?? "failover";
  // 写链策略失败时给个可见的提示，否则下拉框会悄悄弹回原值。
  const changeChainMode = (clientModel: string, mode: ChainMode) => {
    onChainModeChange(clientModel, mode).catch((err) => {
      setChainError(err instanceof Error ? err.message : String(err));
    });
  };
  useEffect(() => {
    if (!chainError) return;
    const timer = setTimeout(() => setChainError(null), 4000);
    return () => clearTimeout(timer);
  }, [chainError]);
  return (
    <section className="list-panel">
      {copiedName && (
        <span className="route-toast" role="status">
          已复制 {copiedName}
        </span>
      )}
      {chainError && (
        <p className="mapping-save-error" role="alert">
          {chainError}
        </p>
      )}
      {groups.length > 0 ? (
        groups.map(({ provider, routes }) => (
          <details className="mapping-group" key={provider.id} open>
            <summary>
              <ProviderIcon provider={provider} />
              <b>{provider.name}</b>
              <span className="mapping-group-count">{routes.length}</span>
              <ChevronDown size={16} className="mapping-group-chevron" />
            </summary>
            <div className="route-grid">
              {routes.map(({ model, mapping }) => (
                <article className="route-card" key={`${provider.id}/${model}`}>
                  <div className="route-card-head">
                    <b
                      className="route-client-model"
                      title={mapping.clientModel}
                    >
                      {mapping.clientModel}
                    </b>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      className="icon-action"
                      onPress={() => copy(mapping.id, mapping.clientModel)}
                      aria-label={`复制 ${mapping.clientModel}`}
                    >
                      <Copy size={14} />
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      className="icon-action"
                      onPress={() => onEdit(mapping)}
                      aria-label={`编辑 ${mapping.clientModel}`}
                    >
                      <Pencil size={15} />
                    </Button>
                  </div>
                  <div className="route-card-body" title={model}>
                    <Link size={13} />
                    <code>{model}</code>
                  </div>
                  {/* 同名链顺位：只有多家提供商共用这个名字时才显示。 */}
                  {(() => {
                    const rank = rankOf(mapping);
                    if (!rank) return null;
                    return (
                      <div className="route-chain-row">
                        <span
                          className="route-chain-rank"
                          title={`同名路由 ${rank.index}/${rank.total}：请求按此顺序尝试，前一家限流或报错时自动转下一家`}
                        >
                          同名 {rank.index}/{rank.total}
                        </span>
                        {rank.index === 1 && (
                          <select
                            className="route-chain-mode"
                            aria-label={`${mapping.clientModel} 的调度策略`}
                            value={chainModeOf(mapping)}
                            onChange={(event) =>
                              changeChainMode(
                                mapping.clientModel,
                                event.target.value as ChainMode,
                              )
                            }
                          >
                            <option value="failover">故障转移</option>
                            <option value="round_robin">轮转</option>
                          </select>
                        )}
                      </div>
                    );
                  })()}
                  {/* 别名是另一条能命中这张卡片的客户端模型名，直接列出来，
                      否则用户只能进抽屉才知道自己配过哪些名字。 */}
                  {mapping.aliases.length > 0 && (
                    <div
                      className="route-card-aliases"
                      title={mapping.aliases.join("、")}
                    >
                      {mapping.aliases.map((alias) => (
                        <span className="route-alias" key={alias}>
                          {alias}
                        </span>
                      ))}
                    </div>
                  )}
                </article>
              ))}
            </div>
          </details>
        ))
      ) : (
        <p className="provider-model-empty">
          尚无启用的提供商模型。前往「提供商连接」启用提供商并勾选模型后，这里会自动列出全部可路由模型。
        </p>
      )}
    </section>
  );
}
function LocalKeys({
  keys,
  onToggle,
  onCreate,
  onDelete,
}: {
  keys: LocalAPIKey[];
  onToggle: (id: string, enabled: boolean) => void;
  onCreate: (name: string) => Promise<LocalAPIKey>;
  onDelete: (key: LocalAPIKey) => void;
}) {
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [justCreated, setJustCreated] = useState<LocalAPIKey | null>(null);
  const [envStatus, setEnvStatus] = useState<Record<string, EnvStatus | null>>(
    {},
  );
  const [envBusy, setEnvBusy] = useState<Record<string, boolean>>({});
  const [envToast, setEnvToast] = useState<string | null>(null);
  // 进入页面即为每个密钥检测一次 AGENT_ROUTER_API_KEY 是否已设置。
  useEffect(() => {
    let cancelled = false;
    for (const k of keys) {
      localAPIKeyEnvStatus(k.id).then((status) => {
        if (!cancelled)
          setEnvStatus((current) => ({ ...current, [k.id]: status }));
      });
    }
    return () => {
      cancelled = true;
    };
  }, [keys]);
  useEffect(() => {
    if (!copiedKey) return;
    const timer = setTimeout(() => setCopiedKey(null), 1400);
    return () => clearTimeout(timer);
  }, [copiedKey]);
  useEffect(() => {
    if (!envToast) return;
    const timer = setTimeout(() => setEnvToast(null), 3000);
    return () => clearTimeout(timer);
  }, [envToast]);
  const copy = async (value: string) => {
    const ok = await copyToClipboard(value);
    if (ok) setCopiedKey(value);
  };
  const exportEnv = async (k: LocalAPIKey) => {
    setEnvBusy((current) => ({ ...current, [k.id]: true }));
    try {
      const status = await exportLocalAPIKeyEnv(k.id);
      setEnvStatus((current) => ({ ...current, [k.id]: status }));
      if (status.error) {
        setEnvToast(`导出失败：${status.error}`);
        return;
      }
      setEnvToast(
        status.set && status.matches
          ? `已更新 ${status.varName}`
          : `已写入 ${status.profilePath || status.varName}`,
      );
    } catch (err) {
      setEnvToast(
        `导出失败：${err instanceof Error ? err.message : String(err)}`,
      );
    } finally {
      setEnvBusy((current) => ({ ...current, [k.id]: false }));
    }
  };
  const mask = (value: string) =>
    value.length <= 8
      ? "••••••••"
      : `${value.slice(0, 4)}••••${value.slice(-4)}`;
  const create = async () => {
    if (!name.trim()) return;
    setCreating(true);
    try {
      const saved = await onCreate(name);
      setName("");
      setJustCreated(saved);
      setRevealed((current) => ({ ...current, [saved.id]: true }));
    } finally {
      setCreating(false);
    }
  };
  const envSet = (k: LocalAPIKey): EnvStatus | null => envStatus[k.id] ?? null;
  return (
    <section className="list-panel">
      {envToast && (
        <span className="route-toast" role="status">
          {envToast}
        </span>
      )}
      {copiedKey && (
        <span className="route-toast" role="status">
          已复制密钥
        </span>
      )}
      <Card className="panel key-create-panel">
        <Card.Content>
          <div className="key-create-row">
            <TextField
              className="key-name-field"
              value={name}
              onChange={setName}
            >
              <Label htmlFor="local-key-name">密钥名称</Label>
              <Input id="local-key-name" placeholder="例如：本地开发" />
            </TextField>
            <Button
              size="sm"
              variant="primary"
              isDisabled={creating || !name.trim()}
              onPress={create}
            >
              {creating ? (
                "生成中…"
              ) : (
                <>
                  <Plus size={15} />
                  生成密钥
                </>
              )}
            </Button>
          </div>
          {justCreated && (
            <div className="key-created">
              <div>
                <b>已生成“{justCreated.name}”</b>
                <p>请立即复制保存，离开后仍可在列表中查看。</p>
              </div>
              <code>{justCreated.key}</code>
              <Button
                size="sm"
                variant="secondary"
                onPress={() => copy(justCreated.key)}
              >
                <Copy size={14} />
                复制
              </Button>
            </div>
          )}
        </Card.Content>
      </Card>
      <div className="provider-card-grid">
        {keys.map((k) => (
          <article className="provider-compact-card" key={k.id}>
            <div className="provider-card-top">
              <span className="provider-symbol">
                <KeyRound size={16} />
              </span>
              <div className="provider-card-title">
                <b>{k.name}</b>
                <span>{k.enabled ? "已启用" : "已停用"}</span>
              </div>
              <div className="provider-card-actions">
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      className="icon-action"
                      isDisabled={envBusy[k.id]}
                      onPress={() => void exportEnv(k)}
                      aria-label={`导出 ${k.name} 为环境变量`}
                    >
                      {envBusy[k.id] ? (
                        <Loader2 size={15} className="spin" />
                      ) : (
                        <Variable size={15} />
                      )}
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>设置环境变量</Tooltip.Content>
                </Tooltip>
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  className="icon-action"
                  onPress={() => copy(k.key)}
                  aria-label={`复制 ${k.name}`}
                >
                  <Copy size={14} />
                </Button>
                <Tooltip>
                  <Tooltip.Trigger>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="ghost"
                      onPress={() => onDelete(k)}
                      aria-label={`删除 ${k.name}`}
                    >
                      <Trash2 size={15} />
                    </Button>
                  </Tooltip.Trigger>
                  <Tooltip.Content>删除密钥</Tooltip.Content>
                </Tooltip>
              </div>
            </div>
            <div className="key-display">
              <code>{revealed[k.id] ? k.key : mask(k.key)}</code>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                className="icon-action"
                onPress={() =>
                  setRevealed((current) => ({
                    ...current,
                    [k.id]: !current[k.id],
                  }))
                }
                aria-label={
                  revealed[k.id] ? `隐藏 ${k.name}` : `查看 ${k.name}`
                }
              >
                {revealed[k.id] ? <EyeOff size={15} /> : <Eye size={15} />}
              </Button>
            </div>
            {envSet(k)?.set && (
              <div
                className={`key-env-row${envSet(k)?.matches ? " matches" : ""}`}
                title={envSet(k)!.varName}
              >
                <Variable size={12} />
                <span>
                  {envSet(k)!.matches
                    ? "已设置环境变量"
                    : `${envSet(k)!.varName} 已指向其它密钥`}
                </span>
              </div>
            )}
            <div className="provider-card-footer">
              <Switch
                size="sm"
                isSelected={k.enabled}
                onChange={(enabled) => onToggle(k.id, enabled)}
                aria-label={`toggle ${k.name}`}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <span>{k.enabled ? "已启用" : "已停用"}</span>
              <Chip
                size="sm"
                color={k.enabled ? "success" : "default"}
                variant="soft"
                className="key-status"
              >
                {k.enabled ? "生效中" : "已停用"}
              </Chip>
            </div>
          </article>
        ))}
      </div>
      {!keys.length && (
        <p className="provider-model-empty">
          尚无本地密钥。创建一个密钥后，客户端携带它即可访问本地网关。
        </p>
      )}
    </section>
  );
}
// 各工具官网 favicon：agent-router 支持的工具逐一映射，缺省回落到通用 Bot 图标。
const toolIcons: Record<string, string> = {
  opencode: opencodeIcon,
  pi: piIcon,
  mimocode: mimocodeIcon,
  claude: claudeIcon,
  omp: ompIcon,
};

function AgentTemplates() {
  const [previews, setPreviews] = useState<ToolPreview[] | null>(null);
  const [configuringId, setConfiguringId] = useState<string | null>(null);
  const [skillsToolId, setSkillsToolId] = useState<string | null>(null);
  // toolId -> 该 CLI 的 skills 目录里已有的条目数（含外部条目）。挂在卡片
  // 的 Skills 按钮上，链接弹窗开关时重算。
  const [skillCounts, setSkillCounts] = useState<Record<string, number>>({});
  const [configTab, setConfigTab] = useState("models");
  const [writingId, setWritingId] = useState<string | null>(null);
  const [written, setWritten] = useState<string | null>(null);
  // toolId -> slotKey -> modelId: explicit slot selections layered over the
  // catalog's automatic defaults ("" clears a slot).
  const [overrides, setOverrides] = useState<
    Record<string, Record<string, string>>
  >({});
  // toolId -> enabled model ids for multi-provider tools (default: all).
  const [modelOverrides, setModelOverrides] = useState<
    Record<string, string[]>
  >({});
  useEffect(() => {
    listToolTemplates().then(setPreviews);
  }, []);
  const skillsReady = previews !== null;
  useEffect(() => {
    if (!skillsReady) return;
    let stale = false;
    Promise.all(
      (previews ?? [])
        .filter((t) => t.skillsPath)
        .map(async (t) => {
          try {
            const d = await listSkillLinks(t.id);
            return [
              t.id,
              d.links.filter((l) => l.state !== "missing").length,
            ] as const;
          } catch {
            return [t.id, 0] as const;
          }
        }),
    ).then((entries) => {
      if (!stale) setSkillCounts(Object.fromEntries(entries));
    });
    return () => {
      stale = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skillsReady, skillsToolId]);
  useEffect(() => {
    if (!configuringId) return;
    document.documentElement.classList.add("agent-config-open");
    document.body.classList.add("agent-config-open");
    return () => {
      document.documentElement.classList.remove("agent-config-open");
      document.body.classList.remove("agent-config-open");
    };
  }, [configuringId]);
  if (!previews) return <div className="loading">正在生成配置模板…</div>;
  const effectiveSlots = (tool: ToolPreview): Record<string, string> => {
    const merged = { ...tool.slotModels, ...(overrides[tool.id] ?? {}) };
    return Object.fromEntries(
      Object.entries(merged).filter(([, v]) => v !== ""),
    );
  };
  // Default checked set comes from the backend: ai-sdk/pi list candidates so they
  // default to every routable model, while Codex's checklist chooses which
  // --profile files to generate and defaults to what is already on disk. Falling
  // back to "all" here would write dozens of files on first open.
  const selectedModels = (tool: ToolPreview): string[] =>
    modelOverrides[tool.id] ??
    tool.selectedModels ??
    tool.routable.map((m) => m.id);
  const applyRender = async (
    toolId: string,
    slotModels: Record<string, string>,
    modelIDs?: string[],
  ) => {
    try {
      const p = await renderToolTemplate(toolId, slotModels, modelIDs);
      setPreviews(
        (prev) =>
          prev?.map((t) =>
            t.id === toolId
              ? {
                  ...t,
                  content: p.content,
                  current: p.current,
                  exists: p.exists,
                  // profile 列表随勾选变化；漏掉它会让勾选后列表停在旧内容。
                  profiles: p.profiles ?? [],
                }
              : t,
          ) ?? prev,
      );
    } catch {
      // Keep the last rendered content; the selection still reflects intent.
    }
  };
  const setSlot = async (toolId: string, slot: string, modelId: string) => {
    const next = { ...(overrides[toolId] ?? {}), [slot]: modelId };
    setOverrides((o) => ({ ...o, [toolId]: next }));
    const tool = previews.find((t) => t.id === toolId);
    if (!tool) return;
    const merged = { ...tool.slotModels, ...next };
    const cleaned = Object.fromEntries(
      Object.entries(merged).filter(([, v]) => v !== ""),
    );
    await applyRender(toolId, cleaned);
  };
  const toggleModel = async (toolId: string, modelId: string, on: boolean) => {
    const tool = previews.find((t) => t.id === toolId);
    if (!tool) return;
    const base = selectedModels(tool);
    const next = on
      ? base.includes(modelId)
        ? base
        : [...base, modelId]
      : base.filter((id) => id !== modelId);
    setModelOverrides((o) => ({ ...o, [toolId]: next }));
    await applyRender(toolId, effectiveSlots(tool), next);
  };
  const write = async (id: string) => {
    setWritingId(id);
    setWritten(null);
    try {
      const tool = previews.find((t) => t.id === id);
      const slots = tool ? effectiveSlots(tool) : {};
      await writeToolTemplate(
        id,
        slots,
        tool ? selectedModels(tool) : undefined,
      );
      setWritten(tool?.configPath ?? id);
      setPreviews(
        (prev) =>
          prev?.map((t) =>
            t.id === id
              ? {
                  ...t,
                  current: t.content,
                  exists: true,
                  // The write just made these the on-disk assignment, so it
                  // becomes the new baseline: reopening the panel re-derives
                  // from it instead of the catalog fallback.
                  slotModels: slots,
                }
              : t,
          ) ?? prev,
      );
      // Drop the session overrides now that the baseline carries the choice.
      setOverrides((o) => {
        if (!(id in o)) return o;
        const next = { ...o };
        delete next[id];
        return next;
      });
    } finally {
      setWritingId(null);
    }
  };
  const changed = (tool: ToolPreview) =>
    !tool.exists || tool.current !== tool.content;
  const configuring = previews.find((t) => t.id === configuringId);
  const openConfig = (id: string) => {
    setConfigTab("models");
    setConfiguringId(id);
  };
  return (
    <section>
      {written && (
        <span className="route-toast" role="status">
          已写入 {written}
        </span>
      )}
      <div className="agent-card-grid">
        {previews.map((tool) => (
          <article className="agent-mini-card" key={tool.id}>
            <div className="agent-mini-head">
              <span className="agent-mini-icon">
                {toolIcons[tool.id] ? (
                  <img
                    src={toolIcons[tool.id]}
                    alt={tool.name}
                    width={17}
                    height={17}
                  />
                ) : (
                  <Bot size={17} />
                )}
              </span>
              <div className="agent-mini-title">
                <b>{tool.name}</b>
                <span className={tool.installed ? "tool-ok" : "tool-warn"}>
                  {tool.installed ? `${tool.cli} 已安装` : `${tool.cli} 未安装`}
                </span>
              </div>
              {changed(tool) && (
                <Chip size="sm" color="warning" variant="soft">
                  待同步
                </Chip>
              )}
            </div>
            <code className="agent-mini-path" title={tool.configPath}>
              {tool.configPath}
            </code>
            <div className="agent-mini-actions">
              <Button
                size="sm"
                variant="secondary"
                onPress={() => openConfig(tool.id)}
              >
                <GitCompare size={14} />
                配置
              </Button>
              {tool.skillsPath && (
                <Button
                  size="sm"
                  variant="secondary"
                  onPress={() => setSkillsToolId(tool.id)}
                >
                  <FolderSymlink size={14} />
                  Skills
                  {skillCounts[tool.id] > 0 && (
                    <span className="nav-count">{skillCounts[tool.id]}</span>
                  )}
                </Button>
              )}
            </div>
          </article>
        ))}
      </div>
      <Modal
        isOpen={configuringId !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen) setConfiguringId(null);
        }}
      >
        <Modal.Backdrop>
          <Modal.Container size="lg" scroll="outside">
            <Modal.Dialog className="agent-config-dialog">
              <Modal.Header>
                <Modal.Heading>配置 · {configuring?.name}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="diff-code-path">
                  <Folder size={13} />
                  <code>{configuring?.configPath}</code>
                </div>
                <Tabs
                  aria-label="Agent 配置"
                  selectedKey={configTab}
                  onSelectionChange={(key) => setConfigTab(String(key))}
                >
                  <Tabs.ListContainer>
                    <Tabs.List>
                      <Tabs.Tab id="models">
                        模型配置
                        <Tabs.Indicator />
                      </Tabs.Tab>
                      <Tabs.Tab id="diff">
                        配置对比
                        <Tabs.Indicator />
                      </Tabs.Tab>
                    </Tabs.List>
                  </Tabs.ListContainer>
                  <Tabs.Panel id="models">
                    <div className="agent-config-tab-content agent-model-tab">
                      {configuring &&
                        (configuring.modelSlots?.length ?? 0) > 0 && (
                          <div className="slot-grid">
                            {configuring.modelSlots.map((slot) => {
                              const value =
                                overrides[configuring.id]?.[slot.key] ??
                                configuring.slotModels[slot.key] ??
                                "";
                              return (
                                <FieldSelect
                                  key={slot.key}
                                  label={slot.label}
                                  placeholder="自动"
                                  isClearable
                                  value={value || null}
                                  onChange={(key) =>
                                    void setSlot(
                                      configuring.id,
                                      slot.key,
                                      key ?? "",
                                    )
                                  }
                                  options={configuring.routable.map((m) => ({
                                    value: m.id,
                                    label: m.name || m.id,
                                  }))}
                                />
                              );
                            })}
                          </div>
                        )}
                      {configuring && configuring.multiProvider && (
                        <div className="model-select-list">
                          {configuring.routable.map((m) => {
                            const checked = selectedModels(
                              configuring,
                            ).includes(m.id);
                            return (
                              <Checkbox
                                key={m.id}
                                isSelected={checked}
                                onChange={(on) =>
                                  void toggleModel(configuring.id, m.id, on)
                                }
                              >
                                <Checkbox.Content>
                                  <Checkbox.Control>
                                    <Checkbox.Indicator />
                                  </Checkbox.Control>
                                  {m.name || m.id}
                                </Checkbox.Content>
                              </Checkbox>
                            );
                          })}
                        </div>
                      )}
                      {configuring &&
                        (configuring.profiles?.length ?? 0) > 0 && (
                          <div className="profile-list">
                            <div className="profile-list-head">
                              同时生成 {configuring.profiles.length} 份
                              profile，用 <code>codex --profile</code> 切换模型
                            </div>
                            {configuring.profiles.map((p) => (
                              <div className="profile-row" key={p.name}>
                                <code className="profile-name">
                                  codex --profile {p.name}
                                </code>
                                <span className="profile-model">{p.model}</span>
                              </div>
                            ))}
                          </div>
                        )}
                    </div>
                  </Tabs.Panel>
                  <Tabs.Panel id="diff">
                    <div className="agent-config-tab-content agent-diff-tab">
                      {configuring && (
                        <div className="diff-split">
                          <div className="diff-pane">
                            <div className="diff-pane-label">
                              当前配置
                              {configuring.exists ? "" : "（文件不存在）"}
                            </div>
                            <pre className="diff-code">
                              {renderDiff(
                                configuring.current,
                                configuring.content,
                                "old",
                              )}
                            </pre>
                          </div>
                          <div className="diff-pane">
                            <div className="diff-pane-label">生成配置</div>
                            <pre className="diff-code">
                              {renderDiff(
                                configuring.current,
                                configuring.content,
                                "new",
                              )}
                            </pre>
                          </div>
                        </div>
                      )}
                    </div>
                  </Tabs.Panel>
                </Tabs>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  size="sm"
                  variant="secondary"
                  onPress={() => setConfiguringId(null)}
                >
                  关闭
                </Button>
                <Button
                  size="sm"
                  variant="primary"
                  isDisabled={writingId === configuring?.id}
                  onPress={() => {
                    if (!configuring) return;
                    void write(configuring.id).then(() =>
                      setConfiguringId(null),
                    );
                  }}
                >
                  {writingId === configuring?.id ? "写入中…" : "写入"}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
      <SkillsLinkModal
        toolId={skillsToolId ?? ""}
        toolName={previews.find((t) => t.id === skillsToolId)?.name ?? ""}
        isOpen={skillsToolId !== null}
        onClose={() => setSkillsToolId(null)}
      />
    </section>
  );
}

// renderDiff renders one side of a line-level diff using multiset matching:
// a line present (with remaining count) on the other side renders plain,
// otherwise it is highlighted as added (new side) or removed (old side).
function renderDiff(current: string, next: string, side: "old" | "new") {
  const ownLines = (side === "old" ? current : next).split("\n");
  const otherLines = (side === "old" ? next : current).split("\n");
  const counts = new Map<string, number>();
  for (const line of otherLines) counts.set(line, (counts.get(line) ?? 0) + 1);
  const kind = side === "old" ? "removed" : "added";
  return ownLines.map((line, index) => {
    const remaining = counts.get(line) ?? 0;
    counts.set(line, Math.max(0, remaining - 1));
    return (
      <span
        key={index}
        className={`diff-line ${remaining > 0 ? "diff-same" : `diff-${kind}`}`}
      >
        {line}
        {"\n"}
      </span>
    );
  });
}

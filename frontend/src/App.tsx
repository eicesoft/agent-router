import { useEffect, useRef, useState } from "react";
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
  Copy,
  Eye,
  EyeOff,
  Folder,
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
  listToolTemplates,
  localAPIKeyEnvStatus,
  renderToolTemplate,
  saveLocalAPIKey,
  saveModelMapping,
  saveProvider,
  setProviderAPIKey,
  setProxyRunning,
  toggleLocalAPIKey,
  toggleProvider,
  writeToolTemplate,
} from "./lib/api";
import type {
  Bootstrap,
  EnvStatus,
  LocalAPIKey,
  ModelMapping,
  Provider,
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

const num = new Intl.NumberFormat("en-US");
const compactNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 1,
});
function formatTokenCount(value: number) {
  if (value >= 10_000_000) return `${compactNum.format(value / 1_000_000)}M`;
  if (value >= 1_000) return `${compactNum.format(value / 1_000)}K`;
  return num.format(value);
}
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
export default function App() {
  const [page, setPage] = useState<Page>("overview");
  const [collapsed, setCollapsed] = useState(false);
  const [data, setData] = useState<Bootstrap | null>(null);
  const [requestLogs, setRequestLogs] = useState<RequestLogPage | null>(null);
  const [logFilter, setLogFilter] = useState<RequestLogFilter>({
    token: "",
    model: "",
    provider: "",
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
  useEffect(() => {
    bootstrap().then(setData);
  }, []);
  useEffect(() => {
    if (data) setProxyRunningState(data.proxyRunning);
  }, [data]);
  useEffect(() => {
    if (page !== "logs") return;
    listRequestLogs(1, requestLogPageSize, logFilter).then(setRequestLogs);
  }, [page]);
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
    const saved = await saveModelMapping({
      id: form.id || `mapping-${Date.now()}`,
      clientModel: form.clientModel.trim() || form.id,
      providerId: form.providerId,
      upstreamModel: form.upstreamModel,
      // 编辑已有映射时保留其启停状态。
      enabled:
        data.mappings.find((item) => item.id === form.id)?.enabled ?? true,
    });
    setData({
      ...data,
      mappings: [
        ...data.mappings.filter((item) => item.id !== saved.id),
        saved,
      ].sort((a, b) => a.clientModel.localeCompare(b.clientModel)),
    });
  };
  // 自动派生每个可路由模型的映射；用户可在抽屉内改名/启停。
  const ensuredMappings = data.mappings.length
    ? data.mappings
    : deriveAutoMappings(data.providers);
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
      updatedAt: editingProvider?.updatedAt ?? "",
    });
    if (form.apiKey) await setProviderAPIKey(saved.id, form.apiKey);
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
      <Sidebar
        page={page}
        setPage={setPage}
        collapsed={collapsed}
        onToggle={() => setCollapsed((value) => !value)}
        providerCount={data.providers.filter((p) => p.enabled).length}
        keyCount={data.apiKeys.filter((k) => k.enabled).length}
        mappingCount={ensuredMappings.filter((m) => m.enabled).length}
        proxyRunning={proxyRunning}
      />
      <main className={page === "logs" ? "content logs-page" : "content"}>
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
        </header>
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
          />
        ) : page === "keys" ? (
          <LocalKeys
            keys={data.apiKeys}
            onToggle={setLocalKey}
            onCreate={createLocalKey}
            onDelete={(key) => setKeyToDelete(key)}
          />
        ) : page === "logs" ? (
          <RequestLogs
            logs={requestLogs}
            tokens={data.apiKeys}
            models={ensuredMappings}
            providers={data.providers}
            filter={logFilter}
            onFilterChange={setLogFilter}
            onSearch={() => {
              void listRequestLogs(1, requestLogPageSize, logFilter).then(
                setRequestLogs,
              );
            }}
            onReset={() => {
              const empty = { token: "", model: "", provider: "" };
              setLogFilter(empty);
              void listRequestLogs(1, requestLogPageSize, empty).then(
                setRequestLogs,
              );
            }}
            onPageChange={(nextPage) => {
              void listRequestLogs(
                nextPage,
                requestLogPageSize,
                logFilter,
              ).then(setRequestLogs);
            }}
          />
        ) : (
          <AgentTemplates />
        )}
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
  tokens: LocalAPIKey[];
  models: ModelMapping[];
  providers: Provider[];
  filter: RequestLogFilter;
  onFilterChange: (filter: RequestLogFilter) => void;
  onSearch: () => void;
  onReset: () => void;
  onPageChange: (page: number) => void;
}) {
  const [selectedPayload, setSelectedPayload] = useState<{
    content: string;
    model: string;
    kind: "输入" | "输出";
  } | null>(null);
  const [copiedOutput, setCopiedOutput] = useState(false);
  const headRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!copiedOutput) return;
    const timer = setTimeout(() => setCopiedOutput(false), 1400);
    return () => clearTimeout(timer);
  }, [copiedOutput]);
  const copyOutput = async () => {
    if (selectedPayload?.kind !== "输出") return;
    if (await copyToClipboard(selectedPayload.content)) setCopiedOutput(true);
  };
  if (!logs) return <div className="loading">正在加载请求日志…</div>;
  return (
    <section className="request-log-panel">
      <div className="request-log-filters">
        <FieldSelect
          label="Token"
          placeholder="选择 Token"
          isClearable
          value={filter.token || null}
          onChange={(key) => onFilterChange({ ...filter, token: key ?? "" })}
          options={tokens.map((t) => ({ value: t.id, label: t.name }))}
        />
        <FieldSelect
          label="模型"
          placeholder="选择映射模型"
          isClearable
          value={filter.model || null}
          onChange={(key) => onFilterChange({ ...filter, model: key ?? "" })}
          options={models.map((m) => ({
            value: m.clientModel,
            label: m.clientModel,
          }))}
        />
        <FieldSelect
          label="提供商"
          placeholder="选择提供商"
          isClearable
          value={filter.provider || null}
          onChange={(key) => onFilterChange({ ...filter, provider: key ?? "" })}
          options={providers.map((p) => ({
            value: p.id,
            label: p.name,
          }))}
        />
        <Button size="sm" variant="primary" onPress={onSearch}>
          查询
        </Button>
        <Button size="sm" variant="secondary" onPress={onReset}>
          重置
        </Button>
      </div>
      <div className="request-log-card">
        <div className="request-log-head" ref={headRef}>
          <table className="request-log-table">
            <thead>
              <tr>
                <th>状态</th>
                <th>Token</th>
                <th>模型</th>
                <th>提供商</th>
                <th>输入</th>
                <th>输出</th>
                <th>Tokens</th>
                <th>耗时</th>
                <th>时间</th>
              </tr>
            </thead>
          </table>
        </div>
        <div
          className="request-log-body"
          ref={bodyRef}
          onScroll={(e) => {
            if (headRef.current)
              headRef.current.scrollLeft = e.currentTarget.scrollLeft;
          }}
        >
          <table className="request-log-table">
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
                  <td
                    className="request-log-compact-cell"
                    title={log.tokenName || log.tokenId}
                  >
                    <span>{log.tokenName || log.tokenId || "—"}</span>
                  </td>
                  <td
                    className="request-log-compact-cell"
                    title={log.clientModel}
                  >
                    <span>{log.clientModel}</span>
                  </td>
                  <td
                    className="request-log-compact-cell"
                    title={log.providerName || log.providerId}
                  >
                    {log.providerName || log.providerId}
                  </td>
                  <td className="request-log-input-cell">
                    <div className="request-log-input">
                      <Button
                        isIconOnly
                        size="sm"
                        variant="ghost"
                        className="request-log-input-action"
                        aria-label="查看完整输入"
                        onPress={() => {
                          setSelectedPayload({
                            content: log.requestBody,
                            model: log.clientModel,
                            kind: "输入",
                          });
                          void getRequestLog(log.id).then((full) =>
                            setSelectedPayload({
                              content: full.requestBody,
                              model: full.clientModel,
                              kind: "输入",
                            }),
                          );
                        }}
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
                        onPress={() => {
                          setSelectedPayload({
                            content: log.responseBody || log.errorMessage,
                            model: log.clientModel,
                            kind: "输出",
                          });
                          void getRequestLog(log.id).then((full) =>
                            setSelectedPayload({
                              content: full.responseBody || full.errorMessage,
                              model: full.clientModel,
                              kind: "输出",
                            }),
                          );
                        }}
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
                    {num.format(log.inputTokens)} /{" "}
                    {num.format(log.outputTokens)}
                    <span className="request-log-tokens-meta">
                      缓存 {num.format(log.cachedInputTokens)} · 推理{" "}
                      {num.format(log.reasoningOutputTokens)}
                    </span>
                  </td>
                  <td className="request-log-latency-cell">
                    {formatLatency(log.latencyMs)}
                  </td>
                  <td>{new Date(log.createdAt).toLocaleString()}</td>
                </tr>
              ))}
              {logs.items.length === 0 && (
                <tr>
                  <td className="request-log-empty" colSpan={9}>
                    暂无请求日志。通过本地代理发起调用后会显示在这里。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
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
                  isDisabled={logs.page <= 1}
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
                      onPress={() => onPageChange(n)}
                    >
                      {n}
                    </Pagination.Link>
                  </Pagination.Item>
                ),
              )}
              <Pagination.Item>
                <Pagination.Next
                  isDisabled={logs.page >= logs.totalPages}
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
            setSelectedPayload(null);
            setCopiedOutput(false);
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
                <pre className="request-log-json-view">
                  {formatJSON(selectedPayload?.content ?? "")}
                </pre>
              </Modal.Body>
              <Modal.Footer>
                {selectedPayload?.kind === "输出" && (
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="secondary"
                        onPress={() => void copyOutput()}
                        aria-label="复制完整输出"
                      >
                        <Copy size={15} />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>
                      {copiedOutput ? "已复制" : "复制完整输出"}
                    </Tooltip.Content>
                  </Tooltip>
                )}
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
              缓存 <TokenValue value={totals.cached} as="span" /> · 推理{" "}
              <TokenValue value={totals.reasoning} as="span" />
            </small>
          </Card.Content>
        </Card>
        <Card className="metric">
          <Card.Content>
            <div className="usage-token-head">
              <span>输出 Tokens</span>
              <TokenValue value={totals.output} />
            </div>
            <small>随响应生成的 Token</small>
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
                stats={breakdown.models}
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
        return (
          <div className="usage-stat" key={s.key}>
            <div className="usage-stat-top">
              <b className="usage-stat-name" title={s.key}>
                {s.name || s.key}
              </b>
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
              <span>{num.format(s.requests)} 次请求</span>
              <span>成功率 {successRate.toFixed(0)}%</span>
            </div>
          </div>
        );
      })}
    </div>
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
            ["请求总数", num.format(u.requests), Activity, "近 30 天", ""],
            [
              "输入 Tokens",
              `${formatTokenCount(u.cachedInputTokens)} / ${formatTokenCount(u.inputTokens)}`,
              ArrowUpRight,
              `${cacheRate.toFixed(1)}% 命中`,
              `缓存 ${num.format(u.cachedInputTokens)} / 输入 ${num.format(u.inputTokens)}`,
            ],
            [
              "输出 Tokens",
              formatTokenCount(u.outputTokens),
              ArrowDownRight,
              "近 30 天",
              `输出 ${num.format(u.outputTokens)}`,
            ],
            ["成功率", `${u.successRate.toFixed(2)}%`, Radio, "近 30 天", ""],
          ] as Array<[string, string, LucideIcon, string, string]>
        ).map(([label, value, Icon, sub, tooltip]) => (
          <Card className="metric" key={label}>
            <Card.Content>
              <div className="metric-icon">
                <Icon size={19} />
              </div>
              <span>{label}</span>
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
              <small>{sub}</small>
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
                  <TagGroup
                    size="sm"
                    variant="surface"
                    aria-label={`${p.name} 模型列表`}
                  >
                    <TagGroup.List>
                      {p.models.map((model) => (
                        <Tag key={model} id={model}>
                          {model}
                        </Tag>
                      ))}
                    </TagGroup.List>
                  </TagGroup>
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
              <code>{baseURL}</code>
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
            </div>
            <div className="code-box">
              <span>Model</span>
              <code>{quickStartModel || "暂无可用模型"}</code>
            </div>
            <Button size="sm" variant="primary" fullWidth>
              查看接入文档
            </Button>
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
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  className="row-edit"
                  onPress={() => onEdit(p)}
                  aria-label={`编辑 ${p.name}`}
                >
                  <Pencil size={15} />
                </Button>
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
}: {
  providers: Provider[];
  mappings: ModelMapping[];
  onEdit: (mapping: ModelMapping) => void;
}) {
  const [copiedName, setCopiedName] = useState<string | null>(null);
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
  const routes: { provider: Provider; model: string }[] = [];
  for (const provider of providers.filter((p) => p.enabled)) {
    for (const model of provider.models) routes.push({ provider, model });
  }
  return (
    <section className="list-panel">
      {copiedName && (
        <span className="route-toast" role="status">
          已复制 {copiedName}
        </span>
      )}
      {routes.length > 0 ? (
        <div className="route-grid">
          {routes.map(({ provider, model }) => {
            const mapping = mappings.find(
              (m) => m.providerId === provider.id && m.upstreamModel === model,
            ) ?? {
              id: `auto-${provider.id}-${model}`,
              clientModel: defaultClientModel(provider, model),
              providerId: provider.id,
              upstreamModel: model,
              enabled: true,
            };
            return (
              <article className="route-card" key={`${provider.id}/${model}`}>
                <div className="route-card-head">
                  <b className="route-client-model" title={mapping.clientModel}>
                    {mapping.clientModel}
                  </b>
                  <Button
                    isIconOnly
                    size="sm"
                    variant="ghost"
                    className="row-edit"
                    onPress={() => copy(mapping.id, mapping.clientModel)}
                    aria-label={`复制 ${mapping.clientModel}`}
                  >
                    <Copy size={14} />
                  </Button>
                  <Button
                    isIconOnly
                    size="sm"
                    variant="ghost"
                    className="row-edit"
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
              </article>
            );
          })}
        </div>
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
                      className="row-edit"
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
                  className="row-edit"
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
                className="row-edit"
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
  // Default for multi-provider tools: every routable model enabled.
  const selectedModels = (tool: ToolPreview): string[] =>
    modelOverrides[tool.id] ?? tool.routable.map((m) => m.id);
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
      await writeToolTemplate(
        id,
        tool ? effectiveSlots(tool) : {},
        tool ? selectedModels(tool) : undefined,
      );
      setWritten(tool?.configPath ?? id);
      setPreviews(
        (prev) =>
          prev?.map((t) =>
            t.id === id ? { ...t, current: t.content, exists: true } : t,
          ) ?? prev,
      );
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

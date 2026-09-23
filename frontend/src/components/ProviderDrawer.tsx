import { useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Checkbox,
  CheckboxGroup,
  Input,
  Label,
  TextField,
  Tooltip,
} from "@heroui/react";
import { RefreshCw, X } from "lucide-react";
import { fetchProviderIcon, fetchProviderModels } from "../lib/api";
import type { AvailableModel, Provider } from "../lib/types";
import catalog from "../../../backend/provider/catalog.json";
import { FieldSelect } from "./FieldSelect";
import { ProviderCredentials } from "./ProviderCredentials";

export type ProviderFormState = {
  icon: string;
  name: string;
  kind: string;
  baseUrl: string;
  modelPrefix: string;
  models: string;
  availableModels: AvailableModel[];
  apiKey: string;
};
const empty: ProviderFormState = {
  icon: "",
  name: "",
  kind: "compatible",
  baseUrl: "",
  modelPrefix: "",
  models: "",
  availableModels: [],
  apiKey: "",
};
const providerKinds = [
  { key: "openai", label: "OpenAI" },
  { key: "compatible", label: "OpenAI Compatible" },
  { key: "anthropic", label: "Anthropic" },
  { key: "gemini", label: "Gemini" },
];

function groupModels(models: AvailableModel[]) {
  const compareModels = (left: AvailableModel, right: AvailableModel) =>
    right.created - left.created || left.id.localeCompare(right.id);
  const groups = Object.entries(
    models.reduce<Record<string, AvailableModel[]>>((result, model) => {
      const prefix = model.id.includes("/")
        ? model.id.split("/", 1)[0]
        : model.id.split("-", 1)[0];
      (result[prefix || "其他"] ??= []).push(model);
      return result;
    }, {}),
  ).map(([prefix, entries]) => ({
    prefix,
    models: entries.sort(compareModels),
  }));
  return groups.sort(
    (left, right) =>
      compareModels(left.models[0], right.models[0]) ||
      left.prefix.localeCompare(right.prefix),
  );
}

function parseModelIds(raw: string): string[] {
  return Array.from(
    new Set(
      raw
        .split(",")
        .map((model) => model.trim())
        .filter(Boolean),
    ),
  );
}

function mergeCatalog(
  catalog: AvailableModel[],
  ids: string[],
): AvailableModel[] {
  const known = new Set(catalog.map((model) => model.id));
  const extras = ids.filter((id) => !known.has(id));
  return extras.length
    ? [...catalog, ...extras.map((id) => ({ id, created: 0 }))]
    : catalog;
}

// 手动填写的模型名必须同时进目录与勾选：勾选列表由目录渲染，名字不在
// 目录里的话，下一次勾选或刷新就会把它从 form.models 里冲掉。
function withManualModels(
  current: ProviderFormState,
  raw: string,
): ProviderFormState {
  const names = parseModelIds(raw);
  if (!names.length) return current;
  const selected = new Set(parseModelIds(current.models));
  for (const id of names) selected.add(id);
  return {
    ...current,
    availableModels: mergeCatalog(current.availableModels, names),
    models: Array.from(selected).join(", "),
  };
}

export function ProviderDrawer({
  provider,
  isOpen,
  onClose,
  onSave,
}: {
  provider: Provider | null;
  isOpen: boolean;
  onClose: () => void;
  onSave: (data: ProviderFormState) => Promise<void>;
}) {
  const initializing = useRef(false);
  const iconRequest = useRef(0);
  const [manualIcon, setManualIcon] = useState(false);
  const [formVersion, setFormVersion] = useState(0);
  const [form, setForm] = useState<ProviderFormState>(empty);
  const [iconStatus, setIconStatus] = useState("");
  const [loadingIcon, setLoadingIcon] = useState(false);
  const [iconRetry, setIconRetry] = useState(0);
  const [kindEdited, setKindEdited] = useState(false);
  const normalizedName = form.name.trim().toLowerCase();
  const presetName =
    (catalog.aliases as Record<string, string>)[normalizedName] ??
    normalizedName;
  const namePreset = catalog.providers.find(
    (item) =>
      item.id.toLowerCase() === presetName ||
      item.name.toLowerCase() === presetName,
  );
  const effectiveKind = !kindEdited && namePreset ? namePreset.kind : form.kind;
  const preset = catalog.providers.find((item) => item.kind === effectiveKind);
  const effectiveBaseURL = form.baseUrl.trim() || preset?.baseUrl || "";
  const usePresetIcon =
    !!preset &&
    (!form.baseUrl.trim() || form.baseUrl.trim() === preset.baseUrl);
  const custom =
    !provider || !catalog.providers.some((item) => item.id === provider.id);
  const [saving, setSaving] = useState(false);
  const [loadingModels, setLoadingModels] = useState(false);
  const [modelError, setModelError] = useState("");
  const [manualModelInput, setManualModelInput] = useState("");
  const change = (
    key: Exclude<keyof ProviderFormState, "availableModels">,
    value: string,
  ) => setForm((current) => ({ ...current, [key]: value }));
  const selectedModels = parseModelIds(form.models);
  const groupedModels = useMemo(
    () => groupModels(form.availableModels),
    [form.availableModels],
  );

  useEffect(() => {
    initializing.current = true;
    setKindEdited(!!provider);
    iconRequest.current += 1;
    setManualIcon(
      !!provider && /^(data:image\/|https?:\/\/)/.test(provider.icon),
    );
    setLoadingIcon(false);
    setIconStatus("");
    setFormVersion((value) => value + 1);
    setIconRetry(0);
    setModelError("");
    setManualModelInput("");
    setForm(
      provider
        ? {
            icon: provider.icon,
            name: provider.name,
            kind: provider.kind,
            baseUrl: provider.baseUrl,
            modelPrefix: provider.modelPrefix,
            models: provider.models.join(", "),
            // 已启用但不在目录里的模型（旧数据/手动加的）补进目录，
            // 保证「勾选 ⊆ 目录」，否则勾选一次就会把它们冲掉。
            availableModels: mergeCatalog(
              provider.availableModels,
              provider.models,
            ),
            apiKey: "",
          }
        : empty,
    );
  }, [provider, isOpen]);
  useEffect(() => {
    if (initializing.current) {
      initializing.current = false;
      return;
    }
    if (!isOpen || !custom || manualIcon) return;
    const request = ++iconRequest.current;
    let active = true;
    setLoadingIcon(false);
    setIconStatus("");
    if (
      provider?.baseUrl === form.baseUrl &&
      provider.icon.startsWith("data:image/") &&
      iconRetry === 0
    )
      return;
    if (usePresetIcon && preset) {
      setForm((current) => ({ ...current, icon: preset.icon }));
      setIconStatus(`使用 ${preset.name} 模板图标`);
      return;
    }
    setForm((current) => ({ ...current, icon: "" }));
    try {
      const url = new URL(effectiveBaseURL);
      if (!["http:", "https:"].includes(url.protocol) || !url.hostname) return;
    } catch {
      return;
    }
    setLoadingIcon(true);
    const timer = window.setTimeout(async () => {
      try {
        const icon = await fetchProviderIcon(effectiveBaseURL);
        if (active && request === iconRequest.current) {
          setForm((current) => ({ ...current, icon }));
          setIconStatus("图标已加载，保存提供商时一并保存");
        }
      } catch (error) {
        if (active && request === iconRequest.current)
          setIconStatus(String(error));
      } finally {
        if (active && request === iconRequest.current) setLoadingIcon(false);
      }
    }, 600);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [
    form.baseUrl,
    effectiveBaseURL,
    usePresetIcon,
    preset,
    isOpen,
    custom,
    provider,
    iconRetry,
    formVersion,
    manualIcon,
  ]);
  const invalidIconURL =
    manualIcon &&
    !!form.icon &&
    !form.icon.startsWith("data:image/") &&
    (() => {
      try {
        const url = new URL(form.icon);
        return (
          !["http:", "https:"].includes(url.protocol) ||
          !url.hostname ||
          !!url.username ||
          !!url.password
        );
      } catch {
        return true;
      }
    })();
  if (!isOpen) return null;

  const submit = async () => {
    setSaving(true);
    try {
      // 输入框里没点「添加」的内容也随本次保存生效，直接保存不丢。
      const payload = withManualModels(form, manualModelInput);
      await onSave({
        ...payload,
        baseUrl: effectiveBaseURL,
        kind: effectiveKind,
      });
      onClose();
    } finally {
      setSaving(false);
    }
  };
  const addManualModels = () => {
    if (!manualModelInput.trim()) return;
    setForm((current) => withManualModels(current, manualModelInput));
    setManualModelInput("");
  };
  const loadModels = async () => {
    if (!effectiveBaseURL || (!provider && !form.apiKey)) {
      setModelError("请先填写 Base URL 和 API Key。");
      return;
    }
    setLoadingModels(true);
    setModelError("");
    try {
      const models = await fetchProviderModels(
        provider?.id ?? "",
        effectiveKind,
        effectiveBaseURL,
        form.apiKey,
      );
      setForm((current) => ({
        ...current,
        // 刷新只扩充目录并保留勾选：手动加过、接口没返回的模型不丢。
        availableModels: mergeCatalog(models, parseModelIds(current.models)),
      }));
      if (!models.length) setModelError("接口未返回可用模型。");
    } catch (error) {
      setModelError(
        error instanceof Error
          ? error.message
          : "获取模型失败，请检查连接与 API Key。",
      );
    } finally {
      setLoadingModels(false);
    }
  };

  return (
    <div
      className="provider-drawer-overlay"
      role="presentation"
      onMouseDown={onClose}
    >
      <aside
        className="provider-drawer"
        role="dialog"
        aria-modal="true"
        aria-label={provider ? "编辑提供商" : "添加提供商"}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="provider-drawer-header">
          <div>
            <h2>{provider ? "编辑提供商" : "添加提供商"}</h2>
            <p>
              {provider ? "更新连接与模型配置" : "连接任意 OpenAI 兼容服务"}
            </p>
          </div>
          <Button
            isIconOnly
            variant="ghost"
            size="sm"
            onPress={onClose}
            aria-label="关闭"
          >
            <X size={17} />
          </Button>
        </div>
        <div className="provider-form">
          <TextField
            autoFocus
            value={form.name}
            onChange={(value) => change("name", value)}
            aria-label="名称"
          >
            <Label>名称</Label>
            <Input placeholder="例如：OpenRouter" />
          </TextField>
          <FieldSelect
            label="接口类型"
            value={effectiveKind}
            onChange={(key) => {
              if (key) {
                setKindEdited(true);
                change("kind", key);
              }
            }}
            options={providerKinds.map((kind) => ({
              value: kind.key,
              label: kind.label,
            }))}
          />
          <TextField
            value={form.baseUrl}
            onChange={(value) => change("baseUrl", value)}
            aria-label="Base URL"
          >
            <Label>Base URL</Label>
            <Input
              placeholder={preset?.baseUrl ?? "https://api.example.com/v1"}
            />
            {preset && (
              <p className="provider-form-note">
                留空使用 {preset.name} 模板：{preset.baseUrl}
              </p>
            )}
          </TextField>
          {custom && (
            <div className="flex flex-col gap-2">
              <TextField
                value={
                  /^https?:\/\//.test(form.icon) ||
                  (manualIcon && !form.icon.startsWith("data:image/"))
                    ? form.icon
                    : ""
                }
                onChange={(value) => {
                  iconRequest.current += 1;
                  setManualIcon(true);
                  setLoadingIcon(false);
                  setIconStatus("");
                  change("icon", value.trim());
                }}
                aria-label="图标地址"
                isInvalid={invalidIconURL}
              >
                <Label>图标地址</Label>
                <Input placeholder="https://example.com/icon.png" />
              </TextField>
              <div className="flex items-center gap-2" aria-live="polite">
                {/^(data:image\/|https?:\/\/)/.test(form.icon) &&
                  !invalidIconURL && (
                    <img
                      key={form.icon}
                      src={form.icon}
                      alt="提供商图标"
                      width={24}
                      height={24}
                      className="object-contain"
                      onLoad={() => {
                        if (manualIcon)
                          setIconStatus("图标已加载，保存时保留当前配置");
                      }}
                      onError={() =>
                        setIconStatus(
                          "图标暂时无法加载，请检查地址；仍可保存该地址",
                        )
                      }
                    />
                  )}
                <p className="provider-form-note">
                  {invalidIconURL
                    ? "请输入有效的 HTTP(S) 图标地址"
                    : loadingIcon
                      ? "正在加载网站图标…"
                      : iconStatus || "自动获取不到时，可输入自定义图标地址"}
                </p>
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  aria-label="重新获取网站图标"
                  isDisabled={loadingIcon || !effectiveBaseURL}
                  onPress={() => {
                    setManualIcon(false);
                    setIconRetry((value) => value + 1);
                  }}
                >
                  <RefreshCw size={15} />
                </Button>
              </div>
            </div>
          )}
          <TextField
            value={form.apiKey}
            onChange={(value) => change("apiKey", value)}
            aria-label="API Key"
          >
            <Label>{provider ? "新增 API Key" : "API Key"}</Label>
            <Input
              type="password"
              placeholder={provider ? "填写后追加到 Key 池" : "sk-..."}
            />
            {provider && (
              <p className="provider-form-note">
                已有 Key 不会被覆盖；留空则不变
              </p>
            )}
          </TextField>
          {provider && <ProviderCredentials providerId={provider.id} />}
          <TextField
            value={form.modelPrefix}
            onChange={(value) => change("modelPrefix", value)}
            aria-label="模型前缀"
          >
            <Label>模型前缀</Label>
            <Input placeholder="可选，如 openai" />
            <p className="provider-form-note">
              用于自动生成客户端模型名，如 openai/gpt-4o
            </p>
          </TextField>
          <section
            className="provider-model-selector"
            aria-labelledby="provider-models-heading"
          >
            <div className="provider-model-selector-header">
              <div>
                <b id="provider-models-heading">模型配置</b>
                <p>目录会随提供商一并保存。</p>
              </div>
              <Tooltip>
                <Tooltip.Trigger>
                  <Button
                    isIconOnly
                    size="sm"
                    variant="ghost"
                    isDisabled={loadingModels}
                    onPress={loadModels}
                    aria-label="重新获取模型"
                  >
                    {loadingModels ? (
                      <RefreshCw size={15} className="provider-refresh-spin" />
                    ) : (
                      <RefreshCw size={15} />
                    )}
                  </Button>
                </Tooltip.Trigger>
                <Tooltip.Content>重新获取模型</Tooltip.Content>
              </Tooltip>
            </div>
            {modelError && <p className="provider-model-error">{modelError}</p>}
            {groupedModels.length > 0 && (
              <div className="provider-model-scroll">
                <CheckboxGroup
                  className="provider-model-checkboxes"
                  aria-label="选择启用的模型"
                  value={selectedModels}
                  onChange={(models) => change("models", models.join(", "))}
                >
                  <div className="provider-model-options">
                    {groupedModels.map(({ prefix, models }) => (
                      <div className="provider-model-group" key={prefix}>
                        <b>{prefix}</b>
                        {models.map((model) => (
                          <Tooltip key={model.id}>
                            <Tooltip.Trigger className="provider-model-trigger">
                              <Checkbox value={model.id}>
                                <Checkbox.Content className="provider-model-option">
                                  <Checkbox.Control>
                                    <Checkbox.Indicator />
                                  </Checkbox.Control>
                                  <span className="provider-model-name">
                                    {model.id}
                                  </span>
                                </Checkbox.Content>
                              </Checkbox>
                            </Tooltip.Trigger>
                            <Tooltip.Content className="provider-model-tooltip">
                              {model.id}
                            </Tooltip.Content>
                          </Tooltip>
                        ))}
                      </div>
                    ))}
                  </div>
                </CheckboxGroup>
              </div>
            )}
            {!groupedModels.length && !modelError && (
              <p className="provider-model-empty">
                点击刷新图标获取模型，或在下方手动添加；首次获取默认不启用。
              </p>
            )}
            {/* 手动添加行始终可见：上游没有 /models 接口时目录永远拉不到，
                若只在目录为空时才给输入框，保存过一次就再也加不了新模型。 */}
            <div className="provider-model-manual">
              <TextField
                value={manualModelInput}
                onChange={setManualModelInput}
                aria-label="手动添加模型"
              >
                <Label>手动添加</Label>
                <Input
                  placeholder="模型名，多个用逗号分隔"
                  onKeyDown={(event) => {
                    if (event.key !== "Enter") return;
                    // 这个输入框不提交表单：回车只把模型名收进目录。
                    event.preventDefault();
                    addManualModels();
                  }}
                />
              </TextField>
              <Button
                size="sm"
                variant="outline"
                isDisabled={!manualModelInput.trim()}
                onPress={addManualModels}
              >
                添加
              </Button>
            </div>
          </section>
        </div>
        <div className="provider-drawer-footer">
          <Button size="sm" variant="outline" onPress={onClose}>
            取消
          </Button>
          <Button
            size="sm"
            variant="primary"
            isDisabled={
              saving ||
              loadingIcon ||
              invalidIconURL ||
              !form.name ||
              !effectiveBaseURL
            }
            onPress={submit}
          >
            {provider ? "保存修改" : "添加提供商"}
          </Button>
        </div>
      </aside>
    </div>
  );
}

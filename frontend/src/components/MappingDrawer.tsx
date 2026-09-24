import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Input,
  Label,
  Popover,
  Separator,
  Tag,
  TagGroup,
  TextField,
  Tooltip,
} from "@heroui/react";
import { Plus, X, Zap } from "lucide-react";
import { formatTokenCount } from "../lib/format";
import { defaultClientModel } from "../lib/naming";
import type { DevModel, ModelMapping, Provider } from "../lib/types";
import { DevModelModal } from "./DevModelModal";
import { FieldSelect } from "./FieldSelect";

export type MappingFormState = {
  id: string;
  clientModel: string;
  providerId: string;
  upstreamModel: string;
  aliases: string[];
  // 模型元数据：输入模态与容量（tokens，0 = 未设置），仅存储与展示。
  inputTypes: string[];
  inputContextSize: number;
  outputSize: number;
  // 单价（每百万 tokens），默认 0 表示未设置；仅存储与展示。
  inputPrice: number;
  outputPrice: number;
  cacheReadPrice: number;
};

// 输入模态的五种标准取值（OpenRouter / LiteLLM 通用分类）。
const INPUT_TYPE_OPTIONS = [
  { value: "text", label: "text", hint: "文本" },
  { value: "image", label: "image", hint: "图像" },
  { value: "audio", label: "audio", hint: "音频" },
  { value: "video", label: "video", hint: "视频" },
  { value: "file", label: "file", hint: "文件 / PDF" },
] as const;

// models.dev 的 input 模态 → 本表单的 inputTypes（pdf 即 file），未知值丢弃。
function toInputTypes(modalities: string[]): string[] {
  const allowed: string[] = INPUT_TYPE_OPTIONS.map((option) => option.value);
  const out: string[] = [];
  for (const raw of modalities) {
    const value = raw === "pdf" ? "file" : raw;
    if (allowed.includes(value) && !out.includes(value)) out.push(value);
  }
  return out;
}

// 快捷档位：K、M 按 1000 进制换算（128K = 128000），覆盖常见上下文与输出规格。
const SIZE_PRESETS = [
  1_000_000, 512_000, 256_000, 200_000, 128_000, 64_000, 32_000, 16_000, 8_000,
];

// 数值输入的统一解析：清空与非法输入一律归 0（0 = 未设置），不允许负数。
const parseSize = (value: string) => Math.max(0, Number(value) || 0);
// 价格允许小数（如 0.0025），其余口径与 parseSize 一致。
const parsePrice = (value: string) => Math.max(0, Number(value) || 0);

// 容量输入框后面的快捷按钮：弹出常用档位，点选即写入并收起弹层。
function SizeQuickSet({ onPick }: { onPick: (value: number) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover isOpen={open} onOpenChange={setOpen}>
      <Popover.Trigger>
        <Button
          size="sm"
          variant="outline"
          isIconOnly
          aria-label="快捷设置档位"
        >
          <Zap size={14} />
        </Button>
      </Popover.Trigger>
      <Popover.Content>
        <Popover.Dialog>
          <div className="mapping-size-presets">
            {SIZE_PRESETS.map((value) => (
              <Button
                key={value}
                size="sm"
                variant="ghost"
                onPress={() => {
                  onPick(value);
                  setOpen(false);
                }}
              >
                {formatTokenCount(value)}
              </Button>
            ))}
          </div>
        </Popover.Dialog>
      </Popover.Content>
    </Popover>
  );
}

export type MappingDraft = Pick<
  MappingFormState,
  "providerId" | "upstreamModel" | "clientModel"
>;

export function MappingDrawer({
  providers,
  mapping,
  initial,
  isOpen,
  onClose,
  onSave,
}: {
  providers: Provider[];
  mapping: ModelMapping | null;
  initial?: MappingDraft | null;
  isOpen: boolean;
  onClose: () => void;
  onSave: (data: MappingFormState) => Promise<void>;
}) {
  const [form, setForm] = useState<MappingFormState>({
    id: "",
    clientModel: "",
    providerId: "",
    upstreamModel: "",
    aliases: [],
    inputTypes: ["text"],
    inputContextSize: 0,
    outputSize: 0,
    inputPrice: 0,
    outputPrice: 0,
    cacheReadPrice: 0,
  });
  // 别名的自由输入框内容：回车或点「添加」才落进 form.aliases，避免每敲一个
  // 字符就生成一个半截别名。
  const [aliasDraft, setAliasDraft] = useState("");
  // 保存失败的原因（例如后端拒绝重复路由）。不显示就只是「点了没反应」。
  const [saveError, setSaveError] = useState<string | null>(null);
  // 模型能力目录弹窗：打开时默认用当前上游模型名过滤。
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  // 启用的提供商及其配置激活的模型，作为上游候选；正在编辑的提供商即使停用也保留。
  const candidates = useMemo(() => {
    const list = providers
      .filter((p) => p.enabled && p.models.length > 0)
      .sort((a, b) => a.name.localeCompare(b.name));
    if (mapping && !list.some((p) => p.id === mapping.providerId)) {
      const current = providers.find((p) => p.id === mapping.providerId);
      if (current) list.push(current);
    }
    return list;
  }, [providers, mapping]);

  useEffect(() => {
    if (!isOpen) return;
    setSaveError(null);
    const preferred = initial?.providerId ?? mapping?.providerId ?? "";
    setForm({
      id: mapping?.id ?? "",
      clientModel: initial?.clientModel ?? mapping?.clientModel ?? "",
      providerId: candidates.some((p) => p.id === preferred)
        ? preferred
        : (candidates[0]?.id ?? ""),
      upstreamModel: initial?.upstreamModel ?? mapping?.upstreamModel ?? "",
      aliases: mapping?.aliases ?? [],
      // 旧数据或未配置时 inputTypes 为空数组：模型默认都有 text，勾选上。
      inputTypes: mapping?.inputTypes?.length ? mapping.inputTypes : ["text"],
      inputContextSize: mapping?.inputContextSize ?? 0,
      outputSize: mapping?.outputSize ?? 0,
      inputPrice: mapping?.inputPrice ?? 0,
      outputPrice: mapping?.outputPrice ?? 0,
      cacheReadPrice: mapping?.cacheReadPrice ?? 0,
    });
    setAliasDraft("");
    setModelPickerOpen(false);
  }, [mapping, initial, isOpen, candidates]);

  if (!isOpen) return null;

  const providerModels = (id: string): string[] =>
    providers.find((p) => p.id === id)?.models ?? [];
  // 新增映射时，若未填写客户端模型名，默认取「前缀 / 模型名称」。
  const impliedName = () => {
    const p = providers.find((item) => item.id === form.providerId);
    const m = providerModels(form.providerId).find(
      (model) => model === form.upstreamModel,
    );
    return p && m ? defaultClientModel(p, m) : form.clientModel;
  };

  const addAlias = () => {
    const name = aliasDraft.trim();
    if (!name) return;
    // 客户端模型名本身就是可命中的名字，再存一遍只会多一行重复标签。
    if (name === (form.clientModel.trim() || impliedName())) {
      setAliasDraft("");
      return;
    }
    setForm((c) =>
      c.aliases.includes(name) ? c : { ...c, aliases: [...c.aliases, name] },
    );
    setAliasDraft("");
  };

  // 从 models.dev 目录选中一条：只回填能力字段，不动价格/别名/上游模型名。
  const applyDevModel = (model: DevModel) => {
    const inputTypes = toInputTypes(model.modalities.input);
    setForm((c) => ({
      ...c,
      inputTypes: inputTypes.length ? inputTypes : c.inputTypes,
      // 目录缺 limit 时保留已填值，避免把手工配置清成 0。
      inputContextSize: model.limit.context || c.inputContextSize,
      outputSize: model.limit.output || c.outputSize,
    }));
    setModelPickerOpen(false);
  };

  const submit = async () => {
    try {
      await onSave({
        ...form,
        clientModel: form.clientModel.trim() || impliedName(),
        // 一个都没勾等于没配置，落回默认 text，避免空数组在列表里显得像坏了。
        inputTypes: form.inputTypes.length ? form.inputTypes : ["text"],
      });
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
      return;
    }
    onClose();
  };

  return (
    <>
      <div
        className="provider-drawer-overlay"
        role="presentation"
        onMouseDown={onClose}
      >
        <aside
          className="provider-drawer"
          role="dialog"
          aria-modal="true"
          aria-label={mapping ? "编辑模型映射" : "添加模型映射"}
          onMouseDown={(event) => event.stopPropagation()}
        >
          <div className="provider-drawer-header">
            <div>
              <h2>{mapping ? "编辑模型映射" : "添加模型映射"}</h2>
              <p>将客户端模型名路由到上游提供商。</p>
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
            <FieldSelect
              label="目标提供商"
              placeholder={
                candidates.length
                  ? "选择已启用提供商"
                  : "暂无启用且已配置模型的提供商"
              }
              value={form.providerId ? form.providerId : null}
              onChange={(key) =>
                setForm((c) => ({
                  ...c,
                  providerId: key ?? "",
                  upstreamModel: "",
                }))
              }
              isDisabled={!candidates.length}
              options={candidates.map((p) => ({ value: p.id, label: p.name }))}
            />
            <FieldSelect
              label="上游模型"
              placeholder="选择提供商激活的模型"
              value={form.upstreamModel ? form.upstreamModel : null}
              onChange={(key) =>
                setForm((c) => ({ ...c, upstreamModel: key ?? "" }))
              }
              isDisabled={!form.providerId}
              options={providerModels(form.providerId).map((model) => ({
                value: model,
                label: model,
              }))}
            />
            <TextField
              className="provider-form-field"
              value={form.clientModel}
              onChange={(value) =>
                setForm((c) => ({ ...c, clientModel: value }))
              }
              aria-label="客户端模型名"
            >
              <Label>
                客户端模型名
                <Tooltip>
                  <Tooltip.Trigger className="field-label-help">
                    ?
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    默认为<b>提供商模型前缀 / 模型名称</b>
                    （未设置前缀时用提供商名称），例如{" "}
                    <code>{impliedName()}</code>； 可重设置为任意名称。
                    多家提供商可以用同一个名称：请求按列表顺序依次尝试，前一家限流或报错就
                    自动转下一家。
                  </Tooltip.Content>
                </Tooltip>
              </Label>
              <Input
                placeholder={
                  impliedName() || "映射使用的名称（默认即上方规则）"
                }
              />
            </TextField>
            <Separator className="my-1" />
            {saveError && (
              <p className="mapping-save-error" role="alert">
                {saveError}
              </p>
            )}
            <section
              className="mapping-meta-field"
              aria-labelledby="mapping-meta-heading"
            >
              <div className="mapping-meta-head">
                <b id="mapping-meta-heading">模型能力</b>
                <Button
                  size="sm"
                  variant="outline"
                  onPress={() => setModelPickerOpen(true)}
                >
                  选择模型
                </Button>
              </div>
              <div className="mapping-meta-label">输入类型</div>
              <div
                className="mapping-type-options"
                role="group"
                aria-label="输入类型"
              >
                {INPUT_TYPE_OPTIONS.map((option) => {
                  const selected = form.inputTypes.includes(option.value);
                  return (
                    <Button
                      key={option.value}
                      size="sm"
                      variant={selected ? "primary" : "outline"}
                      aria-label={`${option.label}（${option.hint}）`}
                      aria-pressed={selected}
                      onPress={() =>
                        setForm((c) => ({
                          ...c,
                          inputTypes: selected
                            ? c.inputTypes.filter((v) => v !== option.value)
                            : [...c.inputTypes, option.value],
                        }))
                      }
                    >
                      {option.label}
                    </Button>
                  );
                })}
              </div>
              <div className="mapping-size-row">
                <TextField
                  className="provider-form-field"
                  value={
                    form.inputContextSize ? String(form.inputContextSize) : ""
                  }
                  onChange={(value) =>
                    setForm((c) => ({
                      ...c,
                      inputContextSize: parseSize(value),
                    }))
                  }
                  aria-label="输入上下文大小"
                >
                  <Label>输入上下文（tokens）</Label>
                  <Input
                    type="number"
                    inputMode="numeric"
                    placeholder="未设置"
                  />
                </TextField>
                <SizeQuickSet
                  onPick={(value) =>
                    setForm((c) => ({ ...c, inputContextSize: value }))
                  }
                />
              </div>
              <div className="mapping-size-row">
                <TextField
                  className="provider-form-field"
                  value={form.outputSize ? String(form.outputSize) : ""}
                  onChange={(value) =>
                    setForm((c) => ({ ...c, outputSize: parseSize(value) }))
                  }
                  aria-label="输出大小"
                >
                  <Label>输出大小（tokens）</Label>
                  <Input
                    type="number"
                    inputMode="numeric"
                    placeholder="未设置"
                  />
                </TextField>
                <SizeQuickSet
                  onPick={(value) =>
                    setForm((c) => ({ ...c, outputSize: value }))
                  }
                />
              </div>
              <p className="provider-form-note">
                仅作配置记录与列表展示，网关按原样转发请求，不做容量校验。
              </p>
            </section>
            <Separator className="my-1" />
            <section
              className="mapping-meta-field"
              aria-labelledby="mapping-price-heading"
            >
              <b id="mapping-price-heading">
                价格
                <Tooltip>
                  <Tooltip.Trigger className="field-label-help">
                    ?
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    单价（每百万 tokens，默认 0 即未设置）：仅 UI
                    配置展示，不参与计费；货币单位由你自行约定。
                  </Tooltip.Content>
                </Tooltip>
              </b>
              <div className="mapping-price-grid">
                <TextField
                  className="provider-form-field"
                  value={String(form.inputPrice)}
                  onChange={(value) =>
                    setForm((c) => ({ ...c, inputPrice: parsePrice(value) }))
                  }
                  aria-label="输入价格"
                >
                  <Label>
                    输入
                    <Tooltip>
                      <Tooltip.Trigger className="field-label-help">
                        ?
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        每 100 万输入 tokens 的价格。
                      </Tooltip.Content>
                    </Tooltip>
                  </Label>
                  <Input
                    type="number"
                    inputMode="decimal"
                    min={0}
                    step="any"
                    placeholder="0"
                  />
                </TextField>
                <TextField
                  className="provider-form-field"
                  value={String(form.outputPrice)}
                  onChange={(value) =>
                    setForm((c) => ({ ...c, outputPrice: parsePrice(value) }))
                  }
                  aria-label="输出价格"
                >
                  <Label>
                    输出
                    <Tooltip>
                      <Tooltip.Trigger className="field-label-help">
                        ?
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        每 100 万输出 tokens 的价格。
                      </Tooltip.Content>
                    </Tooltip>
                  </Label>
                  <Input
                    type="number"
                    inputMode="decimal"
                    min={0}
                    step="any"
                    placeholder="0"
                  />
                </TextField>
                <TextField
                  className="provider-form-field"
                  value={String(form.cacheReadPrice)}
                  onChange={(value) =>
                    setForm((c) => ({
                      ...c,
                      cacheReadPrice: parsePrice(value),
                    }))
                  }
                  aria-label="Cache 读价格"
                >
                  <Label>
                    Cache 读
                    <Tooltip>
                      <Tooltip.Trigger className="field-label-help">
                        ?
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        每 100 万 cache 读 tokens 的价格。
                      </Tooltip.Content>
                    </Tooltip>
                  </Label>
                  <Input
                    type="number"
                    inputMode="decimal"
                    min={0}
                    step="any"
                    placeholder="0"
                  />
                </TextField>
              </div>
            </section>
            <Separator className="my-1" />
            <section
              className="mapping-alias-field"
              aria-labelledby="mapping-alias-heading"
            >
              <b id="mapping-alias-heading">
                模型别名
                <Tooltip>
                  <Tooltip.Trigger className="field-label-help">
                    ?
                  </Tooltip.Trigger>
                  <Tooltip.Content>
                    别名与客户端模型名等价：客户端用它发起请求会命中同一条映射。
                    别名只用于命中路由，不会出现在 <code>/v1/models</code>
                    与生成的 CLI
                    配置里。别名可以与其它提供商的映射重名（同样构成 failover
                    链），但不能与同一提供商的同一上游模型重复。
                  </Tooltip.Content>
                </Tooltip>
              </b>
              <div className="mapping-alias-input">
                <TextField
                  value={aliasDraft}
                  onChange={setAliasDraft}
                  aria-label="模型别名"
                >
                  <Input
                    placeholder="输入别名后回车，例如 gpt-5"
                    onKeyDown={(event) => {
                      if (event.key !== "Enter") return;
                      // 这个输入框不提交表单：回车只收一个别名。
                      event.preventDefault();
                      addAlias();
                    }}
                  />
                </TextField>
                <Button
                  size="sm"
                  variant="outline"
                  isDisabled={!aliasDraft.trim()}
                  onPress={addAlias}
                  aria-label="添加别名"
                >
                  <Plus size={14} />
                </Button>
              </div>
              {form.aliases.length > 0 ? (
                <TagGroup
                  size="sm"
                  variant="surface"
                  aria-label="已添加的模型别名"
                  onRemove={(keys) =>
                    setForm((c) => ({
                      ...c,
                      aliases: c.aliases.filter((name) => !keys.has(name)),
                    }))
                  }
                >
                  <TagGroup.List>
                    {form.aliases.map((name) => (
                      <Tag key={name} id={name}>
                        {name}
                      </Tag>
                    ))}
                  </TagGroup.List>
                </TagGroup>
              ) : (
                <p className="provider-form-note">尚未添加别名</p>
              )}
            </section>
          </div>
          <div className="provider-drawer-footer">
            <Button size="sm" variant="outline" onPress={onClose}>
              取消
            </Button>
            <Button
              size="sm"
              variant="primary"
              isDisabled={!form.providerId || !form.upstreamModel}
              onPress={submit}
            >
              {mapping ? "保存修改" : "添加映射"}
            </Button>
          </div>
        </aside>
      </div>
      {/* 模型弹窗必须在遮罩外：挂在 overlay 里时 mousedown 会沿 React 树冒泡，
          把抽屉一起关掉。选中只回填字段，抽屉保持打开。 */}
      <DevModelModal
        isOpen={modelPickerOpen}
        onClose={() => setModelPickerOpen(false)}
        defaultQuery={form.upstreamModel}
        onSelect={applyDevModel}
      />
    </>
  );
}

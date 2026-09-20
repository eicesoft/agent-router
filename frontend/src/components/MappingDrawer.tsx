import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Input,
  Label,
  Separator,
  Tag,
  TagGroup,
  TextField,
} from "@heroui/react";
import { Plus, X } from "lucide-react";
import { defaultClientModel } from "../lib/naming";
import type { ModelMapping, Provider } from "../lib/types";
import { FieldSelect } from "./FieldSelect";

export type MappingFormState = {
  id: string;
  clientModel: string;
  providerId: string;
  upstreamModel: string;
  aliases: string[];
};

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
  });
  // 别名的自由输入框内容：回车或点「添加」才落进 form.aliases，避免每敲一个
  // 字符就生成一个半截别名。
  const [aliasDraft, setAliasDraft] = useState("");
  // 保存失败的原因（例如后端拒绝重复路由）。不显示就只是「点了没反应」。
  const [saveError, setSaveError] = useState<string | null>(null);
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
    });
    setAliasDraft("");
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

  const submit = async () => {
    try {
      await onSave({
        ...form,
        clientModel: form.clientModel.trim() || impliedName(),
      });
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
      return;
    }
    onClose();
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
            onChange={(value) => setForm((c) => ({ ...c, clientModel: value }))}
            aria-label="客户端模型名"
          >
            <Label>客户端模型名</Label>
            <Input
              placeholder={impliedName() || "映射使用的名称（默认即上方规则）"}
            />
          </TextField>
          <Separator className="my-1" />
          <p className="provider-form-note">
            默认为<b>提供商模型前缀 / 模型名称</b>
            （未设置前缀时用提供商名称），例如 <code>{impliedName()}</code>；
            可重设置为任意名称。
            多家提供商可以用同一个名称：请求按列表顺序依次尝试，前一家限流或报错就
            自动转下一家。
          </p>
          <Separator className="my-1" />
          {saveError && (
            <p className="mapping-save-error" role="alert">
              {saveError}
            </p>
          )}
          <section
            className="mapping-alias-field"
            aria-labelledby="mapping-alias-heading"
          >
            <b id="mapping-alias-heading">模型别名</b>
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
            <p className="provider-form-note">
              别名与客户端模型名等价：客户端用它发起请求会命中同一条映射。
              别名只用于命中路由，不会出现在 <code>/v1/models</code>
              与生成的 CLI 配置里。别名可以与其它提供商的映射重名（同样构成
              failover 链），但不能与同一提供商的同一上游模型重复。
            </p>
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
  );
}

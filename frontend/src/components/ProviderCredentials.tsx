import { useEffect, useState } from "react";
import {
  Button,
  Input,
  Label,
  Switch,
  TextField,
  Tooltip,
} from "@heroui/react";
import { Check, Plus, RotateCcw, Trash2 } from "lucide-react";
import {
  addProviderCredential,
  deleteProviderCredential,
  listProviderCredentials,
  resetProviderCredentialStatus,
  setProviderCredentialMode,
  toggleProviderCredential,
  updateProviderCredential,
} from "../lib/api";
import type { CredentialMode, ProviderCredential } from "../lib/types";
import { FieldSelect } from "./FieldSelect";

const modes: { value: CredentialMode; label: string; hint: string }[] = [
  {
    value: "session",
    label: "会话粘性",
    hint: "同一会话固定同一 Key；上游提示缓存按 Key 隔离，粘性才能命中",
  },
  {
    value: "round_robin",
    label: "轮询",
    hint: "按顺序轮流使用每个 Key",
  },
  {
    value: "least_used",
    label: "用少优先",
    hint: "优先选用请求数最少的 Key，适合分摊限额",
  },
  { value: "random", label: "随机", hint: "在可用 Key 中随机挑选" },
];

function modeHint(mode: string) {
  return modes.find((item) => item.value === mode)?.hint ?? modes[0].hint;
}

// ProviderCredentials edits one provider's key pool. Credentials are separate
// rows rather than part of the provider form: they are added and removed
// individually, and the value itself never leaves the OS Keychain.
export function ProviderCredentials({ providerId }: { providerId: string }) {
  const [items, setItems] = useState<ProviderCredential[]>([]);
  const [mode, setMode] = useState<string>("session");
  const [name, setName] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    setItems([]);
    setError("");
    setName("");
    setApiKey("");
    if (!providerId) return;
    listProviderCredentials(providerId)
      .then((list) => {
        if (active) setItems(list);
      })
      .catch((err) => {
        if (active) setError(String(err));
      });
    return () => {
      active = false;
    };
  }, [providerId]);

  const refresh = async () => {
    if (!providerId) return;
    try {
      setItems(await listProviderCredentials(providerId));
    } catch (err) {
      setError(String(err));
    }
  };

  const add = async () => {
    if (!apiKey.trim()) {
      setError("请填写 API Key。");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await addProviderCredential(providerId, name.trim(), apiKey.trim());
      setName("");
      setApiKey("");
      await refresh();
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  };

  const chooseMode = async (value: string) => {
    setMode(value);
    try {
      await setProviderCredentialMode(providerId, value);
    } catch (err) {
      setError(String(err));
    }
  };

  return (
    <section
      className="provider-credentials"
      aria-labelledby="provider-credentials-heading"
    >
      <header className="provider-credentials-header">
        <div>
          <b id="provider-credentials-heading">API Key 池</b>
          <p>{modeHint(mode)}</p>
        </div>
        <FieldSelect
          label=""
          value={mode}
          onChange={(value) => value && chooseMode(value)}
          options={modes.map((item) => ({
            value: item.value,
            label: item.label,
          }))}
          className="provider-credential-mode"
          fullWidth
        />
      </header>

      {items.length > 0 && (
        <ul className="provider-credential-list">
          {items.map((item, index) => (
            <CredentialRow
              key={item.id}
              credential={item}
              index={index}
              onChanged={refresh}
              onError={setError}
            />
          ))}
        </ul>
      )}

      {items.length === 0 && (
        <p className="provider-model-empty">
          尚未配置 API Key。添加多个 Key 后可按上面的模式轮换使用。
        </p>
      )}

      <div className="provider-credential-add">
        <TextField
          value={name}
          onChange={setName}
          aria-label="Key 名称"
          className="provider-credential-name"
        >
          <Label>名称（可选）</Label>
          <Input placeholder="例如：主账号" />
        </TextField>
        <TextField
          value={apiKey}
          onChange={setApiKey}
          aria-label="API Key"
          className="provider-credential-secret"
        >
          <Label>API Key</Label>
          <Input type="password" placeholder="sk-..." />
        </TextField>
        <Button
          isIconOnly
          size="sm"
          variant="outline"
          onPress={add}
          isDisabled={busy || !apiKey.trim()}
          aria-label="添加 API Key"
        >
          <Plus size={15} />
        </Button>
      </div>
      {error && <p className="provider-model-error">{error}</p>}
    </section>
  );
}

function CredentialRow({
  credential,
  index,
  onChanged,
  onError,
}: {
  credential: ProviderCredential;
  index: number;
  onChanged: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(credential.name);
  const [weight, setWeight] = useState(String(credential.weight));

  useEffect(() => {
    setName(credential.name);
    setWeight(String(credential.weight));
  }, [credential.id, credential.name, credential.weight]);

  // Credentials carry no key material, only the derived mask, so the label
  // identifies them by the operator's name plus the masked key.
  const label = credential.name || `Key ${index + 1}`;
  const invalid = credential.status === "invalid";

  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      await onChanged();
    } catch (err) {
      onError(String(err));
    }
  };

  return (
    <li className="provider-credential-row">
      <div className="provider-credential-main">
        {editing ? (
          <>
            <TextField value={name} onChange={setName} aria-label="名称">
              <Input placeholder="名称" />
            </TextField>
            <TextField value={weight} onChange={setWeight} aria-label="权重">
              <Input type="number" placeholder="权重" />
            </TextField>
            <Button
              isIconOnly
              size="sm"
              variant="ghost"
              aria-label="保存名称与权重"
              onPress={() =>
                run(async () => {
                  await updateProviderCredential(
                    credential.id,
                    name.trim(),
                    Number(weight) || 1,
                  );
                  setEditing(false);
                })
              }
            >
              <Check size={15} />
            </Button>
          </>
        ) : (
          <>
            <button
              type="button"
              className="provider-credential-title"
              onClick={() => setEditing(true)}
              title="点击重命名或调整权重"
            >
              <b>{label}</b>
              {credential.mask && <code>{credential.mask}</code>}
              {credential.weight > 1 && <em>权重 x{credential.weight}</em>}
            </button>
            {invalid && (
              <span
                className="provider-credential-badge"
                title={credential.lastError}
              >
                已失效
              </span>
            )}
          </>
        )}
      </div>
      <div className="provider-credential-actions">
        {invalid && (
          <Tooltip>
            <Tooltip.Trigger>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                aria-label={`重置 ${label}`}
                onPress={() =>
                  run(() => resetProviderCredentialStatus(credential.id))
                }
              >
                <RotateCcw size={14} />
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>重置状态后重新试用该 Key</Tooltip.Content>
          </Tooltip>
        )}
        <Switch
          size="sm"
          isSelected={credential.enabled}
          aria-label={`启用 ${label}`}
          onChange={(enabled) =>
            run(() => toggleProviderCredential(credential.id, enabled))
          }
        />
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          aria-label={`删除 ${label}`}
          onPress={() => run(() => deleteProviderCredential(credential.id))}
        >
          <Trash2 size={14} />
        </Button>
      </div>
    </li>
  );
}

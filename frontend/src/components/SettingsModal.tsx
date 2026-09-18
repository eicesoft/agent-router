import { useEffect, useState } from "react";
import {
  Button,
  Label,
  Modal,
  Select,
  ListBox,
  TextField,
  Input,
} from "@heroui/react";
import type { AppSettings } from "../lib/types";
import { saveSettings } from "../lib/api";

const hosts = [
  { value: "127.0.0.1", label: "127.0.0.1（仅本机）" },
  { value: "0.0.0.0", label: "0.0.0.0（局域网可访问）" },
] as const;

const themes = [
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
] as const;

export function SettingsModal({
  isOpen,
  settings,
  onClose,
  onSaved,
}: {
  isOpen: boolean;
  settings: AppSettings;
  onClose: () => void;
  onSaved: (next: AppSettings) => void;
}) {
  const [form, setForm] = useState(settings);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (isOpen) {
      setForm(settings);
      setError("");
    }
  }, [isOpen, settings]);

  const port = Number(form.port);
  const portValid = Number.isInteger(port) && port >= 1 && port <= 65535;
  const dirty =
    form.host !== settings.host ||
    port !== settings.port ||
    form.theme !== settings.theme;

  const save = async () => {
    if (!portValid) {
      setError("端口必须是 1-65535 之间的整数");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const next = await saveSettings({
        host: form.host,
        port,
        theme: form.theme,
      });
      onSaved(next);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal isOpen={isOpen} onOpenChange={(open) => !open && onClose()}>
      <Modal.Backdrop>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>设置</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <div className="settings-form">
                <div className="settings-field">
                  <Label>监听地址</Label>
                  <Select
                    aria-label="监听地址"
                    selectedKey={form.host}
                    onSelectionChange={(key) =>
                      setForm({ ...form, host: (key as string) ?? "127.0.0.1" })
                    }
                  >
                    <Select.Trigger>
                      <Select.Value />
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={[...hosts]}>
                        {(option) => (
                          <ListBox.Item
                            id={option.value}
                            textValue={option.label}
                          >
                            {option.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>
                <TextField className="settings-field">
                  <Label>端口</Label>
                  <Input
                    type="number"
                    inputMode="numeric"
                    value={String(form.port)}
                    onChange={(value) =>
                      setForm({ ...form, port: Number(value) || 0 })
                    }
                  />
                </TextField>
                <div className="settings-field">
                  <Label>主题</Label>
                  <Select
                    aria-label="主题"
                    selectedKey={form.theme}
                    onSelectionChange={(key) =>
                      setForm({ ...form, theme: (key as string) ?? "light" })
                    }
                  >
                    <Select.Trigger>
                      <Select.Value />
                      <Select.Indicator />
                    </Select.Trigger>
                    <Select.Popover>
                      <ListBox items={[...themes]}>
                        {(option) => (
                          <ListBox.Item
                            id={option.value}
                            textValue={option.label}
                          >
                            {option.label}
                          </ListBox.Item>
                        )}
                      </ListBox>
                    </Select.Popover>
                  </Select>
                </div>
                <p className="provider-form-note settings-note">
                  更改监听地址或端口后，本地网关会立即在新地址重启。
                </p>
                {error && <p className="settings-error">{error}</p>}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button
                size="sm"
                variant="outline"
                onPress={onClose}
                isDisabled={saving}
              >
                取消
              </Button>
              <Button size="sm" onPress={save} isDisabled={saving || !dirty}>
                {saving ? "保存中…" : "保存"}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

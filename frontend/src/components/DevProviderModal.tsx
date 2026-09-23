import { useEffect, useState } from "react";
import { Button, Modal, TextField, Input, Label } from "@heroui/react";
import { Info, Search } from "lucide-react";
import { listDevProviders, refreshDevProviders } from "../lib/api";
import type { DevProvider } from "../lib/types";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";

function openDoc(doc: string) {
  if (!doc) return;
  // Wails 运行时用系统浏览器打开；浏览器预览时退回 tab。
  try {
    if ((window as any).runtime?.BrowserOpenURL) {
      BrowserOpenURL(doc);
      return;
    }
  } catch {
    /* fall through */
  }
  window.open(doc, "_blank", "noopener,noreferrer");
}

// models.dev 供应商选择：打开时先读本地缓存，再后台刷新一次并换上新列表。
export function DevProviderModal({
  isOpen,
  onClose,
  onSelect,
}: {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (provider: DevProvider) => void;
}) {
  const [items, setItems] = useState<DevProvider[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!isOpen) return;
    let active = true;
    setQuery("");
    setError("");
    setLoading(true);
    listDevProviders()
      .then((cached) => {
        if (active) setItems(cached);
      })
      .finally(() => {
        if (!active) return;
        refreshDevProviders()
          .then((fresh) => {
            if (active) setItems(fresh);
          })
          .catch((e) => {
            if (active) setError(String(e));
          })
          .finally(() => {
            if (active) setLoading(false);
          });
      });
    return () => {
      active = false;
    };
  }, [isOpen]);

  const keyword = query.trim().toLowerCase();
  const filtered = keyword
    ? items.filter(
        (item) =>
          item.name.toLowerCase().includes(keyword) ||
          item.id.toLowerCase().includes(keyword) ||
          item.api.toLowerCase().includes(keyword),
      )
    : items;

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={(open: boolean) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop>
        <Modal.Container size="md">
          <Modal.Dialog className="dev-provider-modal">
            <Modal.Header>
              <Modal.Heading>选择供应商</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <TextField
                value={query}
                onChange={setQuery}
                aria-label="搜索供应商"
              >
                <Label className="sr-only">搜索供应商</Label>
                <Input placeholder="搜索名称或 API" />
                <span className="dev-provider-search-icon" aria-hidden>
                  <Search size={14} />
                </span>
              </TextField>
              {error && <div className="skills-error">{error}</div>}
              <div className="dev-provider-list" aria-busy={loading}>
                {loading && items.length === 0 && (
                  <div className="skills-empty">正在加载供应商目录…</div>
                )}
                {!loading && filtered.length === 0 && (
                  <div className="skills-empty">没有匹配的供应商</div>
                )}
                {filtered.map((item) => (
                  <div className="dev-provider-row" key={item.id}>
                    <button
                      type="button"
                      className="dev-provider-pick"
                      onClick={() => onSelect(item)}
                      title={item.api}
                    >
                      <span className="dev-provider-name">{item.name}</span>
                      <span className="dev-provider-api">{item.api}</span>
                    </button>
                    {item.doc && (
                      <Button
                        isIconOnly
                        size="sm"
                        variant="ghost"
                        aria-label={`打开 ${item.name} 文档`}
                        onPress={() => openDoc(item.doc)}
                      >
                        <Info size={15} />
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

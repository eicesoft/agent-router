import { useEffect, useMemo, useState } from "react";
import { Button, Modal, TextField, Input, Label, Tooltip } from "@heroui/react";
import {
  AudioLines,
  Brain,
  Braces,
  FileText,
  Image,
  Search,
  Type,
  Video,
  Wrench,
} from "lucide-react";
import { listDevModels, refreshDevModels } from "../lib/api";
import { formatTokenCount } from "../lib/format";
import type { DevModel } from "../lib/types";

// models.dev 的输入模态 → 本应用 INPUT_TYPE 取值；pdf 在网关侧叫 file。
const MODALITY_ICON: Record<string, { Icon: typeof Type; label: string }> = {
  text: { Icon: Type, label: "文本" },
  image: { Icon: Image, label: "图像" },
  audio: { Icon: AudioLines, label: "音频" },
  video: { Icon: Video, label: "视频" },
  pdf: { Icon: FileText, label: "PDF" },
  file: { Icon: FileText, label: "文件" },
};

// 右侧能力位：最多三个，有才显示，灰色图标 + tooltip。
const CAPABILITY_FLAGS = [
  {
    key: "reasoning" as const,
    Icon: Brain,
    label: "思考",
    tip: "支持思考（reasoning）",
  },
  {
    key: "toolCall" as const,
    Icon: Wrench,
    label: "工具调用",
    tip: "支持工具调用（tool_call）",
  },
  {
    key: "structuredOutput" as const,
    Icon: Braces,
    label: "结构化输出",
    tip: "支持结构化输出（structured_output）",
  },
];

function ModalityIcons({
  values,
  side,
}: {
  values: string[];
  side?: "输入" | "输出";
}) {
  const seen = new Set<string>();
  const items: { key: string; Icon: typeof Type; label: string }[] = [];
  for (const value of values) {
    const hit = MODALITY_ICON[value];
    if (!hit || seen.has(value)) continue;
    seen.add(value);
    items.push({ key: value, ...hit });
  }
  const aria = side ? `${side}模态` : "模态";
  return (
    <span className="dev-model-modalities" aria-label={aria}>
      {items.map(({ key, Icon, label }) => (
        <Tooltip key={key}>
          <Tooltip.Trigger className="dev-model-modality" aria-label={label}>
            <Icon size={13} />
          </Tooltip.Trigger>
          <Tooltip.Content>
            {side ? `${side}：${label}` : label}
          </Tooltip.Content>
        </Tooltip>
      ))}
    </span>
  );
}

// 输入 / 输出两组模态图标，中间用 / 分隔；两边都显示。
function ModalityPair({
  input,
  output,
}: {
  input: string[];
  output: string[];
}) {
  return (
    <span className="dev-model-io">
      <ModalityIcons values={input} side="输入" />
      <span className="dev-model-io-sep" aria-hidden>
        /
      </span>
      <ModalityIcons values={output} side="输出" />
    </span>
  );
}

function CapabilityIcons({ model }: { model: DevModel }) {
  const active = CAPABILITY_FLAGS.filter((flag) => model[flag.key]).slice(0, 3);
  if (!active.length) return null;
  return (
    <span className="dev-model-caps" aria-label="模型能力">
      {active.map(({ key, Icon, tip }) => (
        <Tooltip key={key}>
          <Tooltip.Trigger className="dev-model-cap" aria-label={tip}>
            <Icon size={13} />
          </Tooltip.Trigger>
          <Tooltip.Content>{tip}</Tooltip.Content>
        </Tooltip>
      ))}
    </span>
  );
}

// 从 models.dev 目录挑一条模型，把能力字段回填进映射表单。
// 打开时默认用上游模型名过滤，可改可清。
export function DevModelModal({
  isOpen,
  onClose,
  defaultQuery,
  onSelect,
}: {
  isOpen: boolean;
  onClose: () => void;
  defaultQuery: string;
  onSelect: (model: DevModel) => void;
}) {
  const [items, setItems] = useState<DevModel[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!isOpen) return;
    let active = true;
    setQuery(defaultQuery.trim());
    setError("");
    setLoading(true);
    listDevModels()
      .then((cached) => {
        if (active) setItems(cached);
      })
      .finally(() => {
        if (!active) return;
        refreshDevModels()
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
  }, [isOpen, defaultQuery]);

  const keyword = query.trim().toLowerCase();
  const filtered = useMemo(() => {
    if (!keyword) return items;
    return items.filter(
      (item) =>
        item.id.toLowerCase().includes(keyword) ||
        item.name.toLowerCase().includes(keyword),
    );
  }, [items, keyword]);

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={(open: boolean) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop>
        <Modal.Container size="md">
          <Modal.Dialog className="dev-provider-modal dev-model-modal">
            <Modal.Header>
              <Modal.Heading>选择模型能力</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <TextField
                value={query}
                onChange={setQuery}
                aria-label="搜索模型"
              >
                <Label className="sr-only">搜索模型</Label>
                <Input placeholder="搜索模型名称或 ID" />
                <span className="dev-provider-search-icon" aria-hidden>
                  <Search size={14} />
                </span>
              </TextField>
              {error && <div className="skills-error">{error}</div>}
              <div className="dev-provider-list" aria-busy={loading}>
                {loading && items.length === 0 && (
                  <div className="skills-empty">正在加载模型目录…</div>
                )}
                {!loading && filtered.length === 0 && (
                  <div className="skills-empty">没有匹配的模型</div>
                )}
                {filtered.map((item) => (
                  <button
                    type="button"
                    className="dev-model-row"
                    key={item.id}
                    onClick={() => onSelect(item)}
                  >
                    <span className="dev-model-row-main">
                      <span className="dev-model-name">{item.name}</span>
                      <ModalityPair
                        input={item.modalities.input}
                        output={item.modalities.output}
                      />
                      {item.releaseDate && (
                        <span className="dev-model-meta">
                          {item.releaseDate}
                        </span>
                      )}
                      {(item.limit.context > 0 || item.limit.output > 0) && (
                        <span className="dev-model-context">
                          {item.limit.context > 0 &&
                            formatTokenCount(item.limit.context)}
                          {item.limit.context > 0 &&
                            item.limit.output > 0 &&
                            " / "}
                          {item.limit.output > 0 &&
                            formatTokenCount(item.limit.output)}
                        </span>
                      )}
                    </span>
                    <span className="dev-model-row-sub">
                      {item.description ? (
                        <Tooltip>
                          <Tooltip.Trigger className="dev-model-desc">
                            {item.description}
                          </Tooltip.Trigger>
                          <Tooltip.Content>{item.description}</Tooltip.Content>
                        </Tooltip>
                      ) : (
                        <span className="dev-model-desc" />
                      )}
                      <CapabilityIcons model={item} />
                    </span>
                  </button>
                ))}
              </div>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

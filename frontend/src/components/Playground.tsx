import { useCallback, useEffect, useRef, useState } from "react";
import {
  Button,
  Chip,
  Label,
  Popover,
  ScrollShadow,
  Slider,
  TextArea,
} from "@heroui/react";
import {
  Brain,
  ChevronDown,
  ChevronUp,
  Send,
  Sparkle,
  Square,
} from "lucide-react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { cancelPlayground, playgroundChat } from "../lib/api";
import { renderMarkdown } from "../lib/markdown";
import type {
  LocalAPIKey,
  ModelMapping,
  PlaygroundChunk,
  PlaygroundResult,
  Provider,
} from "../lib/types";
import { FieldSelect } from "./FieldSelect";

type Turn = { role: "user" | "assistant"; text: string; reasoning?: string };

// One SSE frame, kept raw so the panel shows exactly what the gateway sent.
type Frame = { data: string; kind: FrameKind };
type FrameKind = "text" | "reasoning" | "tool" | "usage" | "done" | "other";

// classify labels a frame for coloring; the payload itself is never rewritten.
function classify(data: string): FrameKind {
  if (data === "[DONE]") return "done";
  let chunk: any;
  try {
    chunk = JSON.parse(data);
  } catch {
    return "other";
  }
  if (chunk?.usage) return "usage";
  const delta = chunk?.choices?.[0]?.delta;
  if (delta?.tool_calls?.length) return "tool";
  if (delta?.reasoning_content || delta?.reasoning) return "reasoning";
  if (delta?.content) return "text";
  if (chunk?.choices?.[0]?.finish_reason) return "other";
  return "other";
}

// textFrom returns the assistant-visible text carried by one frame.
function textFrom(data: string): string {
  try {
    const chunk = JSON.parse(data);
    const content = chunk?.choices?.[0]?.delta?.content;
    return typeof content === "string" ? content : "";
  } catch {
    return "";
  }
}

// reasoningFrom returns the thinking text carried by one frame. Upstreams spell
// it reasoning_content (DeepSeek/vLLM) or reasoning (some gateways); the gateway
// rewrites an Anthropic upstream's thinking_delta into reasoning_content too, so
// this is the single place the chain has to be read.
function reasoningFrom(data: string): string {
  try {
    const chunk = JSON.parse(data);
    const delta = chunk?.choices?.[0]?.delta;
    const reasoning = delta?.reasoning_content ?? delta?.reasoning;
    return typeof reasoning === "string" ? reasoning : "";
  } catch {
    return "";
  }
}

// 思考档位。off 不发 reasoning_effort，交给上游默认行为。
const thinkingLevels = [
  { value: "off", label: "关闭" },
  { value: "low", label: "低" },
  { value: "medium", label: "中" },
  { value: "high", label: "高" },
];

// 思考档位选择器：按钮 + Popover 里的滑块，四档等距，配一行刻度说明。
function ThinkingPicker({
  level,
  onChange,
}: {
  level: string;
  onChange: (level: string) => void;
}) {
  const index = Math.max(
    0,
    thinkingLevels.findIndex((item) => item.value === level),
  );
  const current = thinkingLevels[index];
  return (
    <Popover>
      <Popover.Trigger className="playground-thinking-trigger">
        <Brain size={14} />
        思考：{current.label}
      </Popover.Trigger>
      <Popover.Content className="playground-thinking-popover">
        <Popover.Dialog>
          <Slider
            aria-label="思考档位"
            minValue={0}
            maxValue={thinkingLevels.length - 1}
            step={1}
            value={index}
            onChange={(value) =>
              onChange(thinkingLevels[Math.round(Number(value))].value)
            }
          >
            <Label>思考档位</Label>
            <Slider.Output>{() => current.label}</Slider.Output>
            <Slider.Track>
              <Slider.Fill />
              <Slider.Thumb />
            </Slider.Track>
          </Slider>
        </Popover.Dialog>
      </Popover.Content>
    </Popover>
  );
}

// 流式光标：一个呼吸的 AI 小星星，跟着正在输出的文字走。
function Caret() {
  return <Sparkle size={12} className="playground-caret" aria-hidden />;
}

// 思考过程块：默认折叠，摘要里带上还在输出的状态点；展开时随增量实时滚动。
// open 归组件自己管：流式期间默认展开，用户手动折叠后本轮的更新不再抢回去。
function Thinking({ text, streaming }: { text: string; streaming: boolean }) {
  const body = useRef<HTMLDivElement | null>(null);
  const [open, setOpen] = useState(streaming);
  useEffect(() => {
    if (streaming && open) {
      body.current?.scrollTo({ top: body.current.scrollHeight });
    }
  }, [text, streaming, open]);
  return (
    <details
      className="playground-thinking"
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary>
        <Brain size={13} />
        {streaming ? "思考中…" : `思考过程（${text.length} 字）`}
        {streaming && <span className="playground-thinking-dot" />}
      </summary>
      <div className="playground-thinking-body" ref={body}>
        {text}
        {streaming && <Caret />}
      </div>
    </details>
  );
}

// 打字机：上游一次可能吐几十上百字符，逐字直出既看不清也浪费渲染。这里按帧
// 排队，每帧吐固定字数（每字符约 12ms），收到 Done 后直接把余量放完，避免收尾
// 等待。
const TYPE_TICK_MS = 25;
const TYPE_CHARS_PER_TICK = 2; // ≈12ms/字符

function useTypewriter(target: string, streaming: boolean) {
  const [shown, setShown] = useState(streaming ? "" : target);
  // 上游每个 chunk 都会更新 target，把它放进 effect 依赖会让定时器每帧重建、
  // 永远来不及触发；改用 ref 读最新值，定时器整轮只建一次。
  const latest = useRef(target);
  latest.current = target;
  useEffect(() => {
    if (!streaming) {
      setShown(latest.current);
      return;
    }
    const id = window.setInterval(() => {
      setShown((current) =>
        current.length >= latest.current.length
          ? current
          : latest.current.slice(0, current.length + TYPE_CHARS_PER_TICK),
      );
    }, TYPE_TICK_MS);
    return () => window.clearInterval(id);
  }, [streaming]);
  return shown;
}

// 助手气泡：Markdown 渲染 + 流式期间的打字机揭示。
function MarkdownBubble({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  const shown = useTypewriter(text, streaming);
  return (
    <div className="playground-bubble markdown">
      <div
        // eslint-disable-next-line react/no-danger -- HTML 已在 renderMarkdown 里净化。
        dangerouslySetInnerHTML={{ __html: renderMarkdown(shown) }}
      />
      {streaming && <Caret />}
    </div>
  );
}

const frameLabels: Record<FrameKind, string> = {
  text: "文本",
  reasoning: "推理",
  tool: "工具调用",
  usage: "用量",
  done: "结束",
  other: "其他",
};

export function Playground({
  models,
  providers,
  keys,
  proxyRunning,
  onRegisterReset,
  onHasContentChange,
}: {
  models: ModelMapping[];
  providers: Provider[];
  keys: LocalAPIKey[];
  proxyRunning: boolean;
  // 清空按钮在页面头部（见 App.tsx），这里只把回调与“是否有内容”上报出去。
  onRegisterReset: (reset: (() => void) | null) => void;
  onHasContentChange: (hasContent: boolean) => void;
}) {
  const [model, setModel] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [streaming, setStreaming] = useState("");
  const [thinking, setThinking] = useState("");
  const [frames, setFrames] = useState<Frame[]>([]);
  const [result, setResult] = useState<PlaygroundResult | null>(null);
  const [error, setError] = useState("");
  const [showFrames, setShowFrames] = useState(false);
  const [level, setLevel] = useState("low");
  const runId = useRef("");
  // 本轮流式文本按事件累积：完成事件到达时用它落定消息，避免依赖 setState
  // 回调的副作用（React 严格模式下会被调用两次）。
  const streamed = useRef("");
  // 推理文本同样按事件累积；完成事件里与正文一起落定到这条消息上。
  const thought = useRef("");
  const pending = useRef<Turn[]>([]);
  const threadEnd = useRef<HTMLDivElement | null>(null);

  const hasKey = keys.some((k) => k.enabled && k.key);
  // 默认选中第一个可用模型，省去每次进页面都要手动选。
  useEffect(() => {
    if (model && models.some((m) => m.clientModel === model)) return;
    setModel(models[0]?.clientModel ?? null);
  }, [models, model]);

  // 原始帧按 runId 过滤：上一轮的迟到帧不能混进这一轮。完成事件在后端每条
  // 退出路径上都会发出，而绑定返回值与事件到达顺序无保证，因此在这里收尾。
  useEffect(() => {
    const off = EventsOn("playground:chunk", (chunk: PlaygroundChunk) => {
      if (chunk?.runId !== runId.current) return;
      if (chunk.done) {
        if (streamed.current || thought.current) {
          setTurns([
            ...pending.current,
            {
              role: "assistant",
              text: streamed.current,
              reasoning: thought.current || undefined,
            },
          ]);
        }
        streamed.current = "";
        thought.current = "";
        setStreaming("");
        setThinking("");
      }
      setFrames((current) => [
        ...current,
        { data: chunk.data, kind: classify(chunk.data) },
      ]);
      const reasoning = reasoningFrom(chunk.data);
      if (reasoning) {
        thought.current += reasoning;
        setThinking(thought.current);
      }
      const text = textFrom(chunk.data);
      if (text) {
        streamed.current += text;
        setStreaming(streamed.current);
      }
    });
    return off;
  }, []);

  useEffect(() => {
    threadEnd.current?.scrollIntoView({ block: "end" });
  }, [turns, streaming, thinking, frames]);

  // 模型按提供商分组：选项只显示映射后的上游模型名，映射前的客户端模型名放
  // 在悬停提示里。
  //
  // 同名多提供商是一条链：选项的 value 必须是客户端模型名本身（网关只认这个名字，
  // 由链策略决定先打哪家），所以它只能出现一次——重复 value 会让下拉选不中。它也
  // 不属于任何单家提供商，因此单独列一组并标出链上有哪几家，否则用户会以为这个名字
  // 只归链首那家（以前就会这样，点下去永远只打链首）。
  const chainMembers = new Map<string, ModelMapping[]>();
  for (const m of models) {
    chainMembers.set(m.clientModel, [
      ...(chainMembers.get(m.clientModel) ?? []),
      m,
    ]);
  }
  const chains = [...chainMembers.entries()].filter(
    ([, list]) => list.length > 1,
  );
  const chainedNames = new Set(chains.map(([name]) => name));
  const providerName = (id: string) =>
    providers.find((p) => p.id === id)?.name ?? id;
  const modelGroups = [
    ...providers.map((provider) => ({
      title: provider.name,
      options: models
        .filter(
          (m) =>
            m.providerId === provider.id && !chainedNames.has(m.clientModel),
        )
        .map((m) => ({
          value: m.clientModel,
          label: m.upstreamModel,
          tooltip: `客户端模型：${m.clientModel}`,
        }))
        .sort((a, b) => a.label.localeCompare(b.label, "en")),
    })),
    ...(chains.length > 0
      ? [
          {
            title: "同名链（多提供商）",
            options: chains
              .map(([name, list]) => ({
                value: name,
                label: name,
                tooltip: `同名链 ${list.length} 家：${list
                  .map((m) => providerName(m.providerId))
                  .join("、")}`,
              }))
              .sort((a, b) => a.value.localeCompare(b.value, "en")),
          },
        ]
      : []),
  ].filter((group) => group.options.length > 0);

  const send = async () => {
    const question = input.trim();
    if (!question || !model || streaming) return;
    const history: Turn[] = [...turns, { role: "user", text: question }];
    setTurns(history);
    pending.current = history;
    setInput("");
    streamed.current = "";
    thought.current = "";
    setStreaming("");
    setThinking("");
    setFrames([]);
    setResult(null);
    setError("");
    const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    runId.current = id;
    try {
      setResult(
        await playgroundChat(
          id,
          model,
          history.map((turn) => ({ role: turn.role, content: turn.text })),
          level,
        ),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const stop = async () => {
    await cancelPlayground();
  };

  const reset = useCallback(() => {
    setTurns([]);
    setStreaming("");
    setThinking("");
    setFrames([]);
    setResult(null);
    setError("");
  }, []);

  // 把清空回调交给页面头部，并在离开页面时注销。
  useEffect(() => {
    onRegisterReset(reset);
    return () => onRegisterReset(null);
  }, [onRegisterReset, reset]);

  useEffect(() => {
    onHasContentChange(turns.length > 0 || !!frames.length);
  }, [onHasContentChange, turns.length, frames.length]);

  const blocked = !proxyRunning
    ? "本地代理已停止，请先在控制台开启"
    : !hasKey
      ? "请先在「本地密钥」中生成并启用一个密钥"
      : !model
        ? "没有可路由的模型，请先启用提供商"
        : "";

  return (
    <section className="playground">
      <ScrollShadow className="playground-thread" size={24}>
        {turns.map((turn, index) => (
          <div className={`playground-turn ${turn.role}`} key={index}>
            {turn.reasoning && (
              <Thinking text={turn.reasoning} streaming={false} />
            )}
            <MarkdownBubble text={turn.text} streaming={false} />
          </div>
        ))}
        {(thinking || streaming) && (
          <div className="playground-turn assistant">
            {thinking && <Thinking text={thinking} streaming />}
            {streaming && <MarkdownBubble text={streaming} streaming />}
          </div>
        )}
        <div ref={threadEnd} />
      </ScrollShadow>

      {error && <span className="playground-error">{error}</span>}
      {blocked && !error && <span className="playground-error">{blocked}</span>}

      {/* 展开时把手移到面板上方，与面板连成一块。 */}
      <button
        type="button"
        className="playground-frames-toggle"
        aria-label={showFrames ? "收起原始 SSE 帧" : "展开原始 SSE 帧"}
        aria-expanded={showFrames}
        onClick={() => setShowFrames((value) => !value)}
      >
        {showFrames ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
      </button>

      {showFrames && (
        <div className="playground-frames">
          <div className="playground-frames-head">
            <span>原始 SSE 帧</span>
            <span>
              {frames.length > 0
                ? `本轮共 ${frames.length} 帧${
                    result ? ` · ${result.latencyMs} ms` : ""
                  }`
                : "尚未收到数据帧"}
            </span>
          </div>
          <ScrollShadow className="playground-frame-list" size={16}>
            {frames.map((frame, index) => (
              <div className={`playground-frame ${frame.kind}`} key={index}>
                <span className="playground-frame-index">{index + 1}</span>
                <Chip size="sm" className="playground-frame-kind">
                  {frameLabels[frame.kind]}
                </Chip>
                <pre>{frame.data}</pre>
              </div>
            ))}
          </ScrollShadow>
        </div>
      )}

      <div className="playground-composer">
        <div className="playground-composer-box">
          <div className="playground-composer-main">
            <TextArea
              aria-label="输入内容"
              className="playground-input"
              placeholder="输入要发送给模型的内容，Enter 发送，Shift+Enter 换行"
              rows={1}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  void send();
                }
              }}
            />
            <div className="playground-composer-bar">
              <div className="playground-composer-picks">
                <FieldSelect
                  className="playground-model-select"
                  placeholder="选择模型"
                  value={model}
                  onChange={setModel}
                  popoverClassName="request-log-select-popover"
                  renderValue={(value) =>
                    // 同名链没有单一上游模型名（各家不同），直接显示客户端名，
                    // 否则会只显示链首那家的上游名，误导成「只打这一家」。
                    chainedNames.has(value)
                      ? value
                      : (models.find((m) => m.clientModel === value)
                          ?.upstreamModel ?? value)
                  }
                  groups={modelGroups}
                />
                <ThinkingPicker level={level} onChange={setLevel} />
              </div>
            </div>
          </div>
          {streaming ? (
            <Button
              className="playground-send"
              variant="ghost"
              isIconOnly
              aria-label="停止"
              onPress={stop}
            >
              <Square size={16} />
            </Button>
          ) : (
            <Button
              className="playground-send"
              variant="primary"
              isIconOnly
              aria-label="发送"
              isDisabled={!input.trim() || !!blocked}
              onPress={send}
            >
              <Send size={16} />
            </Button>
          )}
        </div>
      </div>
    </section>
  );
}

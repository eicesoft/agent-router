import { useCallback, useEffect, useRef, useState } from "react";
import { Label, Popover, Slider } from "@heroui/react";
import {
  Brain,
  Check,
  ChevronDown,
  ChevronUp,
  Send,
  Sparkle,
  Square,
  Terminal,
  Wrench,
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
import { JsonBlock } from "./JsonBlock";

type ToolCall = { id: string; name: string; arguments: string };
type Turn = {
  role: "user" | "assistant";
  text: string;
  reasoning?: string;
  toolCalls?: ToolCall[];
};

// One SSE frame, kept raw so the panel shows exactly what the gateway sent.
type Frame = { data: string; kind: FrameKind };
type FrameKind = "text" | "reasoning" | "tool" | "usage" | "done" | "other";

type ToolDelta = {
  index: number;
  id?: string;
  name?: string;
  arguments?: string;
};

// classify labels a frame for the debugging view; the payload is never rewritten.
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

function textFrom(data: string): string {
  try {
    const chunk = JSON.parse(data);
    const content = chunk?.choices?.[0]?.delta?.content;
    return typeof content === "string" ? content : "";
  } catch {
    return "";
  }
}

// Upstreams spell reasoning differently; Anthropic's thinking_delta is also
// normalized to reasoning_content by the gateway before it reaches this UI.
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

function toolDeltasFrom(data: string): ToolDelta[] {
  try {
    const chunk = JSON.parse(data);
    const calls = chunk?.choices?.[0]?.delta?.tool_calls;
    if (!Array.isArray(calls)) return [];
    return calls
      .map((call: any, fallbackIndex: number) => ({
        index: Number.isFinite(call?.index) ? call.index : fallbackIndex,
        id: typeof call?.id === "string" ? call.id : undefined,
        name:
          typeof call?.function?.name === "string"
            ? call.function.name
            : undefined,
        arguments:
          typeof call?.function?.arguments === "string"
            ? call.function.arguments
            : undefined,
      }))
      .filter(
        (call: ToolDelta) =>
          call.id || call.name || call.arguments !== undefined,
      );
  } catch {
    return [];
  }
}

function mergeToolCalls(current: ToolCall[], deltas: ToolDelta[]): ToolCall[] {
  const next = [...current];
  for (const delta of deltas) {
    const existing = next[delta.index];
    next[delta.index] = {
      id: delta.id ?? existing?.id ?? `tool-${delta.index}`,
      name: delta.name ?? existing?.name ?? "tool",
      arguments: `${existing?.arguments ?? ""}${delta.arguments ?? ""}`,
    };
  }
  return next;
}

function toolArguments(raw: string): string {
  if (!raw.trim()) return "等待参数…";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

// 思考档位。off 不发 reasoning_effort，交给上游默认行为。
const thinkingLevels = [
  { value: "off", label: "关闭" },
  { value: "low", label: "低" },
  { value: "medium", label: "中" },
  { value: "high", label: "高" },
];

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
      <Popover.Trigger className="pg-thinking-trigger">
        <Brain size={14} />
        <span>思考 {current.label}</span>
      </Popover.Trigger>
      <Popover.Content className="pg-thinking-popover">
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

function ThinkingTrace({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  const body = useRef<HTMLDivElement | null>(null);
  const [open, setOpen] = useState(streaming);
  useEffect(() => {
    if (streaming && open) {
      body.current?.scrollTo({ top: body.current.scrollHeight });
    }
  }, [text, streaming, open]);

  const rows = text.split(/\n+/).filter(Boolean);
  return (
    <div className="pg-thinking">
      <button
        type="button"
        className="pg-thinking-header"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Sparkle
          size={15}
          className={
            streaming ? "pg-thinking-spark is-live" : "pg-thinking-spark"
          }
        />
        <span className="pg-thinking-label">
          {streaming ? "思考中" : `思考过程 · ${text.length} 字`}
        </span>
        <ChevronDown
          size={14}
          className={
            open ? "pg-thinking-chevron is-open" : "pg-thinking-chevron"
          }
        />
      </button>
      <div className={open ? "pg-thinking-trace is-open" : "pg-thinking-trace"}>
        <div className="pg-thinking-clip">
          <div className="pg-thinking-rows" ref={body}>
            {rows.map((row, index) => {
              const last = streaming && index === rows.length - 1;
              return (
                <div className="pg-thinking-row" key={`${index}-${row}`}>
                  <span
                    className={last ? "pg-trace-mark is-live" : "pg-trace-mark"}
                  >
                    {last ? (
                      <span className="pg-trace-spinner" />
                    ) : (
                      <Check size={12} />
                    )}
                  </span>
                  <span>{row}</span>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}

// 上游一次可能吐几十上百字符。保持每字符约 12ms 的节奏，收到 Done 后立即放完。
const TYPE_TICK_MS = 25;
const TYPE_CHARS_PER_TICK = 2;

function useTypewriter(target: string, streaming: boolean) {
  const [shown, setShown] = useState(streaming ? "" : target);
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

function Answer({ text, streaming }: { text: string; streaming: boolean }) {
  const shown = useTypewriter(text, streaming);
  if (streaming) {
    const tailLength = Math.min(6, shown.length);
    const split = shown.length - tailLength;
    return (
      <div className="pg-streaming-answer">
        <span>{shown.slice(0, split)}</span>
        <span className="stream-tail">{shown.slice(split)}</span>
        <span className="stream-caret is-streaming" aria-hidden />
      </div>
    );
  }
  return (
    <div className="pg-answer markdown">
      <div
        // eslint-disable-next-line react/no-danger -- HTML 已在 renderMarkdown 里净化。
        dangerouslySetInnerHTML={{ __html: renderMarkdown(shown) }}
      />
    </div>
  );
}

function ToolRun({ calls }: { calls: ToolCall[] }) {
  const [open, setOpen] = useState(true);
  if (calls.length === 0) return null;
  return (
    <div className="pg-tools">
      <button
        type="button"
        className="pg-tools-header"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <ChevronDown
          size={13}
          className={open ? "pg-tools-chevron is-open" : "pg-tools-chevron"}
        />
        <Wrench size={13} />
        <span>{calls.length} 次工具调用</span>
      </button>
      <div className={open ? "pg-tools-list is-open" : "pg-tools-list"}>
        <div className="pg-tools-clip">
          {calls.map((call, index) => (
            <div className="pg-tool-row" key={call.id || index}>
              <Terminal size={14} />
              <span className="pg-tool-name">{call.name}</span>
              <pre>{toolArguments(call.arguments)}</pre>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function Waiting() {
  return (
    <div className="pg-waiting" role="status" aria-label="等待模型响应">
      <span />
      <span />
      <span />
    </div>
  );
}

const frameLabels: Record<FrameKind, string> = {
  text: "文本",
  reasoning: "推理",
  tool: "工具",
  usage: "用量",
  done: "结束",
  other: "其他",
};

// 每类帧都走 JsonBlock：视图默认收起只占一行，展开/折叠交给点击，且能看清结构。
// 语法着色（键/值/括号各自一色）对所有帧一律保留——类型信息正是调试时最要紧的一层，
// 早期给文本/推理帧开的「单色档」把它们冲成一片同色，已废弃；帧底色仍靠 .pg-frame.*
// 区分（文本白底、推理浅底、用量/结束彩底），语法色按底色挑一套，见 styles.css。
const MODEL_STORAGE_KEY = "agent-router.playground.model";

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
  onRegisterReset: (reset: (() => void) | null) => void;
  onHasContentChange: (hasContent: boolean) => void;
}) {
  const [model, setModel] = useState<string | null>(() => {
    const saved = localStorage.getItem(MODEL_STORAGE_KEY);
    return saved && models.some((item) => item.clientModel === saved)
      ? saved
      : (models[0]?.clientModel ?? null);
  });
  const [input, setInput] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [streaming, setStreaming] = useState("");
  const [thinking, setThinking] = useState("");
  const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
  const [frames, setFrames] = useState<Frame[]>([]);
  const [result, setResult] = useState<PlaygroundResult | null>(null);
  const [error, setError] = useState("");
  const [showFrames, setShowFrames] = useState(false);
  const [level, setLevel] = useState("low");
  const [running, setRunning] = useState(false);
  const runId = useRef("");
  const streamed = useRef("");
  const thought = useRef("");
  const tools = useRef<ToolCall[]>([]);
  const pending = useRef<Turn[]>([]);
  const threadEnd = useRef<HTMLDivElement | null>(null);

  const hasKey = keys.some((key) => key.enabled && key.key);
  useEffect(() => {
    if (model && models.some((item) => item.clientModel === model)) return;
    setModel(models[0]?.clientModel ?? null);
  }, [models, model]);

  useEffect(() => {
    if (model) localStorage.setItem(MODEL_STORAGE_KEY, model);
  }, [model]);

  useEffect(() => {
    const off = EventsOn("playground:chunk", (chunk: PlaygroundChunk) => {
      if (chunk?.runId !== runId.current) return;
      if (chunk.done) {
        if (streamed.current || thought.current || tools.current.length > 0) {
          setTurns([
            ...pending.current,
            {
              role: "assistant",
              text: streamed.current,
              reasoning: thought.current || undefined,
              toolCalls: tools.current.length > 0 ? tools.current : undefined,
            },
          ]);
        }
        streamed.current = "";
        thought.current = "";
        tools.current = [];
        setStreaming("");
        setThinking("");
        setToolCalls([]);
        setRunning(false);
      }
      if (chunk.data) {
        setFrames((current) => [
          ...current,
          { data: chunk.data, kind: classify(chunk.data) },
        ]);
      }
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
      const deltas = toolDeltasFrom(chunk.data);
      if (deltas.length > 0) {
        setToolCalls((current) => {
          const next = mergeToolCalls(current, deltas);
          tools.current = next;
          return next;
        });
      }
    });
    return off;
  }, []);

  useEffect(() => {
    threadEnd.current?.scrollIntoView({ block: "end" });
  }, [turns, streaming, thinking, toolCalls, frames]);

  const chainMembers = new Map<string, ModelMapping[]>();
  for (const item of models) {
    chainMembers.set(item.clientModel, [
      ...(chainMembers.get(item.clientModel) ?? []),
      item,
    ]);
  }
  const chains = [...chainMembers.entries()].filter(
    ([, list]) => list.length > 1,
  );
  const chainedNames = new Set(chains.map(([name]) => name));
  const providerName = (id: string) =>
    providers.find((provider) => provider.id === id)?.name ?? id;
  const modelGroups = [
    ...providers.map((provider) => ({
      title: provider.name,
      options: models
        .filter(
          (item) =>
            item.providerId === provider.id &&
            !chainedNames.has(item.clientModel),
        )
        .map((item) => ({
          value: item.clientModel,
          label: item.upstreamModel,
          tooltip: `客户端模型：${item.clientModel}`,
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
                  .map((item) => providerName(item.providerId))
                  .join("、")}`,
              }))
              .sort((a, b) => a.value.localeCompare(b.value, "en")),
          },
        ]
      : []),
  ].filter((group) => group.options.length > 0);

  const send = async () => {
    const question = input.trim();
    if (!question || !model || running) return;
    const history: Turn[] = [...turns, { role: "user", text: question }];
    setTurns(history);
    pending.current = history;
    setInput("");
    streamed.current = "";
    thought.current = "";
    tools.current = [];
    setStreaming("");
    setThinking("");
    setToolCalls([]);
    setFrames([]);
    setResult(null);
    setError("");
    setRunning(true);
    const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    runId.current = id;
    try {
      setResult(
        await playgroundChat(
          id,
          model,
          history
            .filter((turn) => turn.text)
            .map((turn) => ({ role: turn.role, content: turn.text })),
          level,
        ),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setRunning(false);
    }
  };

  const stop = async () => {
    await cancelPlayground();
  };

  const reset = useCallback(() => {
    if (running) void cancelPlayground();
    runId.current = "";
    streamed.current = "";
    thought.current = "";
    tools.current = [];
    setTurns([]);
    setStreaming("");
    setThinking("");
    setToolCalls([]);
    setFrames([]);
    setResult(null);
    setError("");
    setRunning(false);
  }, [running]);

  useEffect(() => {
    onRegisterReset(reset);
    return () => onRegisterReset(null);
  }, [onRegisterReset, reset]);

  useEffect(() => {
    onHasContentChange(turns.length > 0 || frames.length > 0 || running);
  }, [onHasContentChange, turns.length, frames.length, running]);

  const blocked = !proxyRunning
    ? "本地代理已停止，请先在控制台开启"
    : !hasKey
      ? "请先在「本地密钥」中生成并启用一个密钥"
      : !model
        ? "没有可路由的模型，请先启用提供商"
        : "";
  const hasDraft = running && !thinking && !streaming && toolCalls.length === 0;

  return (
    <section className="playground">
      <div className="pg-thread">
        <div className="pg-thread-inner">
          {turns.map((turn, index) => (
            <div
              className={`pg-message ${turn.role}`}
              key={`${turn.role}-${index}`}
            >
              {turn.reasoning && (
                <ThinkingTrace text={turn.reasoning} streaming={false} />
              )}
              {turn.toolCalls && <ToolRun calls={turn.toolCalls} />}
              {turn.text &&
                (turn.role === "user" ? (
                  <div className="pg-user-bubble">{turn.text}</div>
                ) : (
                  <Answer text={turn.text} streaming={false} />
                ))}
            </div>
          ))}
          {(thinking || streaming || toolCalls.length > 0 || hasDraft) && (
            <div className="pg-message assistant">
              {thinking && <ThinkingTrace text={thinking} streaming />}
              {toolCalls.length > 0 && <ToolRun calls={toolCalls} />}
              {streaming && <Answer text={streaming} streaming />}
              {hasDraft && <Waiting />}
            </div>
          )}
          <div ref={threadEnd} />
        </div>
      </div>

      {error && <span className="pg-error">{error}</span>}
      {blocked && !error && <span className="pg-error">{blocked}</span>}

      <div className={showFrames ? "pg-debug is-open" : "pg-debug"}>
        <button
          type="button"
          className="pg-debug-toggle"
          aria-label={showFrames ? "收起原始 SSE 帧" : "展开原始 SSE 帧"}
          aria-expanded={showFrames}
          onClick={() => setShowFrames((value) => !value)}
        >
          {showFrames ? <ChevronDown size={18} /> : <ChevronUp size={18} />}
        </button>
        {showFrames && (
          <div className="pg-frames">
            {frames.length === 0 ? (
              <div className="pg-frames-empty">尚未收到数据帧</div>
            ) : (
              frames.map((frame, index) => (
                <div className={`pg-frame ${frame.kind}`} key={index}>
                  <span className="pg-frame-index">{index + 1}</span>
                  <span className="pg-frame-kind">
                    {frameLabels[frame.kind]}
                  </span>
                  <JsonBlock className="pg-frame-body" text={frame.data} />
                </div>
              ))
            )}
          </div>
        )}
      </div>

      <div className="pg-composer">
        <textarea
          aria-label="输入内容"
          className="pg-composer-input"
          placeholder="输入要发送给模型的内容，Enter 发送，Shift+Enter 换行"
          rows={1}
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (
              event.key === "Enter" &&
              !event.shiftKey &&
              !event.nativeEvent.isComposing
            ) {
              event.preventDefault();
              void send();
            }
          }}
        />
        <div className="pg-composer-bar">
          <div className="pg-composer-picks">
            <FieldSelect
              className="pg-model-select"
              placeholder="选择模型"
              value={model}
              onChange={setModel}
              popoverClassName="request-log-select-popover pg-model-select-popover"
              renderValue={(value) =>
                chainedNames.has(value)
                  ? value
                  : (models.find((item) => item.clientModel === value)
                      ?.upstreamModel ?? value)
              }
              groups={modelGroups}
            />
            <ThinkingPicker level={level} onChange={setLevel} />
          </div>
          {running ? (
            <button
              type="button"
              className="pg-send is-stop"
              aria-label="停止"
              onClick={() => void stop()}
            >
              <Square size={14} />
            </button>
          ) : (
            <button
              type="button"
              className="pg-send"
              aria-label="发送"
              disabled={!input.trim() || !!blocked}
              onClick={() => void send()}
            >
              <Send size={15} />
            </button>
          )}
        </div>
      </div>
    </section>
  );
}

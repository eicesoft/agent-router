import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { FoldVertical, UnfoldVertical } from "lucide-react";

// 展开策略做成枚举而不是回调，是为了保持引用稳定：SSE 帧是逐条 append 的，父组件
// 每次渲染都传一个新箭头函数会让每条帧的树全量重算，memo 就白做了。
const expandPolicies = {
  // 全收起：默认档。SSE 帧一行一条，收起来只占一行，展开交给点击。
  collapsed: () => false,
  // 只展开根：顶层键可见，更深层收起。
  rootOnly: (level: number) => level < 1,
  // 展开两层：看清结构即可，深层留给自己点开。
  twoLevels: (level: number) => level < 2,
  all: () => true,
};

export type JsonBlockProps = {
  /** 原始 JSON 文本；解析失败或不是对象/数组时原样回退为等宽文本。 */
  text: string;
  expand?: keyof typeof expandPolicies;
  className?: string;
  /** text 为空时显示的占位文案（如「暂无输出内容」）。 */
  placeholder?: string;
};

// 一次 JSON.parse 同时回答「能不能当树渲染」和「树的数据是什么」。SSE 的 data 不
// 保证是 JSON（[DONE]、keep-alive、上游错误体都可能出现），所以非对象/数组一律
// 退回 <pre>，保持「内容原样可见」这个前提。
function useParsedJSON(text: string) {
  return useMemo(() => {
    const trimmed = text.trim();
    if (!trimmed || (trimmed[0] !== "{" && trimmed[0] !== "[")) return null;
    try {
      const parsed: unknown = JSON.parse(trimmed);
      return typeof parsed === "object" && parsed !== null ? parsed : null;
    } catch {
      return null;
    }
  }, [text]);
}

/* ===== 折叠预览 =====
   折叠节点只显示一行摘要，摘要来自节点自身的内容（折叠时子节点不渲染，内容只存在
   于数据里）。宽度内优先撑满，由 CSS 的 text-overflow 按容器裁；字符预算只兜底
   （防超大对象把每一项都拼进 DOM），并在整条超预算时按条目边界丢尾、补省略号——
   而不是从中间切断某个 token，那样会显示出半截的键或字符串，看起来像坏掉的 JSON。
   预算曾取 64，在宽弹窗里约半行就出现 `…`、右侧空掉一半；抬到撑满常见宽度量级。 */

/** 折叠摘要的字符预算：条目累计能占多少（兜底上限，正常宽度裁剪走 CSS）。 */
const SNIPPET_BUDGET = 512;

/** 单条摘要条目的长度上限：给长字符串留足宽度，又不让一条无限吃掉预算。 */
const SNIPPET_ENTRY_MAX = 256;

type PieceKind =
  "punct" | "key" | "string" | "number" | "boolean" | "null" | "other";

/** 摘要由带类型的片段拼成，直接复用展开态的语法色，折叠前后看起来是同一套 JSON。 */
type Piece = { kind: PieceKind; text: string };

const pieceLength = (pieces: Piece[]) =>
  pieces.reduce((total, piece) => total + piece.text.length, 0);

const PIECE_CLASS: Record<PieceKind, string> = {
  punct: "json-tree-punct",
  key: "json-tree-label",
  string: "json-tree-string",
  number: "json-tree-number",
  boolean: "json-tree-boolean",
  null: "json-tree-null",
  other: "json-tree-other",
};

/** 把若干片段裁到 limit 个字符，截断处补省略号。 */
function clipPieces(pieces: Piece[], limit: number): Piece[] {
  const total = pieceLength(pieces);
  if (total <= limit) return pieces;
  const out: Piece[] = [];
  let used = 0;
  for (const piece of pieces) {
    // 留一格给省略号。
    const room = limit - 1 - used;
    if (room <= 0) break;
    if (piece.text.length <= room) {
      out.push(piece);
      used += piece.text.length;
    } else {
      // 用 Array.from 按码点切：直接 slice 会把代理对（emoji 等）劈成半个，渲染出
      // 替换字符。裁完再按码点拼回去，长度最多比 room 少 1。
      const codePoints = Array.from(piece.text);
      out.push({
        kind: piece.kind,
        text: `${codePoints.slice(0, room).join("")}…`,
      });
      return out;
    }
  }
  return out.length ? out : [{ kind: pieces[0].kind, text: "…" }];
}

/** 单个值的摘要片段；作为「值」出现的嵌套容器只表形状（{…} / […]）。
    数组元素走 arrayEntryPieces：那里会展开一层真实条目，与 object 折叠摘要一致。 */
function previewPieces(value: unknown): Piece[] {
  if (typeof value === "string")
    return [{ kind: "string", text: JSON.stringify(value) }];
  if (Array.isArray(value))
    return [{ kind: "punct", text: value.length ? "[…]" : "[]" }];
  if (value !== null && typeof value === "object") {
    return [{ kind: "punct", text: Object.keys(value).length ? "{…}" : "{}" }];
  }
  if (value === null) return [{ kind: "null", text: "null" }];
  if (typeof value === "boolean")
    return [{ kind: "boolean", text: String(value) }];
  if (typeof value === "number")
    return [{ kind: "number", text: String(value) }];
  return [{ kind: "other", text: String(value) }];
}

/** 折叠节点大括号之间的内容：`"key":1,"flag":true,…`。 */
function snippetPieces(value: unknown): Piece[] {
  // 先按「键 + 值」拼好整条再裁剪：先裁值会让键占满预算，出现 `"很长…":{…}` 甚至
  // 只剩 `{…}` 的摘要——键短、值长是最常见的形状（tool arguments 就是这样），必须
  // 把预算均摊给两者。
  const entries: Piece[][] = Array.isArray(value)
    ? (value as unknown[]).map(arrayEntryPieces)
    : Object.entries(value as Record<string, unknown>).map(([key, item]) =>
        clipPieces(
          [
            { kind: "key" as const, text: `${JSON.stringify(key)}:` },
            ...previewPieces(item),
          ],
          SNIPPET_ENTRY_MAX,
        ),
      );

  const out: Piece[] = [];
  let used = 0;
  let truncated = false;
  for (const entry of entries) {
    const entryLength = pieceLength(entry);
    // 分隔逗号也占预算，否则刚好卡住上限的那条会被逗号顶出去。
    const separator = out.length ? 1 : 0;
    if (used + separator + entryLength > SNIPPET_BUDGET) {
      truncated = true;
      break;
    }
    if (separator) {
      out.push({ kind: "punct", text: "," });
      used += 1;
    }
    out.push(...entry);
    used += entryLength;
  }
  // 一条都放不下时退到纯省略号，而不是空的大括号——空括号会被读成「真的没有内容」。
  if (!out.length) return [{ kind: "punct", text: "…" }];
  // 截断标记自身不计入预算，所以最终长度最多是预算 + 2，可以接受。
  if (truncated) out.push({ kind: "punct", text: ",…" });
  return out;
}

/** 容器摊成带括号的单行摘要；空容器直接给 `[]` / `{}`，不走 snippet 的纯省略号。 */
function inlineContainerPieces(value: object): Piece[] {
  const isArray = Array.isArray(value);
  const open: Piece = { kind: "punct", text: isArray ? "[" : "{" };
  const close: Piece = { kind: "punct", text: isArray ? "]" : "}" };
  if (isArray) {
    if ((value as unknown[]).length === 0)
      return [{ kind: "punct", text: "[]" }];
  } else if (Object.keys(value as Record<string, unknown>).length === 0) {
    return [{ kind: "punct", text: "{}" }];
  }
  return [open, ...snippetPieces(value), close];
}

/** 数组元素的摘要条目：标量用 previewPieces；对象/数组展开一层真实条目，
    与 object 折叠时展示自身内容的方式一致——否则 `[{…}]` 看不到任何实际字段。
    深一层的值仍走 previewPieces 的形状占位，避免摘要不必要地膨胀。 */
function arrayEntryPieces(item: unknown): Piece[] {
  const pieces =
    item !== null && typeof item === "object"
      ? inlineContainerPieces(item)
      : previewPieces(item);
  return clipPieces(pieces, SNIPPET_ENTRY_MAX);
}

/* ===== 渲染 ===== */

type Policy = (level: number, value: unknown, field?: string) => boolean;

type NodeProps = {
  /** 对象键名；数组元素为 null（不显示键名，与 JSON 的字面形状一致）。 */
  field: string | null;
  value: unknown;
  level: number;
  isLast: boolean;
  policy: Policy;
  /** 顶层「全部展开」开关：打开时忽略各自策略，容器一律展开。 */
  expandAll?: boolean;
  /** 只挂在根节点：上报展开态，供顶部「全部展开」按钮显隐。 */
  onExpandedChange?: (expanded: boolean) => void;
};

function primitiveClass(value: unknown): string {
  if (value === null) return "json-tree-null";
  if (typeof value === "string") return "json-tree-string";
  if (typeof value === "number") return "json-tree-number";
  if (typeof value === "boolean") return "json-tree-boolean";
  return "json-tree-other";
}

/** 字符串值保留转义（\n、\" 等），否则 SSE 里被转义过的 tool arguments 会直接
    断行，看起来像坏掉的 JSON。 */
function primitiveText(value: unknown): string {
  return JSON.stringify(value) ?? "null";
}

function PrimitiveNode({ field, value, isLast }: NodeProps) {
  return (
    <div className="json-tree-row" role="treeitem">
      {/* 标量没有折叠把手，但同层的键要和有把手的兄弟对齐，所以补一格等宽空槽。 */}
      <span className="json-tree-gutter" aria-hidden="true" />
      {field !== null && (
        <span className="json-tree-label">{JSON.stringify(field)}:</span>
      )}
      <span className={primitiveClass(value)}>{primitiveText(value)}</span>
      {!isLast && <span className="json-tree-punct">,</span>}
    </div>
  );
}

function ContainerNode({
  field,
  value,
  level,
  isLast,
  policy,
  expandAll = false,
  onExpandedChange,
}: NodeProps) {
  const resolveExpanded = (base: Policy, all: boolean) =>
    (all ? expandPolicies.all : base)(level, value, field ?? undefined);
  const [state, setState] = useState(() => ({
    value,
    policy,
    expandAll,
    expanded: resolveExpanded(policy, expandAll),
  }));
  // 数据、展开策略或「全部展开」开关变了就重算：前者避免上一次手动展开的形状平移
  // 到新内容上（看起来像展开错了节点），后两者让调用方改 expand / 点顶部按钮立刻
  // 生效——库原先靠一个 effect 在策略变化时重算，去掉库之后这一步必须自己做。
  // expandPolicies 的值是模块级常量，引用稳定，所以不会因为父组件重渲染而误重置。
  if (
    state.value !== value ||
    state.policy !== policy ||
    state.expandAll !== expandAll
  ) {
    setState({
      value,
      policy,
      expandAll,
      expanded: resolveExpanded(policy, expandAll),
    });
  }
  const expanded = state.expanded;
  // 根节点的展开态要让父级知道：「全部展开」只在根打开后才有意义。折叠状态归
  // 本节点自治，这里只做上报，不反向受控。
  useEffect(() => {
    onExpandedChange?.(expanded);
  }, [expanded, onExpandedChange]);
  const toggle = (next?: boolean) =>
    setState((prev) => ({ ...prev, expanded: next ?? !prev.expanded }));

  const isArray = Array.isArray(value);
  const items: Array<[string | null, unknown]> = isArray
    ? (value as unknown[]).map((item) => [null, item])
    : Object.entries(value as Record<string, unknown>);

  // 空容器没有可折叠的内容：它有展开状态、但展开前后都是 `{ }`，此时把手是个死
  // 控件，折叠态还会显示 `{…}`——那会谎称里面有条目。所以按叶子渲染。
  if (!items.length) {
    return (
      <div className="json-tree-row" role="treeitem">
        <span className="json-tree-gutter" aria-hidden="true" />
        {field !== null && (
          <span className="json-tree-label">{JSON.stringify(field)}:</span>
        )}
        <span className="json-tree-punct">{isArray ? "[" : "{"}</span>
        <span className="json-tree-punct">{isArray ? "]" : "}"}</span>
        {!isLast && <span className="json-tree-punct">,</span>}
      </div>
    );
  }

  const onKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    // 左右键折叠/展开是该树唯一需要自理的键：上下移动与激活交给 <button> 原生的
    // Tab 与 Enter/Space，比库原先「只有根可 Tab、上下键手动搬焦点」更好用。
    if (event.key === "ArrowLeft" && expanded) {
      event.preventDefault();
      toggle(false);
    } else if (event.key === "ArrowRight" && !expanded) {
      event.preventDefault();
      toggle(true);
    }
  };

  return (
    // 容器的 treeitem 必须是「标签行 + 子节点组」的共同外壳，树形结构才对。
    <div className="json-tree-node" role="treeitem" aria-expanded={expanded}>
      {/* 折叠态的行反过来：必须严格一行。用 flex 让摘要参与收缩，超宽才出现省略号；
          若把摘要做成块级元素，它会在 `{` 之后强制换行，短摘要也被推到下一行去。 */}
      <div
        className={
          expanded
            ? "json-tree-row json-tree-row-open"
            : "json-tree-row json-tree-row-clipped"
        }
      >
        <button
          type="button"
          className={expanded ? "json-tree-toggle is-open" : "json-tree-toggle"}
          aria-label={expanded ? "折叠 JSON" : "展开 JSON"}
          aria-expanded={expanded}
          onClick={() => toggle()}
          onKeyDown={onKeyDown}
        />
        {field !== null && (
          <span
            className="json-tree-label json-tree-label-clickable"
            onClick={() => toggle()}
          >
            {JSON.stringify(field)}:
          </span>
        )}
        <span className="json-tree-punct">{isArray ? "[" : "{"}</span>
        {!expanded && (
          // 摘要放在行尾、参与 flex 收缩，超宽时由行上的 overflow: hidden 截断。
          // 它不能是块级元素：那会在 `{` 之后强制换行，短摘要也被推到下一行。
          <span className="json-tree-collapsed-snippet" aria-hidden="true">
            {snippetPieces(value).map((piece, index) => (
              <span className={PIECE_CLASS[piece.kind]} key={index}>
                {piece.text}
              </span>
            ))}
          </span>
        )}
        {/* 首行整行可点：折叠态点任意处展开，展开态点任意处收缩。热区是绝对定位
            覆盖层，不参与排版；把手仍留在 DOM 里供 Tab/方向键操作。 */}
        <button
          type="button"
          className="json-tree-row-hit"
          aria-label={expanded ? "折叠节点" : "展开节点"}
          onClick={() => toggle()}
        />
        {!expanded && (
          // 收尾括号与逗号：折叠态跟在摘要后收成一行；展开态首行只留开括号，
          // 收尾由下方那行负责——两处都渲染会让展开后第一行变成 `{}`。
          <span className="json-tree-punct">{isArray ? "]" : "}"}</span>
        )}
        {!expanded && !isLast && <span className="json-tree-punct">,</span>}
      </div>
      {expanded && (
        // <li> 只是 <ul> 的必需外壳，标 role="none" 免得被读成 listitem。
        <ul className="json-tree-children" role="group">
          {items.map(([key, item], index) => (
            <li className="json-tree-item" role="none" key={key ?? `#${index}`}>
              <TreeNode
                field={key}
                value={item}
                level={level + 1}
                isLast={index === items.length - 1}
                policy={policy}
                expandAll={expandAll}
              />
            </li>
          ))}
        </ul>
      )}
      {expanded && (
        <div className="json-tree-row">
          <span className="json-tree-gutter" aria-hidden="true" />
          <span className="json-tree-punct">{isArray ? "]" : "}"}</span>
          {!isLast && <span className="json-tree-punct">,</span>}
        </div>
      )}
    </div>
  );
}

/** 分派用：容器要有展开状态，标量不需要，拆成两个组件才能让 hook 无条件调用。 */
function TreeNode(props: NodeProps) {
  const { value } = props;
  const isContainer =
    value !== null && typeof value === "object" && !Array.isArray(value)
      ? true
      : Array.isArray(value);
  return isContainer ? (
    <ContainerNode {...props} />
  ) : (
    <PrimitiveNode {...props} />
  );
}

function JsonTree({
  data,
  policy,
  expandAll,
  onRootExpandedChange,
}: {
  data: object;
  policy: Policy;
  expandAll: boolean;
  onRootExpandedChange: (expanded: boolean) => void;
}) {
  return (
    <div className="json-tree" role="tree" aria-label="JSON view">
      <TreeNode
        field={null}
        value={data}
        level={0}
        isLast
        policy={policy}
        expandAll={expandAll}
        onExpandedChange={onRootExpandedChange}
      />
    </div>
  );
}

function JsonBlockImpl({
  text,
  expand = "collapsed",
  className,
  placeholder,
}: JsonBlockProps) {
  const parsed = useParsedJSON(text);
  const classes = ["json-block", className ?? ""].filter(Boolean).join(" ");
  // 归一成 Policy 再调用：expandPolicies 各档 arity 不一致，直接下标调用会撞 TS。
  const basePolicy: Policy = expandPolicies[expand];
  const [expandAll, setExpandAll] = useState(false);
  // 「全部收起」后：根保持展开、根以下全收。不能退回默认 collapsed——那会把根也
  // 收成一行，按钮跟着消失；请求日志的 rootOnly 不受影响（本来就是这一档）。
  const [collapsedAll, setCollapsedAll] = useState(false);
  const policy: Policy = collapsedAll ? expandPolicies.rootOnly : basePolicy;
  // 根节点是否展开：默认档（collapsed）收起时不渲染「全部展开」，展开后才出现。
  const [rootExpanded, setRootExpanded] = useState(() => basePolicy(0, null));
  const handleRootExpandedChange = useCallback((expanded: boolean) => {
    setRootExpanded(expanded);
  }, []);
  // 换了一份内容就回到默认折叠策略：上一份的「已全部展开」不该平移到新 payload。
  const [lastText, setLastText] = useState(text);
  if (lastText !== text) {
    setLastText(text);
    if (expandAll) setExpandAll(false);
    if (collapsedAll) setCollapsedAll(false);
    // 根节点也会按新内容重算展开态；这里同步按钮显隐，避免 effect 前闪一下旧按钮。
    setRootExpanded(basePolicy(0, null));
  }
  if (parsed === null) {
    // className 已含 request-log-json-view / pg-frame 的排布规则，这里只补一个
    // 纯文本回退的标记类。
    return (
      <div className={`${classes} json-block-plain`}>
        <pre>{text || placeholder || ""}</pre>
      </div>
    );
  }
  // 根收起且未处于「全部展开」时，整棵树只有一行，顶部按钮没有落点，先藏起来。
  // 「全部收起」后根仍展开，按钮留在原位，可再一键全部展开。
  const showToggleAll = expandAll || rootExpanded;
  return (
    <div className={classes}>
      {showToggleAll && (
        <div className="json-block-actions">
          <button
            type="button"
            className="json-block-expand-all"
            aria-label={expandAll ? "全部收起" : "全部展开"}
            title={expandAll ? "全部收起" : "全部展开"}
            onClick={() => {
              if (expandAll) {
                // 根及第一层键可见，更深层收起；根不参与收起。
                setExpandAll(false);
                setCollapsedAll(true);
                setRootExpanded(true);
              } else {
                setExpandAll(true);
                setCollapsedAll(false);
              }
            }}
          >
            {expandAll ? (
              <FoldVertical size={13} aria-hidden="true" />
            ) : (
              <UnfoldVertical size={13} aria-hidden="true" />
            )}
          </button>
        </div>
      )}
      <JsonTree
        data={parsed}
        policy={policy}
        expandAll={expandAll}
        onRootExpandedChange={handleRootExpandedChange}
      />
    </div>
  );
}

export const JsonBlock = memo(JsonBlockImpl);

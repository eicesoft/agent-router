import {
  cloneElement,
  createContext,
  isValidElement,
  use,
  useMemo,
  useSyncExternalStore,
  type ComponentPropsWithRef,
  type ReactNode,
  type Ref,
} from "react";
import {
  Focusable,
  OverlayArrow,
  Tooltip as TooltipPrimitive,
  TooltipTrigger as TooltipTriggerPrimitive,
  TooltipTriggerStateContext,
} from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 tooltipVariants 同值的 BEM 类名。 */
interface TooltipSlots {
  base: string;
  trigger: string;
}

const tooltipSlots: TooltipSlots = {
  base: "tooltip",
  trigger: "tooltip__trigger",
};

/** composeSlotClassName 的字符串版：slots 缺省时原样返回 className（可为 undefined）。 */
function slotClass(slot: string | undefined, className: string | undefined) {
  return slot === undefined ? className : cx(slot, className);
}

const TooltipContext = createContext<{ slots?: TooltipSlots }>({});

/* -------------------------------------------------------------------------------------------------
 * 本地小工具（heroui 的 utils/css.js parseCSSTime + hooks/use-css-variable.js + mergeProps 内联）
 * -----------------------------------------------------------------------------------------------*/

function parseCSSTime(value: string | undefined): number | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  const val = parseFloat(trimmed);
  if (Number.isNaN(val)) return undefined;
  if (trimmed.endsWith("ms")) return val;
  if (trimmed.endsWith("s")) return val * 1000;
  return val;
}

const cssVariableCache = new Map<string, string | undefined>();

/** @react-aria/ssr 的 useIsSSR 等价物：SSR 快照 true，客户端快照 false。 */
function useIsSSR(): boolean {
  return useSyncExternalStore(
    () => () => {},
    () => typeof document === "undefined",
    () => true,
  );
}

function useCSSVariable(variableName: string): string | undefined {
  const isSSR = useIsSSR();
  return useMemo(() => {
    if (isSSR) return undefined;
    if (cssVariableCache.has(variableName)) {
      return cssVariableCache.get(variableName);
    }
    try {
      const root = document.documentElement;
      const value =
        getComputedStyle(root).getPropertyValue(variableName).trim() ||
        undefined;
      cssVariableCache.set(variableName, value);
      return value;
    } catch {
      return undefined;
    }
  }, [variableName, isSSR]);
}

/** react-aria 的 mergeProps 最小等价物（on* 函数链化 + undefined 不覆盖）。 */
function mergeProps(
  base: Record<string, unknown>,
  props: Record<string, unknown>,
): Record<string, unknown> {
  const result: Record<string, unknown> = { ...base };
  for (const key of Object.keys(props)) {
    const a = result[key];
    const b = props[key];
    const isEventHandler =
      typeof a === "function" &&
      typeof b === "function" &&
      key.charCodeAt(0) === /* o */ 111 &&
      key.charCodeAt(1) === /* n */ 110 &&
      key.charCodeAt(2) >= 65 &&
      key.charCodeAt(2) <= 90;
    if (isEventHandler) {
      const chainA = a as (...args: unknown[]) => void;
      const chainB = b as (...args: unknown[]) => void;
      result[key] = (...args: unknown[]) => {
        chainA(...args);
        chainB(...args);
      };
    } else {
      result[key] = b !== undefined ? b : a;
    }
  }
  return result;
}

/* -------------------------------------------------------------------------------------------------
 * Tooltip Animation Scope
 * -----------------------------------------------------------------------------------------------*/

// React Aria 在全局 warmup 计时器期间置 shouldSkipAnimation，会让换入的 tooltip 丢掉
// data-entering/exiting；heroui 让后代清掉该标志以保住淡入淡出，这里照搬。
const TooltipAnimationScope = ({
  children,
  shouldSkipAnimation,
}: {
  children?: ReactNode;
  shouldSkipAnimation?: boolean;
}) => {
  const state = use(TooltipTriggerStateContext);
  if (!state || shouldSkipAnimation) return <>{children}</>;
  return (
    <TooltipTriggerStateContext
      value={{ ...state, shouldSkipAnimation: false }}
    >
      {children}
    </TooltipTriggerStateContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Tooltip Root
 * -----------------------------------------------------------------------------------------------*/

type TooltipRootProps = ComponentPropsWithRef<
  typeof TooltipTriggerPrimitive
> & {
  shouldSkipAnimation?: boolean;
};

/** 从 @heroui/react 的 tooltip/tooltip.js 移植。 */
const TooltipRoot = ({
  children,
  closeDelay,
  delay,
  shouldSkipAnimation = false,
  ...props
}: TooltipRootProps) => {
  const slots = useMemo(() => tooltipSlots, []);
  const cssDelay = useCSSVariable("--tooltip-delay");
  const cssCloseDelay = useCSSVariable("--tooltip-close-delay");
  const resolvedDelay = delay ?? parseCSSTime(cssDelay);
  const resolvedCloseDelay = closeDelay ?? parseCSSTime(cssCloseDelay);
  return (
    <TooltipContext value={{ slots }}>
      <TooltipTriggerPrimitive
        closeDelay={resolvedCloseDelay}
        data-slot="tooltip-root"
        delay={resolvedDelay}
        {...props}
      >
        <TooltipAnimationScope shouldSkipAnimation={shouldSkipAnimation}>
          {children}
        </TooltipAnimationScope>
      </TooltipTriggerPrimitive>
    </TooltipContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Tooltip Content
 * -----------------------------------------------------------------------------------------------*/

interface TooltipContentProps extends Omit<
  ComponentPropsWithRef<typeof TooltipPrimitive>,
  "children"
> {
  showArrow?: boolean;
  children: ReactNode;
}

const TooltipContent = ({
  children,
  className,
  offset: offsetProp,
  showArrow = false,
  ...props
}: TooltipContentProps) => {
  const { slots } = use(TooltipContext);
  const offset = offsetProp ? offsetProp : showArrow ? 7 : 3;
  return (
    <TooltipPrimitive
      {...props}
      className={composeTwRenderProps(className, slots?.base)}
      offset={offset}
    >
      {children}
    </TooltipPrimitive>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Tooltip Arrow
 * -----------------------------------------------------------------------------------------------*/

type TooltipArrowProps = Omit<
  ComponentPropsWithRef<typeof OverlayArrow>,
  "children"
> & {
  children?: ReactNode;
};

const TooltipArrow = ({ children, className, ...props }: TooltipArrowProps) => {
  const defaultArrow = (
    <svg
      data-slot="overlay-arrow"
      fill="none"
      height="12"
      viewBox="0 0 12 12"
      width="12"
      xmlns="http://www.w3.org/2000/svg"
    >
      <path d="M0 0C5.48483 8 6.5 8 12 0Z" />
    </svg>
  );
  const arrow = isValidElement(children)
    ? cloneElement(children as React.ReactElement<{ "data-slot"?: string }>, {
        "data-slot": "overlay-arrow",
      })
    : defaultArrow;
  return (
    <OverlayArrow data-slot="tooltip-arrow" {...props} className={className}>
      {arrow}
    </OverlayArrow>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Tooltip Trigger
 * -----------------------------------------------------------------------------------------------*/

interface TriggerCaptureProps {
  ref?: Ref<HTMLDivElement>;
  className?: string;
  "data-slot"?: string;
  role?: string;
  user?: Record<string, unknown>;
  kids?: ReactNode;
  [key: string]: unknown;
}

/**
 * Focusable 会把 focusableProps 与子元素 props 合并后 clone 回子元素，合并结果的键序是
 * [子元素原键, 追加的 focusable 键]；heroui 的 DOM 序是 [静态键, ...mergeProps(focusable,
 * 用户 props)]。这里把 user/kids 单列成具名 prop，在捕获后按 heroui 的顺序重组再渲染 div，
 * 保证 SSR 字符串逐字节一致。
 */
const TooltipTriggerCapture = (props: TriggerCaptureProps) => {
  const {
    ref,
    className,
    role,
    user,
    kids,
    "data-slot": dataSlot,
    ...rest
  } = props;
  const merged = mergeProps(rest, user ?? {});
  return (
    <div
      ref={ref}
      className={className}
      data-slot={dataSlot}
      role={role}
      {...(merged as unknown as React.HTMLAttributes<HTMLDivElement>)}
      children={kids}
    />
  );
};

type TooltipTriggerProps = ComponentPropsWithRef<"div">;

const TooltipTrigger = ({
  children,
  className,
  ...props
}: TooltipTriggerProps) => {
  const { slots } = use(TooltipContext);
  // Focusable 消费 RAC TooltipTrigger 提供的 FocusableProvider 上下文，产出与 heroui
  // useFocusable({}, triggerRef) 等价的 focusableProps（tabIndex、hover/focus 处理器…）。
  return (
    <Focusable>
      <TooltipTriggerCapture
        className={slotClass(slots?.trigger, className)}
        data-slot="tooltip-trigger"
        role="button"
        user={props as Record<string, unknown>}
        kids={children}
      />
    </Focusable>
  );
};

export { TooltipRoot, TooltipTrigger, TooltipContent, TooltipArrow };

/** 复合组件形状与 @heroui/react 的 tooltip/index.js 一致。 */
export const Tooltip = Object.assign(TooltipRoot, {
  Root: TooltipRoot,
  Trigger: TooltipTrigger,
  Content: TooltipContent,
  Arrow: TooltipArrow,
});

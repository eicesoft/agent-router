import type {
  ComponentPropsWithRef,
  CSSProperties,
  ReactElement,
  ReactNode,
  Ref,
  SVGProps,
} from "react";
import {
  Children,
  cloneElement,
  createContext,
  isValidElement,
  use,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import { SelectionIndicator } from "react-aria-components/SelectionIndicator";
import {
  Tab as TabPrimitive,
  TabList as TabListPrimitive,
  TabPanel as TabPanelPrimitive,
  Tabs as TabsPrimitive,
} from "react-aria-components/Tabs";
import { dom } from "./dom";
import { IconChevronDown, IconChevronRight, IconChevronUp } from "./icons";
import { composeTwRenderProps, cx } from "./utils";

/**
 * 共享 icons.tsx 的 IconChevronLeft 路径与 @heroui 源码不一致（少 "-4.5" 折点与末段
 * 弧线），此处内联 @heroui 的正确路径以保证 SSR DOM 逐字节一致；icons.tsx 修复后可换回。
 */
function IconChevronLeftFixed(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      aria-hidden="true"
      fill="none"
      height={16}
      role="presentation"
      viewBox="0 0 16 16"
      width={16}
      xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <path
        clipRule="evenodd"
        d="M10.53 2.97a.75.75 0 0 1 0 1.06L6.56 8l3.97 3.97a.75.75 0 1 1-1.06 1.06l-4.5-4.5a.75.75 0 0 1 0-1.06l4.5-4.5a.75.75 0 0 1 1.06 0"
        fill="currentColor"
        fillRule="evenodd"
      />
    </svg>
  );
}

/* -------------------------------------------------------------------------------------------------
 * 类名映射（@heroui/styles 的 tabsVariants）
 * -----------------------------------------------------------------------------------------------*/

export type TabsAlign = "start" | "center" | "end";
export type TabsVariant = "primary" | "secondary";

interface TabsSlots {
  base: string;
  scrollNext: string;
  scrollPrev: string;
  scroller: string;
  separator: string;
  tab: string;
  tabIndicator: string;
  tabList: string;
  tabListContainer: string;
  tabPanel: string;
}

/** 类名顺序对齐 @heroui/styles 的 tv()：base → align → variant。 */
function tabsSlots(align?: TabsAlign, variant?: TabsVariant): TabsSlots {
  return {
    base: cx(
      "tabs",
      align === "end"
        ? "tabs--align-end"
        : align === "start"
          ? "tabs--align-start"
          : undefined,
      variant === "secondary" ? "tabs--secondary" : undefined,
    ),
    scrollNext: "tabs__list-container__scroll-next",
    scrollPrev: "tabs__list-container__scroll-prev",
    scroller: "tabs__list-container__scroller",
    separator: "tabs__separator",
    tab: "tabs__tab",
    tabIndicator: "tabs__indicator",
    tabList: "tabs__list",
    tabListContainer: "tabs__list-container",
    tabPanel: "tabs__panel",
  };
}

type Orientation = "horizontal" | "vertical";

const TabsContext = createContext<{
  orientation?: Orientation;
  slots?: TabsSlots;
}>({ orientation: "horizontal", slots: tabsSlots() });

/* -------------------------------------------------------------------------------------------------
 * Tabs Collection Slot（从 utils/collection-prop-injection.js 最小化移植）
 * -----------------------------------------------------------------------------------------------*/

const listContainerKey = "$$agent-router.collection.tabs.listContainer";

type ListContainerInjected = Record<string, unknown> & {
  className?: string;
  render?: (props: Record<string, unknown>, _: undefined) => ReactNode;
};

const ListContainerContext = createContext<ListContainerInjected | undefined>(
  undefined,
);

function ListContainerInjector({
  children,
  ...value
}: ListContainerInjected & { children?: ReactNode }) {
  const inject = (node: ReactNode): ReactNode =>
    Children.map(node, (child) =>
      isValidElement(child)
        ? cloneElement(child as ReactElement<Record<string, unknown>>, {
            [listContainerKey]: value,
          })
        : child,
    );
  return (
    <ListContainerContext value={value}>
      {inject(children)}
    </ListContainerContext>
  );
}

function useListSlot<T extends object>(
  props: T,
): [ListContainerInjected | undefined, T] {
  const injected = (props as any)[listContainerKey] as
    ListContainerInjected | undefined;
  const rest: any = { ...props };
  delete rest[listContainerKey];
  const fromContext = useContext(ListContainerContext);
  return [injected ?? fromContext, rest as T];
}

/* -------------------------------------------------------------------------------------------------
 * Tabs Root
 * -----------------------------------------------------------------------------------------------*/

export interface TabsRootProps extends ComponentPropsWithRef<
  typeof TabsPrimitive
> {
  children: ReactNode;
  align?: TabsAlign;
  variant?: TabsVariant;
}

export function TabsRoot({
  align,
  children,
  className,
  orientation = "horizontal",
  variant,
  ...props
}: TabsRootProps) {
  const slots = useMemo(() => tabsSlots(align, variant), [align, variant]);
  return (
    <TabsContext value={{ orientation, slots }}>
      <TabsPrimitive
        {...props}
        className={composeTwRenderProps(className, slots.base)}
        data-slot="tabs"
        orientation={orientation}
      >
        {children}
      </TabsPrimitive>
    </TabsContext>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tabs List Container
 * -----------------------------------------------------------------------------------------------*/

export interface TabListContainerProps extends ComponentPropsWithRef<"div"> {
  children?: ReactNode;
  className?: string;
  /** Overrides the container element rendering (与 @heroui 的 DOMRenderFunction 同形). */
  render?: (
    props: React.JSX.IntrinsicElements["div"],
    renderProps: unknown,
  ) => ReactElement;
}

export function TabListContainer({
  children,
  className,
  render,
  ...containerProps
}: TabListContainerProps) {
  return (
    <ListContainerInjector
      {...containerProps}
      className={className}
      render={
        render as unknown as (
          props: Record<string, unknown>,
          _: undefined,
        ) => ReactNode
      }
    >
      {children}
    </ListContainerInjector>
  );
}

/* -------------------------------------------------------------------------------------------------
 * ScrollShadow（从 scroll-shadow 移植；仅 tabs.listContainer 使用）
 * -----------------------------------------------------------------------------------------------*/

interface ScrollShadowProps {
  children?: ReactNode;
  className?: string;
  hideScrollBar?: boolean;
  isEnabled?: boolean;
  offset?: number;
  onVisibilityChange?: (visibility: string) => void;
  orientation?: Orientation;
  ref?: Ref<HTMLDivElement>;
  size?: number;
  style?: CSSProperties;
  variant?: "fade";
  visibility?: string;
}

const useSafeLayoutEffect =
  typeof window !== "undefined" ? useLayoutEffect : useEffect;

function useScrollShadow(props: {
  containerRef: { current: HTMLDivElement | null };
  isEnabled: boolean;
  offset: number;
  onVisibilityChange?: (visibility: string) => void;
  orientation: Orientation;
  visibility: string;
}) {
  const {
    containerRef,
    isEnabled,
    offset,
    onVisibilityChange,
    orientation,
    visibility,
  } = props;

  const onVisibilityChangeRef = useRef(onVisibilityChange);
  useEffect(() => {
    onVisibilityChangeRef.current = onVisibilityChange;
  }, [onVisibilityChange]);

  const prevStateRef = useRef<{
    hasScrollBefore: boolean;
    hasScrollAfter: boolean;
  } | null>(null);
  const rafIdRef = useRef<number | null>(null);

  const checkOverflow = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const isVertical = orientation === "vertical";
    const scrollStart = isVertical ? el.scrollTop : Math.abs(el.scrollLeft);
    const scrollSize = isVertical ? el.scrollHeight : el.scrollWidth;
    const clientSize = isVertical ? el.clientHeight : el.clientWidth;
    const hasScrollBefore = scrollStart > offset;
    const hasScrollAfter = scrollStart + clientSize + offset < scrollSize - 1;

    const prevState = prevStateRef.current;
    if (
      prevState &&
      prevState.hasScrollBefore === hasScrollBefore &&
      prevState.hasScrollAfter === hasScrollAfter
    ) {
      return;
    }
    prevStateRef.current = { hasScrollBefore, hasScrollAfter };

    if (rafIdRef.current !== null) {
      cancelAnimationFrame(rafIdRef.current);
    }
    rafIdRef.current = requestAnimationFrame(() => {
      rafIdRef.current = null;
      const notify = onVisibilityChangeRef.current;
      const dataset = el.dataset as Record<string, string | undefined>;
      if (isVertical) {
        if (hasScrollBefore && hasScrollAfter) {
          dataset["topBottomScroll"] = "true";
          delete dataset["topScroll"];
          delete dataset["bottomScroll"];
          notify?.("both");
        } else {
          dataset["topScroll"] = String(hasScrollBefore);
          dataset["bottomScroll"] = String(hasScrollAfter);
          delete dataset["topBottomScroll"];
          if (notify) {
            if (hasScrollBefore) notify("top");
            else if (hasScrollAfter) notify("bottom");
            else notify("none");
          }
        }
      } else {
        if (hasScrollBefore && hasScrollAfter) {
          dataset["leftRightScroll"] = "true";
          delete dataset["leftScroll"];
          delete dataset["rightScroll"];
          notify?.("both");
        } else {
          dataset["leftScroll"] = String(hasScrollBefore);
          dataset["rightScroll"] = String(hasScrollAfter);
          delete dataset["leftRightScroll"];
          if (notify) {
            if (hasScrollBefore) notify("left");
            else if (hasScrollAfter) notify("right");
            else notify("none");
          }
        }
      }
    });
  }, [containerRef, orientation, offset]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el || !isEnabled || visibility !== "auto") return;

    checkOverflow();
    el.addEventListener("scroll", checkOverflow, { passive: true });
    const resizeObserver = new ResizeObserver(checkOverflow);
    resizeObserver.observe(el);
    const mutationObserver = new MutationObserver(checkOverflow);
    mutationObserver.observe(el, {
      attributeFilter: ["class", "style"],
      attributes: true,
      characterData: true,
      childList: true,
      subtree: true,
    });
    return () => {
      el.removeEventListener("scroll", checkOverflow);
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current);
        rafIdRef.current = null;
      }
      prevStateRef.current = null;
    };
  }, [containerRef, visibility, isEnabled, checkOverflow]);
}

function ScrollShadow({
  children,
  className,
  hideScrollBar = false,
  isEnabled = true,
  offset = 0,
  onVisibilityChange,
  orientation = "vertical",
  ref,
  size = 40,
  style: styleProp,
  variant = "fade",
  visibility = "auto",
  ...props
}: ScrollShadowProps) {
  const internalRef = useRef<HTMLDivElement | null>(null);
  useScrollShadow({
    containerRef: internalRef,
    isEnabled,
    offset,
    onVisibilityChange,
    orientation,
    visibility,
  });

  // Handle controlled visibility mode
  useSafeLayoutEffect(() => {
    const el = internalRef.current;
    if (!el || visibility === "auto") return;
    const dataset = el.dataset as Record<string, string | undefined>;
    delete dataset["topScroll"];
    delete dataset["bottomScroll"];
    delete dataset["topBottomScroll"];
    delete dataset["leftScroll"];
    delete dataset["rightScroll"];
    delete dataset["leftRightScroll"];
    if (visibility === "both") {
      dataset[
        orientation === "vertical" ? "topBottomScroll" : "leftRightScroll"
      ] = "true";
    } else if (visibility !== "none") {
      dataset[`${visibility}Scroll`] = "true";
    }
  }, [visibility, orientation]);

  const slots = useMemo(
    () =>
      cx(
        "scroll-shadow",
        hideScrollBar ? "scroll-shadow--hide-scrollbar" : undefined,
        orientation === "horizontal"
          ? "scroll-shadow--horizontal"
          : orientation === "vertical"
            ? "scroll-shadow--vertical"
            : undefined,
        variant === "fade" ? "scroll-shadow--fade" : undefined,
      ),
    [orientation, hideScrollBar, variant],
  );
  const style = {
    "--scroll-shadow-size": `${size}px`,
    "--scroll-shadow-offset": `${offset}px`,
    ...styleProp,
  } as CSSProperties;
  const setRef = (node: HTMLDivElement | null) => {
    internalRef.current = node;
    if (typeof ref === "function") ref(node);
    else if (ref) (ref as { current: HTMLDivElement | null }).current = node;
  };
  return (
    <div
      ref={setRef}
      className={cx(slots, className)}
      data-orientation={orientation}
      // Selects the scroll-driven fade where supported. Controlled and disabled modes stay
      // attribute-driven, matching what the hook does for the same combination of props.
      data-scroll-shadow-mode={
        isEnabled && visibility === "auto" ? "auto" : "manual"
      }
      data-scroll-shadow-size={size}
      data-slot="scroll-shadow"
      style={style}
      {...(props as Record<string, unknown>)}
    >
      {children}
    </div>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tabs List
 * -----------------------------------------------------------------------------------------------*/

export interface TabListProps extends ComponentPropsWithRef<
  typeof TabListPrimitive<object>
> {
  children: ReactNode;
  className?: string;
}

export function TabList({ children, className, ...props }: TabListProps) {
  const { orientation = "horizontal", slots } = use(TabsContext);
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const isVertical = orientation === "vertical";
  const [listContainerProps, restProps] = useListSlot(props);
  const scrollBy = useCallback(
    (direction: number) => {
      const el = scrollerRef.current;
      if (!el) return;
      const size = isVertical ? el.clientHeight : el.clientWidth;
      const scrollSize = isVertical ? el.scrollHeight : el.scrollWidth;
      const maxScroll = Math.max(0, scrollSize - size);

      // In RTL, the horizontal scroll range runs from 0 (start, on the right) to negative,
      // so the delta sign must be flipped for `scrollLeft` to move toward the intended edge.
      const isRTL = !isVertical && getComputedStyle(el).direction === "rtl";
      const delta = direction * size * 0.8 * (isRTL ? -1 : 1);
      const current = isVertical ? el.scrollTop : el.scrollLeft;

      // Clamp the target to the scrollable range so a press near the edge lands
      // flush on it instead of requesting a position past the content, which can
      // cut the smooth scroll short of the edge (e.g. iOS Safari) or leave the
      // strip visually stranded. RTL ranges run [-maxScroll, 0].
      const next = Math.min(
        isRTL ? 0 : maxScroll,
        Math.max(isRTL ? -maxScroll : 0, current + delta),
      );
      if (next === current) return;
      el.scrollTo({
        behavior: "smooth",
        [isVertical ? "top" : "left"]: next,
      });
    },
    [isVertical],
  );

  // Without ListContainer, stay a thin RAC TabList
  if (!listContainerProps) {
    return (
      <TabListPrimitive
        {...restProps}
        className={composeTwRenderProps(className, slots?.tabList)}
        data-slot="tabs-list"
      >
        {children}
      </TabListPrimitive>
    );
  }
  const {
    className: containerClassName,
    render: containerRender,
    ...containerRest
  } = listContainerProps;
  return (
    <TabListPrimitive
      {...restProps}
      className={composeTwRenderProps(className, slots?.tabList)}
      data-slot="tabs-list"
      render={(renderProps) => {
        const {
          children: listChildren,
          className: listClassName,
          ref: listRef,
          ...listRest
        } = renderProps;
        return (
          <dom.div
            className={cx(slots?.tabListContainer, containerClassName)}
            data-slot="tabs-list-container"
            render={containerRender}
            {...containerRest}
          >
            <ScrollShadow
              ref={scrollerRef}
              hideScrollBar
              className={slots?.scroller}
              orientation={orientation}
              size={64}
            >
              <div {...listRest} ref={listRef} className={listClassName}>
                {listChildren}
              </div>
            </ScrollShadow>
            <button
              aria-label={isVertical ? "Scroll tabs up" : "Scroll tabs left"}
              className={slots?.scrollPrev}
              tabIndex={-1}
              type="button"
              onClick={() => scrollBy(-1)}
            >
              {isVertical ? <IconChevronUp /> : <IconChevronLeftFixed />}
            </button>
            <button
              aria-label={isVertical ? "Scroll tabs down" : "Scroll tabs right"}
              className={slots?.scrollNext}
              tabIndex={-1}
              type="button"
              onClick={() => scrollBy(1)}
            >
              {isVertical ? <IconChevronDown /> : <IconChevronRight />}
            </button>
          </dom.div>
        );
      }}
    >
      {children}
    </TabListPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tab
 * -----------------------------------------------------------------------------------------------*/

export interface TabProps extends ComponentPropsWithRef<typeof TabPrimitive> {
  children?: ReactNode;
  className?: string;
}

export function Tab({ children, className, ...props }: TabProps) {
  const { slots } = use(TabsContext);
  return (
    <TabPrimitive
      {...props}
      className={composeTwRenderProps(className, slots?.tab)}
      data-slot="tabs-tab"
    >
      {children}
    </TabPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tab Indicator
 * -----------------------------------------------------------------------------------------------*/

export interface TabIndicatorProps extends ComponentPropsWithRef<
  typeof SelectionIndicator
> {
  className?: string;
}

export function TabIndicator({ className, ...props }: TabIndicatorProps) {
  const { slots } = use(TabsContext);
  return (
    <SelectionIndicator
      className={cx(slots?.tabIndicator, className)}
      data-slot="tabs-indicator"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tab Panel
 * -----------------------------------------------------------------------------------------------*/

export interface TabPanelProps extends Omit<
  ComponentPropsWithRef<typeof TabPanelPrimitive>,
  "children"
> {
  children: ReactNode;
  className?: string;
}

export function TabPanel({ children, className, ...props }: TabPanelProps) {
  const { slots } = use(TabsContext);
  return (
    <TabPanelPrimitive
      {...props}
      className={composeTwRenderProps(className, slots?.tabPanel)}
      data-slot="tabs-panel"
    >
      {children}
    </TabPanelPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Tab Separator
 * -----------------------------------------------------------------------------------------------*/

export interface TabSeparatorProps extends ComponentPropsWithRef<"span"> {
  className?: string;
}

export function TabSeparator({ className, ...props }: TabSeparatorProps) {
  const { slots } = use(TabsContext);
  return (
    <dom.span
      aria-hidden="true"
      className={cx(slots?.separator, className)}
      data-slot="tabs-separator"
      {...props}
    />
  );
}

export const Tabs = Object.assign(TabsRoot, {
  Root: TabsRoot,
  ListContainer: TabListContainer,
  List: TabList,
  Tab,
  Indicator: TabIndicator,
  Separator: TabSeparator,
  Panel: TabPanel,
});

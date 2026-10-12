import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  type ComponentProps,
  type Ref,
  type RefObject,
} from "react";
import { cx } from "./utils";

/** 从 @heroui/react 的 scroll-shadow/use-scroll-shadow.js 移植。 */
export type ScrollShadowVisibility =
  "auto" | "both" | "top" | "bottom" | "left" | "right" | "none";

export type ScrollShadowOrientation = "horizontal" | "vertical";
export type ScrollShadowVariant = "fade";
export type ScrollShadowScrollBar = boolean;

export interface ScrollShadowRootProps extends Omit<
  ComponentProps<"div">,
  "size"
> {
  hideScrollBar?: ScrollShadowScrollBar;
  /** @default 40 */
  size?: number;
  /** @default 0 */
  offset?: number;
  /** @default "auto" */
  visibility?: ScrollShadowVisibility;
  /** @default true */
  isEnabled?: boolean;
  orientation?: ScrollShadowOrientation;
  variant?: ScrollShadowVariant;
  onVisibilityChange?: (visibility: ScrollShadowVisibility) => void;
}

interface UseScrollShadowProps {
  containerRef: RefObject<HTMLDivElement | null>;
  isEnabled: boolean;
  offset: number;
  onVisibilityChange?: (visibility: ScrollShadowVisibility) => void;
  orientation: ScrollShadowOrientation;
  visibility: ScrollShadowVisibility;
}

function mergeRefs<T>(
  ...refs: Array<Ref<T> | undefined | null>
): (node: T | null) => void {
  return (node) => {
    for (const ref of refs) {
      if (typeof ref === "function") ref(node);
      else if (ref && typeof ref === "object")
        (ref as MutableRefObjectLike<T>).current = node;
    }
  };
}

type MutableRefObjectLike<T> = { current: T | null };

// heroui 的 hooks/use-safe-layout-effect：浏览器用 layout effect，SSR 退回 effect。
const useSafeLayoutEffect =
  typeof document !== "undefined" ? useLayoutEffect : useEffect;

export function useScrollShadow({
  containerRef,
  isEnabled,
  offset,
  onVisibilityChange,
  orientation,
  visibility,
}: UseScrollShadowProps) {
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
      if (isVertical) {
        if (hasScrollBefore && hasScrollAfter) {
          el.dataset["topBottomScroll"] = "true";
          delete el.dataset["topScroll"];
          delete el.dataset["bottomScroll"];
          notify?.("both");
        } else {
          el.dataset["topScroll"] = String(hasScrollBefore);
          el.dataset["bottomScroll"] = String(hasScrollAfter);
          delete el.dataset["topBottomScroll"];
          if (notify) {
            if (hasScrollBefore) {
              notify("top");
            } else if (hasScrollAfter) {
              notify("bottom");
            } else {
              notify("none");
            }
          }
        }
      } else {
        if (hasScrollBefore && hasScrollAfter) {
          el.dataset["leftRightScroll"] = "true";
          delete el.dataset["leftScroll"];
          delete el.dataset["rightScroll"];
          notify?.("both");
        } else {
          el.dataset["leftScroll"] = String(hasScrollBefore);
          el.dataset["rightScroll"] = String(hasScrollAfter);
          delete el.dataset["leftRightScroll"];
          if (notify) {
            if (hasScrollBefore) {
              notify("left");
            } else if (hasScrollAfter) {
              notify("right");
            } else {
              notify("none");
            }
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

/** 与 @heroui/styles 的 scrollShadowVariants 同值的 BEM 类名映射。 */
function scrollShadowClass(
  hideScrollBar: boolean,
  orientation: ScrollShadowOrientation,
  variant: ScrollShadowVariant,
): string {
  return cx(
    "scroll-shadow",
    hideScrollBar ? "scroll-shadow--hide-scrollbar" : undefined,
    orientation === "horizontal"
      ? "scroll-shadow--horizontal"
      : "scroll-shadow--vertical",
    variant === "fade" ? "scroll-shadow--fade" : undefined,
  );
}

/** 从 @heroui/react 的 scroll-shadow/scroll-shadow.js 移植。 */
export function ScrollShadowRoot({
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
}: ScrollShadowRootProps) {
  const internalRef = useRef<HTMLDivElement | null>(null);
  useScrollShadow({
    containerRef: internalRef,
    isEnabled,
    offset,
    onVisibilityChange,
    orientation,
    visibility,
  });

  // 受控可见性模式：清掉 hook 写入的 data-* 后按 visibility 重设。
  useSafeLayoutEffect(() => {
    const el = internalRef.current;
    if (!el || visibility === "auto") return;

    delete el.dataset["topScroll"];
    delete el.dataset["bottomScroll"];
    delete el.dataset["topBottomScroll"];
    delete el.dataset["leftScroll"];
    delete el.dataset["rightScroll"];
    delete el.dataset["leftRightScroll"];

    if (visibility === "both") {
      el.dataset[
        orientation === "vertical" ? "topBottomScroll" : "leftRightScroll"
      ] = "true";
    } else if (visibility !== "none") {
      el.dataset[`${visibility}Scroll`] = "true";
    }
  }, [visibility, orientation]);

  const own = useMemo(
    () => scrollShadowClass(hideScrollBar, orientation, variant),
    [hideScrollBar, orientation, variant],
  );
  const style = {
    "--scroll-shadow-size": `${size}px`,
    "--scroll-shadow-offset": `${offset}px`,
    ...styleProp,
  } as React.CSSProperties & Record<string, string | number | undefined>;
  return (
    <div
      ref={mergeRefs(
        internalRef,
        ref as Ref<HTMLDivElement> | null | undefined,
      )}
      className={cx(own, className)}
      data-orientation={orientation}
      data-scroll-shadow-mode={
        isEnabled && visibility === "auto" ? "auto" : "manual"
      }
      data-scroll-shadow-size={size}
      data-slot="scroll-shadow"
      style={style}
      {...props}
      children={children}
    />
  );
}
ScrollShadowRoot.displayName = "AgentRouter.ScrollShadow";

export const ScrollShadow = Object.assign(ScrollShadowRoot, {
  Root: ScrollShadowRoot,
});

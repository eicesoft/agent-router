import type { ComponentPropsWithRef, ReactNode, SVGProps } from "react";
import { createContext, useContext, useMemo } from "react";
import { Button as ButtonPrimitive } from "react-aria-components/Button";
import { dom } from "./dom";
import { IconChevronRight } from "./icons";
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

/** 与 @heroui/styles 的 paginationVariants 同值的 BEM 类名映射。 */
export type PaginationSize = "sm" | "md" | "lg";

const sizeClass: Record<PaginationSize, string> = {
  lg: "pagination--lg",
  md: "pagination--md",
  sm: "pagination--sm",
};

interface PaginationSlots {
  base: string;
  content: string;
  ellipsis: string;
  item: string;
  link: string;
  summary: string;
}

/** 类名顺序对齐 @heroui/styles 的 tv()：base → size。 */
function paginationSlots(size?: PaginationSize): PaginationSlots {
  return {
    base: cx("pagination", sizeClass[size ?? "md"]),
    content: "pagination__content",
    ellipsis: "pagination__ellipsis",
    item: "pagination__item",
    link: "pagination__link",
    summary: "pagination__summary",
  };
}

const PaginationContext = createContext<{ slots?: PaginationSlots }>({
  slots: paginationSlots(),
});

export interface PaginationRootProps extends ComponentPropsWithRef<"nav"> {
  children: ReactNode;
  /** Pagination size. */
  size?: PaginationSize;
}

function PaginationRoot({
  children,
  className,
  size,
  ...props
}: PaginationRootProps) {
  const slots = useMemo(() => paginationSlots(size), [size]);
  return (
    <PaginationContext value={{ slots }}>
      <dom.nav
        aria-label="pagination"
        data-slot="pagination"
        role="navigation"
        {...props}
        className={cx(slots.base, className)}
      >
        {children}
      </dom.nav>
    </PaginationContext>
  );
}
PaginationRoot.displayName = "AgentRouter.Pagination";

export interface PaginationSummaryProps extends ComponentPropsWithRef<"div"> {
  children: ReactNode;
}

function PaginationSummary({
  children,
  className,
  ...props
}: PaginationSummaryProps) {
  const { slots } = useContext(PaginationContext);
  return (
    <dom.div
      className={cx(slots?.summary, className)}
      data-slot="pagination-summary"
      // Auto-detect base direction from the summary's own text so mixed
      // number/word content (e.g. "1 to 5 of 10 invoices") is not bidi-reordered
      // by an RTL ancestor. User-provided `dir` still overrides via `...props`.
      dir="auto"
      {...props}
    >
      {children}
    </dom.div>
  );
}
PaginationSummary.displayName = "AgentRouter.Pagination.Summary";

export interface PaginationContentProps extends ComponentPropsWithRef<"ul"> {
  children: ReactNode;
}

function PaginationContent({
  children,
  className,
  ...props
}: PaginationContentProps) {
  const { slots } = useContext(PaginationContext);
  return (
    <dom.ul
      className={cx(slots?.content, className)}
      data-slot="pagination-content"
      {...props}
    >
      {children}
    </dom.ul>
  );
}
PaginationContent.displayName = "AgentRouter.Pagination.Content";

export interface PaginationItemProps extends ComponentPropsWithRef<"li"> {
  children: ReactNode;
}

function PaginationItem({
  children,
  className,
  ...props
}: PaginationItemProps) {
  const { slots } = useContext(PaginationContext);
  return (
    <dom.li
      className={cx(slots?.item, className)}
      data-slot="pagination-item"
      {...props}
    >
      {children}
    </dom.li>
  );
}
PaginationItem.displayName = "AgentRouter.Pagination.Item";

export interface PaginationLinkProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  children: ReactNode;
  isActive?: boolean;
}

function PaginationLink({
  children,
  className,
  isActive,
  ...props
}: PaginationLinkProps) {
  const { slots } = useContext(PaginationContext);
  return (
    <ButtonPrimitive
      aria-current={isActive ? "page" : undefined}
      className={composeTwRenderProps(className, slots?.link)}
      data-active={isActive ? "true" : undefined}
      data-slot="pagination-link"
      {...props}
    >
      {children}
    </ButtonPrimitive>
  );
}
PaginationLink.displayName = "AgentRouter.Pagination.Link";

export interface PaginationPreviousProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  children: ReactNode;
}

function PaginationPrevious({
  children,
  className,
  ...props
}: PaginationPreviousProps) {
  const { slots } = useContext(PaginationContext);
  const baseClass = `${slots?.link ?? ""} pagination__link--nav`.trim();
  return (
    <ButtonPrimitive
      className={composeTwRenderProps(className, baseClass)}
      data-slot="pagination-previous"
      {...props}
    >
      {children}
    </ButtonPrimitive>
  );
}
PaginationPrevious.displayName = "AgentRouter.Pagination.Previous";

export interface PaginationPreviousIconProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

function PaginationPreviousIcon({
  children,
  className,
  ...props
}: PaginationPreviousIconProps) {
  return (
    <dom.span
      aria-hidden="true"
      className={className}
      data-slot="pagination-previous-icon"
      {...props}
    >
      {children ?? <IconChevronLeftFixed />}
    </dom.span>
  );
}
PaginationPreviousIcon.displayName = "AgentRouter.Pagination.PreviousIcon";

export interface PaginationNextProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  children: ReactNode;
}

function PaginationNext({
  children,
  className,
  ...props
}: PaginationNextProps) {
  const { slots } = useContext(PaginationContext);
  const baseClass = `${slots?.link ?? ""} pagination__link--nav`.trim();
  return (
    <ButtonPrimitive
      className={composeTwRenderProps(className, baseClass)}
      data-slot="pagination-next"
      {...props}
    >
      {children}
    </ButtonPrimitive>
  );
}
PaginationNext.displayName = "AgentRouter.Pagination.Next";

export interface PaginationNextIconProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

function PaginationNextIcon({
  children,
  className,
  ...props
}: PaginationNextIconProps) {
  return (
    <dom.span
      aria-hidden="true"
      className={className}
      data-slot="pagination-next-icon"
      {...props}
    >
      {children ?? <IconChevronRight />}
    </dom.span>
  );
}
PaginationNextIcon.displayName = "AgentRouter.Pagination.NextIcon";

export interface PaginationEllipsisProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

function PaginationEllipsis({ className, ...props }: PaginationEllipsisProps) {
  const { slots } = useContext(PaginationContext);
  return (
    <dom.span
      aria-hidden="true"
      className={cx(slots?.ellipsis, className)}
      data-slot="pagination-ellipsis"
      {...props}
    >
      {"…"}
    </dom.span>
  );
}
PaginationEllipsis.displayName = "AgentRouter.Pagination.Ellipsis";

export const Pagination = Object.assign(PaginationRoot, {
  Content: PaginationContent,
  Ellipsis: PaginationEllipsis,
  Item: PaginationItem,
  Link: PaginationLink,
  Next: PaginationNext,
  NextIcon: PaginationNextIcon,
  Previous: PaginationPrevious,
  PreviousIcon: PaginationPreviousIcon,
  Root: PaginationRoot,
  Summary: PaginationSummary,
});

export {
  PaginationRoot,
  PaginationSummary,
  PaginationContent,
  PaginationItem,
  PaginationLink,
  PaginationPrevious,
  PaginationPreviousIcon,
  PaginationNext,
  PaginationNextIcon,
  PaginationEllipsis,
};

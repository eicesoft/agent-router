import {
  createContext,
  use,
  type ComponentPropsWithRef,
  type ReactNode,
} from "react";
import {
  ListBox as ListBoxPrimitive,
  ListBoxItem as ListBoxItemPrimitive,
  ListBoxSection as ListBoxSectionPrimitive,
  type ListBoxItemRenderProps,
} from "react-aria-components";
import { dom } from "./dom";
import { composeTwRenderProps, cx, dataAttr } from "./utils";

/** 与 @heroui/styles 的 listboxVariants / listboxItemVariants 同值的 BEM 类名。 */
export type ListBoxVariant = "default" | "danger";

export interface ListBoxRootProps<
  T extends object = object,
> extends ComponentPropsWithRef<typeof ListBoxPrimitive<T>> {
  className?: string;
  variant?: ListBoxVariant | null;
}

export function ListBoxRoot<T extends object = object>({
  className,
  variant,
  ...props
}: ListBoxRootProps<T>) {
  // listboxVariants 无 slots：base → variant（默认 default）。
  const styles = cx(
    "list-box",
    variant === "danger" ? "list-box--danger" : "list-box--default",
  );
  return (
    <ListBoxPrimitive
      className={composeTwRenderProps(className, styles)}
      data-slot="list-box"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * ListBox Item
 * -----------------------------------------------------------------------------------------------*/

interface ListBoxItemContextValue {
  slots?: { item: string; indicator: string };
  state?: ListBoxItemRenderProps;
}

const ListBoxItemContext = createContext<ListBoxItemContextValue>({});

export interface ListBoxItemRootProps extends ComponentPropsWithRef<
  typeof ListBoxItemPrimitive
> {
  className?: string;
  variant?: ListBoxVariant | null;
}

export function ListBoxItemRoot({
  children,
  className,
  variant,
  ...props
}: ListBoxItemRootProps) {
  const slots = {
    // listboxItemVariants 无 slots：base → variant（默认 default）。
    item: cx(
      "list-box-item",
      variant === "danger" ? "list-box-item--danger" : "list-box-item--default",
    ),
    indicator: "list-box-item__indicator",
  };
  return (
    <ListBoxItemPrimitive
      className={composeTwRenderProps(className, slots.item)}
      data-slot="list-box-item"
      {...props}
    >
      {(values) => (
        <ListBoxItemContext value={{ slots, state: values }}>
          {typeof children === "function" ? children(values) : children}
        </ListBoxItemContext>
      )}
    </ListBoxItemPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * ListBox Item Indicator
 * -----------------------------------------------------------------------------------------------*/

export type ListBoxItemIndicatorProps = {
  children?: ReactNode | ((props: ListBoxItemRenderProps) => ReactNode);
  className?: string;
} & Omit<ComponentPropsWithRef<"span">, "children" | "className">;

export function ListBoxItemIndicator({
  children,
  className,
  ...props
}: ListBoxItemIndicatorProps) {
  const { slots, state } = use(ListBoxItemContext);
  const isSelected = state?.isSelected;
  const content =
    typeof children === "function" ? (
      children(state ?? ({} as ListBoxItemRenderProps))
    ) : children ? (
      children
    ) : (
      <svg
        aria-hidden="true"
        data-slot="list-box-item-indicator--checkmark"
        fill="none"
        role="presentation"
        stroke="currentColor"
        strokeDasharray={22}
        strokeDashoffset={isSelected ? 44 : 66}
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        viewBox="0 0 17 18"
      >
        <polyline points="1 9 7 14 15 4" />
      </svg>
    );
  return (
    <dom.span
      aria-hidden="true"
      className={cx(slots?.indicator, className)}
      data-slot="list-box-item-indicator"
      data-visible={isSelected || undefined}
      {...props}
    >
      {content}
    </dom.span>
  );
}

/* -------------------------------------------------------------------------------------------------
 * ListBox Section
 * -----------------------------------------------------------------------------------------------*/

export type ListBoxSectionRootProps = ComponentPropsWithRef<
  typeof ListBoxSectionPrimitive
>;

export function ListBoxSectionRoot({
  children,
  className,
  ...props
}: ListBoxSectionRootProps) {
  // 对齐 heroui：只有字符串 className 参与拼接（函数 className 会被 clsx 忽略）。
  const styles = cx(
    "list-box-section",
    typeof className === "string" ? className : undefined,
  );
  return (
    <ListBoxSectionPrimitive className={styles} {...props}>
      {children}
    </ListBoxSectionPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const ListBoxSection = ListBoxSectionRoot;

export const ListBoxItem = Object.assign(ListBoxItemRoot, {
  Root: ListBoxItemRoot,
  Indicator: ListBoxItemIndicator,
});

export const ListBox = Object.assign(ListBoxRoot, {
  Root: ListBoxRoot,
  Item: ListBoxItem,
  ItemIndicator: ListBoxItemIndicator,
  Section: ListBoxSection,
});

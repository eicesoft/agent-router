import {
  createContext,
  use,
  type ComponentPropsWithRef,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  CheckboxButton as CheckboxButtonPrimitive,
  CheckboxField as CheckboxFieldPrimitive,
  LabelContext,
  type CheckboxFieldRenderProps,
} from "react-aria-components";
import { CheckboxGroupContext } from "./checkbox-group";
import { composeTwRenderProps, cx } from "./utils";
import { dom } from "./dom";

/** 与 @heroui/styles 的 checkboxVariants 同值的变体名。 */
export type CheckboxVariant = "primary" | "secondary";

type DomRenderFunction = (
  props: React.JSX.IntrinsicElements["span"],
  renderProps: undefined,
) => ReactElement;

/** heroui utils/compose.js 的 composeSlotClassName 最小化内联。 */
function composeSlotClassName(
  slotFn: ((opts: { className?: string }) => string) | undefined,
  className?: string,
): string | undefined {
  return typeof slotFn === "function" ? slotFn({ className }) : className;
}

// 类名顺序对齐 @heroui/styles 的 checkboxVariants：base → variant（默认 primary）。
const checkboxSlots = (variant?: CheckboxVariant) => ({
  base: cx(
    "checkbox",
    variant === "secondary" ? "checkbox--secondary" : "checkbox--primary",
  ),
  content: "checkbox__content",
  control: (opts?: { className?: string }) =>
    cx("checkbox__control", opts?.className),
  indicator: (opts?: { className?: string }) =>
    cx("checkbox__indicator", opts?.className),
});

type CheckboxSlots = ReturnType<typeof checkboxSlots>;

const CheckboxContext = createContext<{
  slots?: CheckboxSlots;
  state?: CheckboxFieldRenderProps;
}>({});

export interface CheckboxRootProps extends ComponentPropsWithRef<
  typeof CheckboxFieldPrimitive
> {
  /** The name of the checkbox, used when submitting an HTML form. */
  name?: string;
  variant?: CheckboxVariant;
}

/** 与 @heroui/react 的 CheckboxRoot 对应：react-aria CheckboxField。 */
export function CheckboxRoot({
  children,
  className,
  variant,
  ...props
}: CheckboxRootProps) {
  const checkboxGroupContext = use(CheckboxGroupContext);
  const effectiveVariant = variant ?? checkboxGroupContext.variant;
  const slots = checkboxSlots(effectiveVariant);
  return (
    <CheckboxFieldPrimitive
      data-slot="checkbox"
      {...props}
      className={composeTwRenderProps(className, slots.base)}
      children={(state) => (
        <CheckboxContext value={{ slots, state }}>
          {typeof children === "function" ? children(state) : children}
        </CheckboxContext>
      )}
    />
  );
}

export interface CheckboxContentProps extends ComponentPropsWithRef<
  typeof CheckboxButtonPrimitive
> {}

/** 可点击的 label 区域；Label 在其中被渲染为 span。 */
export function CheckboxContent({
  children,
  className,
  ...props
}: CheckboxContentProps) {
  const { slots } = use(CheckboxContext);
  return (
    <CheckboxButtonPrimitive
      data-slot="checkbox-content"
      {...props}
      className={composeTwRenderProps(className, slots?.content)}
      children={(state) => (
        <LabelContext value={{ elementType: "span" }}>
          {typeof children === "function" ? children(state) : children}
        </LabelContext>
      )}
    />
  );
}

export type CheckboxControlProps = {
  children?: ReactNode;
  className?: string;
  render?: DomRenderFunction;
} & Record<string, unknown>;

export function CheckboxControl({
  children,
  className,
  ...props
}: CheckboxControlProps) {
  const { slots } = use(CheckboxContext);
  return (
    <dom.span
      className={composeSlotClassName(slots?.control, className)}
      data-slot="checkbox-control"
      {...props}
    >
      {children}
    </dom.span>
  );
}

export type CheckboxIndicatorProps = {
  children?: ReactNode | ((props: CheckboxFieldRenderProps) => ReactNode);
  className?: string;
  render?: DomRenderFunction;
} & Record<string, unknown>;

/** 默认指示图形与 heroui 逐属性一致：indeterminate 画横线，否则画对勾。 */
export function CheckboxIndicator({
  children,
  className,
  ...props
}: CheckboxIndicatorProps) {
  const { slots, state } = use(CheckboxContext);
  const isSelected = state?.isSelected;
  const isIndeterminate = state?.isIndeterminate;
  const content =
    typeof children === "function" ? (
      children(state ?? ({} as CheckboxFieldRenderProps))
    ) : children ? (
      children
    ) : isIndeterminate ? (
      <svg
        aria-hidden="true"
        data-slot="checkbox-default-indicator--indeterminate"
        fill="none"
        role="presentation"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth={3}
        viewBox="0 0 24 24"
      >
        <line x1="21" x2="3" y1="12" y2="12" />
      </svg>
    ) : (
      <svg
        aria-hidden="true"
        data-slot="checkbox-default-indicator--checkmark"
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
      className={composeSlotClassName(slots?.indicator, className)}
      data-slot="checkbox-indicator"
      {...props}
    >
      {content}
    </dom.span>
  );
}

export const Checkbox = Object.assign(CheckboxRoot, {
  Root: CheckboxRoot,
  Content: CheckboxContent,
  Control: CheckboxControl,
  Indicator: CheckboxIndicator,
});

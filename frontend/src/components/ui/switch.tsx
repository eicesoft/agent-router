import {
  createContext,
  use,
  type ComponentPropsWithRef,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  LabelContext,
  SwitchButton as SwitchButtonPrimitive,
  SwitchField as SwitchFieldPrimitive,
  type SwitchFieldRenderProps,
} from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";
import { dom } from "./dom";

/** 与 @heroui/styles 的 switchVariants 同值的尺寸。 */
export type SwitchSize = "sm" | "md" | "lg";

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

// 类名顺序对齐 @heroui/styles 的 switchVariants：base → size（默认 md）。
const sizeClass: Record<SwitchSize, string> = {
  sm: "switch--sm",
  md: "switch--md",
  lg: "switch--lg",
};

const switchSlots = (size?: SwitchSize) => ({
  base: cx("switch", sizeClass[size ?? "md"]),
  content: "switch__content",
  control: (opts?: { className?: string }) =>
    cx("switch__control", opts?.className),
  icon: (opts?: { className?: string }) => cx("switch__icon", opts?.className),
  thumb: (opts?: { className?: string }) =>
    cx("switch__thumb", opts?.className),
});

type SwitchSlots = ReturnType<typeof switchSlots>;

const SwitchContext = createContext<{
  slots?: SwitchSlots;
  state?: SwitchFieldRenderProps;
}>({});

export interface SwitchRootProps extends ComponentPropsWithRef<
  typeof SwitchFieldPrimitive
> {
  size?: SwitchSize;
}

/** 与 @heroui/react 的 SwitchRoot 对应：react-aria SwitchField。 */
export function SwitchRoot({
  children,
  className,
  size,
  ...props
}: SwitchRootProps) {
  const slots = switchSlots(size);
  return (
    <SwitchFieldPrimitive
      data-slot="switch"
      {...props}
      className={composeTwRenderProps(className, slots.base)}
      children={(state) => (
        <SwitchContext value={{ slots, state }}>
          {typeof children === "function" ? children(state) : children}
        </SwitchContext>
      )}
    />
  );
}

export interface SwitchContentProps extends ComponentPropsWithRef<
  typeof SwitchButtonPrimitive
> {}

/** 可点击的 label 区域；Label 在其中被渲染为 span。 */
export function SwitchContent({
  children,
  className,
  ...props
}: SwitchContentProps) {
  const { slots } = use(SwitchContext);
  return (
    <SwitchButtonPrimitive
      data-slot="switch-content"
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

export type SwitchControlProps = {
  children?: ReactNode;
  className?: string;
  render?: DomRenderFunction;
} & Record<string, unknown>;

/** 与 @heroui/react 的 SwitchControl 对应：dom.span 透传 render。 */
export function SwitchControl({
  children,
  className,
  ...props
}: SwitchControlProps) {
  const { slots } = use(SwitchContext);
  return (
    <dom.span
      className={composeSlotClassName(slots?.control, className)}
      data-slot="switch-control"
      {...props}
    >
      {children}
    </dom.span>
  );
}

export type SwitchThumbProps = {
  children?: ReactNode;
  className?: string;
  render?: DomRenderFunction;
} & Record<string, unknown>;

export function SwitchThumb({
  children,
  className,
  ...props
}: SwitchThumbProps) {
  const { slots } = use(SwitchContext);
  return (
    <dom.span
      className={composeSlotClassName(slots?.thumb, className)}
      data-slot="switch-thumb"
      {...props}
    >
      {children}
    </dom.span>
  );
}

export type SwitchIconProps = {
  children?: ReactNode;
  className?: string;
  render?: DomRenderFunction;
} & Record<string, unknown>;

export function SwitchIcon({ children, className, ...props }: SwitchIconProps) {
  const { slots } = use(SwitchContext);
  return (
    <dom.span
      className={composeSlotClassName(slots?.icon, className)}
      data-slot="switch-icon"
      {...props}
    >
      {children}
    </dom.span>
  );
}

export const Switch = Object.assign(SwitchRoot, {
  Root: SwitchRoot,
  Content: SwitchContent,
  Control: SwitchControl,
  Thumb: SwitchThumb,
  Icon: SwitchIcon,
});

import type { ComponentPropsWithRef, ReactNode } from "react";
import { createContext, use, useMemo } from "react";
import { dom } from "./dom";
import { cx } from "./utils";

/** 与 @heroui/styles 的 chipVariants 同值的 BEM 类名映射。 */
export type ChipColor = "accent" | "danger" | "default" | "success" | "warning";
export type ChipSize = "sm" | "md" | "lg";
export type ChipVariant = "primary" | "secondary" | "soft" | "tertiary";

const colorClass: Record<ChipColor, string> = {
  accent: "chip--accent",
  danger: "chip--danger",
  default: "chip--default",
  success: "chip--success",
  warning: "chip--warning",
};

const sizeClass: Record<ChipSize, string> = {
  lg: "chip--lg",
  md: "chip--md",
  sm: "chip--sm",
};

const variantClass: Record<ChipVariant, string> = {
  primary: "chip--primary",
  secondary: "chip--secondary",
  soft: "chip--soft",
  tertiary: "chip--tertiary",
};

interface ChipSlots {
  base: string;
  label: string;
}

/** 类名顺序对齐 @heroui/styles 的 tv()：base → color → size → variant。 */
function chipSlots(
  color?: ChipColor,
  size?: ChipSize,
  variant?: ChipVariant,
): ChipSlots {
  return {
    base: cx(
      "chip",
      colorClass[color ?? "default"],
      size ? sizeClass[size] : undefined,
      variantClass[variant ?? "secondary"],
    ),
    label: "chip__label",
  };
}

const ChipContext = createContext<{ slots?: ChipSlots }>({
  slots: chipSlots(),
});

export interface ChipRootProps extends ComponentPropsWithRef<"span"> {
  children: ReactNode;
  /** Chip color. */
  color?: ChipColor;
  /** Chip size. */
  size?: ChipSize;
  /** Chip variant. */
  variant?: ChipVariant;
}

export function ChipRoot({
  children,
  className,
  color,
  size,
  variant,
  ...props
}: ChipRootProps) {
  const slots = useMemo(
    () => chipSlots(color, size, variant),
    [color, size, variant],
  );
  const chipChildren = useMemo(() => {
    if (typeof children === "string" || typeof children === "number") {
      return <ChipLabel>{children}</ChipLabel>;
    }
    return children;
  }, [children]);
  return (
    <ChipContext value={{ slots }}>
      <dom.span
        {...props}
        className={cx(slots.base, className)}
        data-slot="chip"
      >
        {chipChildren}
      </dom.span>
    </ChipContext>
  );
}

export interface ChipLabelProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

export function ChipLabel({ children, className, ...props }: ChipLabelProps) {
  const { slots } = use(ChipContext);
  return (
    <dom.span
      className={cx(slots?.label, className)}
      data-slot="chip-label"
      {...props}
    >
      {children}
    </dom.span>
  );
}

export const Chip = Object.assign(ChipRoot, {
  Root: ChipRoot,
  Label: ChipLabel,
});

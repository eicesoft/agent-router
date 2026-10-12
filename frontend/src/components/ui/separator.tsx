import type { ComponentPropsWithRef } from "react";
import {
  Separator as SeparatorPrimitive,
  SeparatorContext,
  useSlottedContext,
} from "react-aria-components";
import { cx } from "./utils";

/** 与 @heroui/styles 的 separatorVariants 同值的 BEM 类名映射。 */
export type SeparatorOrientation = "horizontal" | "vertical";
export type SeparatorVariant = "default" | "secondary" | "tertiary";

export interface SeparatorProps extends Omit<
  ComponentPropsWithRef<typeof SeparatorPrimitive>,
  "orientation"
> {
  orientation?: SeparatorOrientation;
  variant?: SeparatorVariant;
}

const orientationClass: Record<SeparatorOrientation, string> = {
  horizontal: "separator--horizontal",
  vertical: "separator--vertical",
};

const variantClass: Record<SeparatorVariant, string> = {
  default: "separator--default",
  secondary: "separator--secondary",
  tertiary: "separator--tertiary",
};

/** 从 @heroui/react 的 separator/separator.js 移植（tv 输出换成本地常量）。 */
export function SeparatorRoot({
  className,
  orientation,
  variant,
  ...props
}: SeparatorProps) {
  const context = useSlottedContext(SeparatorContext);
  const resolvedOrientation =
    orientation ?? context?.orientation ?? "horizontal";
  return (
    <SeparatorPrimitive
      data-orientation={resolvedOrientation}
      data-slot="separator"
      orientation={resolvedOrientation}
      className={cx(
        "separator",
        orientationClass[resolvedOrientation],
        variantClass[variant ?? "default"],
        className,
      )}
      {...props}
    />
  );
}

export const Separator = Object.assign(SeparatorRoot, { Root: SeparatorRoot });

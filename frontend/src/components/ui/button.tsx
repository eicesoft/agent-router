import type { ComponentPropsWithRef } from "react";
import { Button as ButtonPrimitive } from "react-aria-components";
import { composeTwRenderProps } from "./utils";

/** 与 @heroui/styles 的 buttonVariants 同值的 BEM 类名映射。 */
export type ButtonSize = "sm" | "md" | "lg";
export type ButtonVariant =
  | "primary"
  | "secondary"
  | "tertiary"
  | "ghost"
  | "outline"
  | "danger"
  | "danger-soft";

export interface ButtonProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  fullWidth?: boolean;
  isIconOnly?: boolean;
  size?: ButtonSize;
  variant?: ButtonVariant;
}

const sizeClass: Record<ButtonSize, string> = {
  sm: "button--sm",
  md: "button--md",
  lg: "button--lg",
};

const variantClass: Record<ButtonVariant, string> = {
  primary: "button--primary",
  secondary: "button--secondary",
  tertiary: "button--tertiary",
  ghost: "button--ghost",
  outline: "button--outline",
  danger: "button--danger",
  "danger-soft": "button--danger-soft",
};

export function ButtonRoot({
  children,
  className,
  fullWidth,
  isDisabled,
  isIconOnly,
  size = "md",
  slot,
  style,
  variant = "primary",
  ...rest
}: ButtonProps) {
  // 类名顺序对齐 @heroui/styles 的 tv()：base → fullWidth → isIconOnly → size → variant。
  const own = [
    "button",
    fullWidth ? "button--full-width" : undefined,
    isIconOnly ? "button--icon-only" : undefined,
    sizeClass[size],
    variantClass[variant],
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <ButtonPrimitive
      className={composeTwRenderProps(className, own)}
      data-slot="button"
      isDisabled={isDisabled}
      slot={slot}
      style={style}
      children={children}
      {...rest}
    />
  );
}

export const Button = ButtonRoot;

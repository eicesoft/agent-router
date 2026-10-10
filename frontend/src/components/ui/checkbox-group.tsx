import { createContext, type ComponentPropsWithRef } from "react";
import { CheckboxGroup as CheckboxGroupPrimitive } from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 checkboxGroupVariants 同值的变体名。 */
export type CheckboxGroupVariant = "primary" | "secondary";

/** 与 @heroui/react 的 CheckboxGroupContext 同义：向 Checkbox 传递 variant。 */
export const CheckboxGroupContext = createContext<{
  variant?: CheckboxGroupVariant;
}>({});

export interface CheckboxGroupProps extends ComponentPropsWithRef<
  typeof CheckboxGroupPrimitive
> {
  variant?: CheckboxGroupVariant;
}

// 类名顺序对齐 @heroui/styles 的 checkboxGroupVariants：base → variant（默认 primary）。
const checkboxGroupClass = (variant?: CheckboxGroupVariant) =>
  cx(
    "checkbox-group",
    variant === "secondary"
      ? "checkbox-group--secondary"
      : "checkbox-group--primary",
  );

export function CheckboxGroup({
  children,
  className,
  variant,
  ...props
}: CheckboxGroupProps) {
  return (
    <CheckboxGroupContext value={{ variant }}>
      <CheckboxGroupPrimitive
        className={composeTwRenderProps(className, checkboxGroupClass(variant))}
        data-slot="checkbox-group"
        {...props}
        children={(values) =>
          typeof children === "function" ? children(values) : children
        }
      />
    </CheckboxGroupContext>
  );
}

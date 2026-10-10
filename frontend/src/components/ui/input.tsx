import { use, type ComponentPropsWithRef } from "react";
import { Input as InputPrimitive } from "react-aria-components";
import { TextFieldContext } from "./textfield";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 inputVariants 同值的变体名。 */
export type InputVariant = "primary" | "secondary";

export interface InputRootProps extends ComponentPropsWithRef<
  typeof InputPrimitive
> {
  fullWidth?: boolean;
  variant?: InputVariant;
}

// 类名顺序对齐 @heroui/styles 的 inputVariants：base → fullWidth → variant（默认 primary）。
const inputClass = (fullWidth?: boolean, variant?: InputVariant) =>
  cx(
    "input",
    fullWidth ? "input--full-width" : undefined,
    variant === "secondary" ? "input--secondary" : "input--primary",
  );

export function InputRoot({
  className,
  fullWidth,
  variant: variantProp,
  ...rest
}: InputRootProps) {
  const textFieldContext = use(TextFieldContext);
  // heroui 还会从 ComboBoxContext 取 variant；本地尚无 combo-box，语义同「显式 prop 优先、其次 TextField 上下文」。
  const variant = variantProp ?? textFieldContext.variant;
  return (
    <InputPrimitive
      className={composeTwRenderProps(
        className,
        inputClass(fullWidth, variant),
      )}
      data-slot="input"
      {...rest}
    />
  );
}

export const Input = Object.assign(InputRoot, { Root: InputRoot });

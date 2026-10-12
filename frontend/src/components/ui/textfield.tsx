import { createContext, type ComponentPropsWithRef } from "react";
import { TextField as TextFieldPrimitive } from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 textFieldVariants 同值的变体名。 */
export type TextFieldVariant = "primary" | "secondary";

/** 与 @heroui/react 的 TextFieldContext 同义：向 Input 等子组件传递 variant。 */
export const TextFieldContext = createContext<{ variant?: TextFieldVariant }>(
  {},
);

export interface TextFieldRootProps extends ComponentPropsWithRef<
  typeof TextFieldPrimitive
> {
  fullWidth?: boolean;
  variant?: TextFieldVariant;
}

// 类名顺序对齐 @heroui/styles 的 textFieldVariants：base → fullWidth（false 输出空串被丢弃）。
const textFieldClass = (fullWidth?: boolean) =>
  cx("textfield", fullWidth ? "textfield--full-width" : undefined);

export function TextFieldRoot({
  children,
  className,
  fullWidth,
  variant,
  ...props
}: TextFieldRootProps) {
  return (
    <TextFieldPrimitive
      data-slot="textfield"
      {...props}
      className={composeTwRenderProps(className, textFieldClass(fullWidth))}
      children={(values) => (
        <TextFieldContext value={{ variant }}>
          {typeof children === "function" ? children(values) : children}
        </TextFieldContext>
      )}
    />
  );
}

export const TextField = Object.assign(TextFieldRoot, { Root: TextFieldRoot });

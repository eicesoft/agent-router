import type { ComponentPropsWithRef } from "react";
import { Label as LabelPrimitive } from "react-aria-components";
import { cx } from "./utils";

export interface LabelRootProps extends ComponentPropsWithRef<
  typeof LabelPrimitive
> {
  isDisabled?: boolean;
  isInvalid?: boolean;
  isRequired?: boolean;
}

// 类名顺序对齐 @heroui/styles 的 labelVariants：base → isDisabled → isInvalid → isRequired（true 才输出），className 追加在最后。
const labelClass = (
  isDisabled?: boolean,
  isInvalid?: boolean,
  isRequired?: boolean,
) =>
  cx(
    "label",
    isDisabled ? "label--disabled" : undefined,
    isInvalid ? "label--invalid" : undefined,
    isRequired ? "label--required" : undefined,
  );

export function LabelRoot({
  children,
  className,
  isDisabled,
  isInvalid,
  isRequired,
  ...rest
}: LabelRootProps) {
  return (
    <LabelPrimitive
      className={cx(labelClass(isDisabled, isInvalid, isRequired), className)}
      data-slot="label"
      {...rest}
      children={children}
    />
  );
}

export const Label = Object.assign(LabelRoot, { Root: LabelRoot });

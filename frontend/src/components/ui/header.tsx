import type { ComponentPropsWithRef } from "react";
import { Header as HeaderPrimitive } from "react-aria-components";
import { cx } from "./utils";

export type HeaderRootProps = ComponentPropsWithRef<typeof HeaderPrimitive>;

export function HeaderRoot({ children, className, ...rest }: HeaderRootProps) {
  // headerVariants 是无 variants 的 tv()：base → className（函数 className 被 clsx 忽略）。
  return (
    <HeaderPrimitive
      className={cx(
        "header",
        typeof className === "string" ? className : undefined,
      )}
      data-slot="header"
      {...rest}
    >
      {children}
    </HeaderPrimitive>
  );
}

export const Header = HeaderRoot;

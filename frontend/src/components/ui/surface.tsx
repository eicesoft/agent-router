import { createContext, type ReactNode } from "react";
import { cx } from "./utils";
import { dom } from "./dom";

export type SurfaceVariant =
  "default" | "secondary" | "tertiary" | "transparent";

export const SurfaceContext = createContext<{ variant?: SurfaceVariant }>({});

const surfaceClass: Record<SurfaceVariant, string> = {
  default: "surface surface--default",
  secondary: "surface surface--secondary",
  tertiary: "surface surface--tertiary",
  transparent: "surface surface--transparent",
};

/** 从 @heroui/react 的 surface/surface.js 移植（surfaceVariants 换成本地类名表）。 */
export function SurfaceRoot({
  children,
  className,
  variant = "default",
  ...rest
}: {
  children?: ReactNode;
  className?: string;
  variant?: SurfaceVariant;
} & Record<string, unknown>) {
  return (
    <SurfaceContext value={{ variant }}>
      <dom.div
        className={cx(surfaceClass[variant], className)}
        data-slot="surface"
        {...rest}
      >
        {children}
      </dom.div>
    </SurfaceContext>
  );
}

import type { ComponentPropsWithRef, ReactNode } from "react";
import { createContext, use, useMemo } from "react";
import { dom } from "./dom";
import { SurfaceContext, type SurfaceVariant } from "./surface";
import { cx } from "./utils";

/** 与 @heroui/styles 的 cardVariants 同值的 BEM 类名映射。 */
export type CardVariant = SurfaceVariant;

const variantClass: Record<CardVariant, string> = {
  default: "card--default",
  secondary: "card--secondary",
  tertiary: "card--tertiary",
  transparent: "card--transparent",
};

interface CardSlots {
  base: string;
  content: string;
  description: string;
  footer: string;
  header: string;
  title: string;
}

/** 类名顺序对齐 @heroui/styles 的 tv()：base → variant。 */
function cardSlots(variant?: CardVariant): CardSlots {
  return {
    base: cx("card", variantClass[variant ?? "default"]),
    content: "card__content",
    description: "card__description",
    footer: "card__footer",
    header: "card__header",
    title: "card__title",
  };
}

const CardContext = createContext<{ slots?: CardSlots }>({
  slots: cardSlots(),
});

export interface CardRootProps extends ComponentPropsWithRef<"div"> {
  children: ReactNode;
  /** Visual variant. @default "default" */
  variant?: CardVariant;
}

export function CardRoot({
  children,
  className,
  variant = "default",
  ...props
}: CardRootProps) {
  const slots = useMemo(() => cardSlots(variant), [variant]);
  const content = (
    <dom.div className={cx(slots.base, className)} data-slot="card" {...props}>
      {children}
    </dom.div>
  );
  return (
    <CardContext value={{ slots }}>
      {variant === "transparent" ? (
        content
      ) : (
        // Allows inner components to apply "on-surface" colors for proper contrast
        <SurfaceContext value={{ variant }}>{content}</SurfaceContext>
      )}
    </CardContext>
  );
}

export interface CardHeaderProps extends ComponentPropsWithRef<"div"> {
  children?: ReactNode;
}

export function CardHeader({ className, ...props }: CardHeaderProps) {
  const { slots } = use(CardContext);
  return (
    <dom.div
      className={cx(slots?.header, className)}
      data-slot="card-header"
      {...props}
    />
  );
}

export interface CardTitleProps extends ComponentPropsWithRef<"h3"> {
  children?: ReactNode;
}

export function CardTitle({ children, className, ...props }: CardTitleProps) {
  const { slots } = use(CardContext);
  return (
    <dom.h3
      className={cx(slots?.title, className)}
      data-slot="card-title"
      {...props}
    >
      {children}
    </dom.h3>
  );
}

export interface CardDescriptionProps extends ComponentPropsWithRef<"p"> {
  children?: ReactNode;
}

export function CardDescription({
  children,
  className,
  ...props
}: CardDescriptionProps) {
  const { slots } = use(CardContext);
  return (
    <dom.p
      className={cx(slots?.description, className)}
      data-slot="card-description"
      {...props}
    >
      {children}
    </dom.p>
  );
}

export interface CardContentProps extends ComponentPropsWithRef<"div"> {
  children?: ReactNode;
}

export function CardContent({ className, ...props }: CardContentProps) {
  const { slots } = use(CardContext);
  return (
    <dom.div
      className={cx(slots?.content, className)}
      data-slot="card-content"
      {...props}
    />
  );
}

export interface CardFooterProps extends ComponentPropsWithRef<"div"> {
  children?: ReactNode;
}

export function CardFooter({ className, ...props }: CardFooterProps) {
  const { slots } = use(CardContext);
  return (
    <dom.div
      className={cx(slots?.footer, className)}
      data-slot="card-footer"
      {...props}
    />
  );
}

export const Card = Object.assign(CardRoot, {
  Root: CardRoot,
  Header: CardHeader,
  Title: CardTitle,
  Description: CardDescription,
  Content: CardContent,
  Footer: CardFooter,
});

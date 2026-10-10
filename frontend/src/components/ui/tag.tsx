import type { ComponentPropsWithRef, ReactElement, ReactNode } from "react";
import {
  Children,
  createContext,
  isValidElement,
  use,
  useMemo,
  type ElementType,
} from "react";
import { Button as ButtonPrimitive } from "react-aria-components/Button";
import { Tag as TagPrimitive } from "react-aria-components/TagGroup";
import { CloseIcon } from "./icons";
import { TagGroupContext } from "./tag-group";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 tagVariants 同值的 BEM 类名映射。 */
export type TagSize = "sm" | "md" | "lg";
export type TagVariant = "default" | "surface";

const sizeClass: Record<TagSize, string> = {
  lg: "tag--lg",
  md: "tag--md",
  sm: "tag--sm",
};

const variantClass: Record<TagVariant, string> = {
  default: "tag--default",
  surface: "tag--surface",
};

interface TagSlots {
  base: string;
  removeButton: string;
}

/** 类名顺序对齐 @heroui/styles 的 tv()：base → size → variant。 */
function tagSlots(size?: TagSize, variant?: TagVariant): TagSlots {
  return {
    base: cx(
      "tag",
      sizeClass[size ?? "md"],
      variantClass[variant ?? "default"],
    ),
    removeButton: "tag__remove-button",
  };
}

const TagContext = createContext<{ slots?: TagSlots }>({ slots: tagSlots() });

/** 从 @heroui/react 的 utils/children.js 移植。 */
const pickChildren = (
  children: ReactNode,
  targetChild: ElementType,
): [ReactNode, ReactElement[] | undefined] => {
  const target: ReactElement[] = [];
  const withoutTargetChildren = Children.map(children, (item) => {
    if (!isValidElement(item)) return item;
    if (item.type === targetChild) {
      target.push(item);
      return null;
    }
    return item;
  })?.filter(Boolean);
  const targetChildren = target.length >= 0 ? target : undefined;
  return [withoutTargetChildren, targetChildren];
};

export interface TagRootProps extends ComponentPropsWithRef<
  typeof TagPrimitive
> {}

export function TagRoot({ children, className, ...restProps }: TagRootProps) {
  const { size, variant } = use(TagGroupContext);
  const slots = useMemo(() => tagSlots(size, variant), [size, variant]);
  const textValue = useMemo(() => {
    if (typeof children === "string") {
      return children;
    }
    if (typeof children === "object") {
      return Children.toArray(children).filter(
        (node): node is string => typeof node === "string",
      )[0];
    }
    return undefined;
  }, [children]);

  // Extract custom RemoveButton from children if present
  const picked = useMemo(() => {
    if (typeof children === "function") return null;
    return pickChildren(children, TagRemoveButton);
  }, [children]);
  return (
    <TagPrimitive
      className={composeTwRenderProps(className, slots.base)}
      data-slot="tag"
      textValue={textValue}
      {...restProps}
    >
      {(renderProps) => (
        <TagContext value={{ slots }}>
          {typeof children === "function" ? (
            children(renderProps)
          ) : (
            <>
              {picked && picked[0]}
              {!!renderProps.allowsRemoving &&
                (picked && picked[1] && picked[1].length > 0 ? (
                  picked[1]
                ) : (
                  <TagRemoveButton />
                ))}
            </>
          )}
        </TagContext>
      )}
    </TagPrimitive>
  );
}

export interface TagRemoveButtonProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  children?: ReactNode;
}

export function TagRemoveButton({
  children,
  className,
  ...restProps
}: TagRemoveButtonProps) {
  const { slots } = use(TagContext);
  return (
    <CloseButton
      aria-label="Remove tag"
      className={composeTwRenderProps(className, slots?.removeButton)}
      data-slot="tag-remove-button"
      slot="remove"
      {...restProps}
    >
      {children}
    </CloseButton>
  );
}

/* -------------------------------------------------------------------------------------------------
 * CloseButton（从 @heroui/react 的 close-button 移植，仅 Tag.RemoveButton 使用）
 * -----------------------------------------------------------------------------------------------*/

const closeButtonVariantClass: Record<"default", string> = {
  default: "close-button--default",
};

interface CloseButtonProps extends ComponentPropsWithRef<
  typeof ButtonPrimitive
> {
  variant?: "default";
}

function CloseButton({
  children,
  className,
  slot,
  style,
  variant,
  ...rest
}: CloseButtonProps) {
  const styles = cx(
    "close-button",
    closeButtonVariantClass[variant ?? "default"],
  );
  return (
    <ButtonPrimitive
      aria-label="Close"
      className={composeTwRenderProps(className, styles)}
      data-slot="close-button"
      slot={slot}
      style={style}
      type="button"
      {...rest}
    >
      {(renderProps) =>
        typeof children === "function"
          ? children(renderProps)
          : (children ?? <CloseIcon data-slot="close-button-icon" />)
      }
    </ButtonPrimitive>
  );
}

export const Tag = Object.assign(TagRoot, {
  Root: TagRoot,
  RemoveButton: TagRemoveButton,
});

import type { ComponentPropsWithRef, ReactNode } from "react";
import { createContext, use, useMemo } from "react";
import {
  TagGroup as TagGroupPrimitive,
  TagList as TagListPrimitive,
} from "react-aria-components/TagGroup";
import { FieldSlotsGate } from "./field-slots";
import { composeTwRenderProps, cx } from "./utils";
import type { TagSize, TagVariant } from "./tag";

/** 与 @heroui/styles 的 tagGroupVariants 同值的 BEM 类名映射。 */
export interface TagGroupSlots {
  base: string;
  list: string;
}

const tagGroupSlots: TagGroupSlots = {
  base: "tag-group",
  list: "tag-group__list",
};

export interface TagGroupContextValue {
  slots?: TagGroupSlots;
  size?: TagSize;
  variant?: TagVariant;
}

export const TagGroupContext = createContext<TagGroupContextValue>({
  slots: tagGroupSlots,
});

export interface TagGroupRootProps extends ComponentPropsWithRef<
  typeof TagGroupPrimitive
> {
  /** Tag size (传给子级 Tag). */
  size?: TagSize;
  /** Tag variant (传给子级 Tag). */
  variant?: TagVariant;
}

export function TagGroupRoot({
  children,
  className,
  size,
  variant,
  ...restProps
}: TagGroupRootProps) {
  const slots = useMemo(() => tagGroupSlots, []);
  return (
    <FieldSlotsGate>
      <TagGroupContext value={{ slots, size, variant }}>
        <TagGroupPrimitive
          className={cx(slots.base, className)}
          data-slot="tag-group"
          {...restProps}
        >
          {children}
        </TagGroupPrimitive>
      </TagGroupContext>
    </FieldSlotsGate>
  );
}

export interface TagGroupListProps<
  T extends object = Record<string, unknown>,
> extends ComponentPropsWithRef<typeof TagListPrimitive<T>> {
  children?: ReactNode;
}

export function TagGroupList<T extends object = Record<string, unknown>>({
  children,
  className,
  ...restProps
}: TagGroupListProps<T>) {
  const { slots } = use(TagGroupContext);
  return (
    <TagListPrimitive
      className={composeTwRenderProps(className, slots?.list)}
      data-slot="tag-group-list"
      {...restProps}
    >
      {children}
    </TagListPrimitive>
  );
}

export const TagGroup = Object.assign(TagGroupRoot, {
  Root: TagGroupRoot,
  List: TagGroupList,
});

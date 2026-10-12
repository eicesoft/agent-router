import {
  cloneElement,
  createContext,
  isValidElement,
  use,
  type ComponentPropsWithRef,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  Button as ButtonPrimitive,
  Group as GroupPrimitive,
  Input as InputPrimitive,
  SearchField as SearchFieldPrimitive,
} from "react-aria-components";
import { CloseIcon, IconSearch } from "./icons";
import { composeTwRenderProps, cx } from "./utils";

/** 与 @heroui/styles 的 searchFieldVariants 同值的 BEM 类名。 */
export type SearchFieldVariant = "primary" | "secondary";

interface SearchFieldSlots {
  base: string;
  clearButton: string;
  group: string;
  input: string;
  searchIcon: string;
}

/** 类名顺序对齐 tv()：base → fullWidth → variant（声明顺序）。 */
function searchFieldSlots(
  fullWidth?: boolean,
  variant?: SearchFieldVariant | null,
): SearchFieldSlots {
  return {
    base: cx(
      "search-field",
      fullWidth ? "search-field--full-width" : undefined,
      variant === "secondary"
        ? "search-field--secondary"
        : "search-field--primary",
    ),
    clearButton: "search-field__clear-button",
    group: cx(
      "search-field__group",
      fullWidth ? "search-field__group--full-width" : undefined,
    ),
    input: "search-field__input",
    searchIcon: "search-field__search-icon",
  };
}

const SearchFieldContext = createContext<{ slots?: SearchFieldSlots }>({});

/* -------------------------------------------------------------------------------------------------
 * SearchField Root
 * -----------------------------------------------------------------------------------------------*/

export interface SearchFieldRootProps extends ComponentPropsWithRef<
  typeof SearchFieldPrimitive
> {
  fullWidth?: boolean;
  variant?: SearchFieldVariant | null;
}

export function SearchFieldRoot({
  children,
  className,
  fullWidth,
  variant,
  ...props
}: SearchFieldRootProps) {
  const slots = searchFieldSlots(fullWidth, variant);
  return (
    <SearchFieldContext value={{ slots }}>
      <SearchFieldPrimitive
        data-slot="search-field"
        {...props}
        className={composeTwRenderProps(className, slots.base)}
      >
        {(values) => (
          <>{typeof children === "function" ? children(values) : children}</>
        )}
      </SearchFieldPrimitive>
    </SearchFieldContext>
  );
}

/* -------------------------------------------------------------------------------------------------
 * SearchField Group
 * -----------------------------------------------------------------------------------------------*/

export type SearchFieldGroupProps = ComponentPropsWithRef<
  typeof GroupPrimitive
>;

export function SearchFieldGroup({
  children,
  className,
  ...props
}: SearchFieldGroupProps) {
  const { slots } = use(SearchFieldContext);
  return (
    <GroupPrimitive
      className={composeTwRenderProps(className, slots?.group)}
      data-slot="search-field-group"
      {...props}
    >
      {(values) => (
        <>{typeof children === "function" ? children(values) : children}</>
      )}
    </GroupPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * SearchField Input
 * -----------------------------------------------------------------------------------------------*/

export type SearchFieldInputProps = ComponentPropsWithRef<
  typeof InputPrimitive
>;

export function SearchFieldInput({
  className,
  ...props
}: SearchFieldInputProps) {
  const { slots } = use(SearchFieldContext);
  return (
    <InputPrimitive
      className={composeTwRenderProps(className, slots?.input)}
      data-slot="search-field-input"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * SearchField Search Icon
 * -----------------------------------------------------------------------------------------------*/

export type SearchFieldSearchIconProps = {
  children?: ReactNode;
  className?: string;
} & Omit<React.JSX.IntrinsicElements["svg"], "children" | "className">;

export function SearchFieldSearchIcon({
  children,
  className,
  ...props
}: SearchFieldSearchIconProps) {
  const { slots } = use(SearchFieldContext);
  if (children && isValidElement(children)) {
    return cloneElement(children as ReactElement<Record<string, unknown>>, {
      ...props,
      className: cx(slots?.searchIcon, className),
      "data-slot": "search-field-search-icon",
    });
  }
  return (
    <IconSearch
      className={cx(slots?.searchIcon, className)}
      data-slot="search-field-search-icon"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * SearchField Clear Button（heroui 的 close-button 就地内联：本族唯一使用方）
 * -----------------------------------------------------------------------------------------------*/

function CloseButton({
  children,
  className,
  slot,
  style,
  ...rest
}: ComponentPropsWithRef<typeof ButtonPrimitive>) {
  return (
    <ButtonPrimitive
      aria-label="Close"
      className={composeTwRenderProps(
        className,
        "close-button close-button--default",
      )}
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

export type SearchFieldClearButtonProps = ComponentPropsWithRef<
  typeof CloseButton
>;

export function SearchFieldClearButton({
  className,
  ...props
}: SearchFieldClearButtonProps) {
  const { slots } = use(SearchFieldContext);
  return (
    <CloseButton
      className={composeTwRenderProps(className, slots?.clearButton)}
      data-slot="search-field-clear-button"
      slot="clear"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const SearchField = Object.assign(SearchFieldRoot, {
  Root: SearchFieldRoot,
  Group: SearchFieldGroup,
  Input: SearchFieldInput,
  SearchIcon: SearchFieldSearchIcon,
  ClearButton: SearchFieldClearButton,
});

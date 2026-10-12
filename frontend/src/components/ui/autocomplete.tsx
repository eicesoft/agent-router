import {
  cloneElement,
  createContext,
  isValidElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentPropsWithRef,
  type ContextType,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
  type Ref,
  type RefObject,
} from "react";
import { useIsHidden } from "react-aria/private/collections/Hidden";
import {
  Autocomplete as AutocompletePrimitive,
  Button as ButtonPrimitive,
  Group as GroupPrimitive,
  Popover as PopoverPrimitive,
  Select as SelectPrimitive,
  SelectStateContext,
  SelectValue as SelectValuePrimitive,
} from "react-aria-components";
import { composeTwRenderProps, cx, dataAttr } from "./utils";
import { dom } from "./dom";
import { FieldSlotsGate } from "./field-slots";
import { CloseIcon, IconChevronDown } from "./icons";
import { SurfaceContext } from "./surface";

/** 与 @heroui/styles 的 autocompleteVariants 同值的 BEM 类名。 */
export type AutocompleteVariant = "primary" | "secondary";

interface AutocompleteSlots {
  base: string;
  clearButton: string;
  indicator: string;
  popover: string;
  trigger: string;
  value: string;
}

/** 类名顺序对齐 tv()：base → fullWidth → variant（声明顺序）。 */
function autocompleteSlots(
  fullWidth?: boolean,
  variant?: AutocompleteVariant | null,
): AutocompleteSlots {
  return {
    base: cx(
      "autocomplete",
      fullWidth ? "autocomplete--full-width" : undefined,
      variant === "secondary"
        ? "autocomplete--secondary"
        : "autocomplete--primary",
    ),
    clearButton: "autocomplete__clear-button",
    indicator: "autocomplete__indicator",
    popover: "autocomplete__popover",
    trigger: cx(
      "autocomplete__trigger",
      fullWidth ? "autocomplete__trigger--full-width" : undefined,
    ),
    value: "autocomplete__value",
  };
}

interface AutocompleteContextValue {
  slots?: AutocompleteSlots;
  triggerRef?: RefObject<HTMLElement | null>;
  clearButtonRef?: RefObject<HTMLElement | null>;
  onClear?: () => void;
  isDisabled?: boolean;
}

const AutocompleteContext = createContext<AutocompleteContextValue>({
  triggerRef: { current: null },
  clearButtonRef: { current: null },
  isDisabled: false,
});

function mergeRefs<T>(...refs: Array<Ref<T> | undefined>) {
  return (node: T | null) => {
    for (const ref of refs) {
      if (typeof ref === "function") ref(node);
      else if (ref) (ref as RefObject<T | null>).current = node;
    }
  };
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Root
 * -----------------------------------------------------------------------------------------------*/

export interface AutocompleteRootProps<
  T extends object = object,
  M extends "single" | "multiple" = "single",
> extends ComponentPropsWithRef<typeof SelectPrimitive<T, M>> {
  items?: Iterable<T, M>;
  fullWidth?: boolean;
  variant?: AutocompleteVariant | null;
  onClear?: () => void;
}

export function AutocompleteRoot<
  T extends object = object,
  M extends "single" | "multiple" = "single",
>({
  children,
  className,
  fullWidth,
  isDisabled,
  onClear,
  variant,
  ...props
}: AutocompleteRootProps<T, M>) {
  const slots = useMemo(
    () => autocompleteSlots(fullWidth, variant),
    [fullWidth, variant],
  );
  const triggerRef = useRef<HTMLElement | null>(null);
  const clearButtonRef = useRef<HTMLElement | null>(null);
  const context = useMemo(
    () => ({ slots, triggerRef, clearButtonRef, onClear, isDisabled }),
    [slots, onClear, isDisabled],
  );
  return (
    <FieldSlotsGate>
      <AutocompleteContext value={context}>
        <SelectPrimitive
          data-slot="autocomplete"
          {...props}
          className={composeTwRenderProps(className, slots.base)}
          isDisabled={isDisabled}
        >
          {(values) => (
            <>{typeof children === "function" ? children(values) : children}</>
          )}
        </SelectPrimitive>
      </AutocompleteContext>
    </FieldSlotsGate>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Trigger
 * -----------------------------------------------------------------------------------------------*/

export type AutocompleteTriggerProps = ComponentPropsWithRef<
  typeof GroupPrimitive
>;

export function AutocompleteTrigger({
  children,
  className,
  isDisabled: isDisabledProp,
  onClick,
  ref,
  ...props
}: AutocompleteTriggerProps) {
  const {
    clearButtonRef,
    isDisabled: rootDisabled,
    slots,
    triggerRef,
  } = useContext(AutocompleteContext);
  const state = useContext(SelectStateContext);
  const isDisabled = isDisabledProp ?? rootDisabled ?? false;
  const isHidden = useIsHidden();

  // Callback ref to update context ref
  const contextRefCallback = useCallback(
    (node: HTMLDivElement | null) => {
      if (triggerRef) triggerRef.current = node;
    },
    [triggerRef],
  );
  const mergedRef = mergeRefs<HTMLDivElement>(contextRefCallback, ref);
  const handleClick = (e: React.MouseEvent<HTMLDivElement>) => {
    // Don't toggle if clicking the clear button
    if (clearButtonRef?.current?.contains(e.target as Node)) {
      return;
    }
    onClick?.(e);
    state?.toggle();
  };
  if (isHidden) {
    return null;
  }
  return (
    <GroupPrimitive
      ref={mergedRef}
      className={composeTwRenderProps(className, slots?.trigger)}
      data-slot="autocomplete-trigger"
      isDisabled={isDisabled}
      onClick={handleClick}
      {...props}
    >
      {(values) => (
        <>{typeof children === "function" ? children(values) : children}</>
      )}
    </GroupPrimitive>
  );
}
AutocompleteTrigger.displayName = "AutocompleteTrigger";

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Value
 * -----------------------------------------------------------------------------------------------*/

export type AutocompleteValueProps = ComponentPropsWithRef<
  typeof SelectValuePrimitive
>;

export function AutocompleteValue({
  children,
  className,
  ...props
}: AutocompleteValueProps) {
  const { slots } = useContext(AutocompleteContext);
  return (
    <SelectValuePrimitive
      className={composeTwRenderProps(className, slots?.value)}
      data-slot="autocomplete-value"
      {...props}
    >
      {children}
    </SelectValuePrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Indicator
 * -----------------------------------------------------------------------------------------------*/

export type AutocompleteIndicatorProps = {
  children?: ReactNode;
  className?: string;
} & Omit<React.JSX.IntrinsicElements["svg"], "children" | "className">;

export function AutocompleteIndicator({
  children,
  className,
  ...props
}: AutocompleteIndicatorProps) {
  const { slots } = useContext(AutocompleteContext);
  const state = useContext(SelectStateContext);
  const indicator =
    children && isValidElement(children) ? (
      cloneElement(children as ReactElement<Record<string, unknown>>, {
        ...props,
        className: cx(slots?.indicator, className),
        "data-slot": "autocomplete-indicator",
        "data-open": dataAttr(state?.isOpen),
      })
    ) : (
      <IconChevronDown
        className={cx(slots?.indicator, className)}
        data-open={dataAttr(state?.isOpen)}
        data-slot="autocomplete-default-indicator"
        {...props}
      />
    );
  return <ButtonPrimitive>{indicator}</ButtonPrimitive>;
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Popover
 * -----------------------------------------------------------------------------------------------*/

export type AutocompletePopoverProps = Omit<
  ComponentPropsWithRef<typeof PopoverPrimitive>,
  "children"
> & {
  children: ReactNode;
};

export function AutocompletePopover({
  children,
  className,
  placement = "bottom",
  style,
  ...props
}: AutocompletePopoverProps) {
  const { slots, triggerRef } = useContext(AutocompleteContext);
  const [triggerWidth, setTriggerWidth] = useState<string | null>(null);
  useEffect(() => {
    const node = triggerRef?.current;
    if (!node || typeof ResizeObserver === "undefined") return;
    const onResize = () => setTriggerWidth(node.offsetWidth + "px");
    onResize();
    const observer = new ResizeObserver(onResize);
    observer.observe(node);
    return () => observer.disconnect();
  }, [triggerRef]);
  const popoverStyle = {
    "--trigger-width": triggerWidth,
    ...(typeof style === "object" && style !== null ? style : {}),
  } as CSSProperties;
  return (
    <SurfaceContext value={{ variant: "default" }}>
      <PopoverPrimitive
        {...props}
        className={composeTwRenderProps(className, slots?.popover)}
        data-slot="autocomplete-popover"
        placement={placement}
        triggerRef={triggerRef}
        style={popoverStyle}
      >
        {children}
      </PopoverPrimitive>
    </SurfaceContext>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Filter
 * -----------------------------------------------------------------------------------------------*/

export type AutocompleteFilterProps = ComponentPropsWithRef<
  typeof AutocompletePrimitive
>;

export function AutocompleteFilter({
  children,
  ...props
}: AutocompleteFilterProps) {
  return (
    <AutocompletePrimitive data-slot="autocomplete-filter" {...props}>
      {children}
    </AutocompletePrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Autocomplete Clear Button
 * -----------------------------------------------------------------------------------------------*/

export type AutocompleteClearButtonProps = {
  children?: ReactNode;
  className?: string;
} & Omit<ComponentPropsWithRef<"button">, "children" | "className">;

export function AutocompleteClearButton({
  className,
  onClick,
  ref,
  ...props
}: AutocompleteClearButtonProps) {
  const { clearButtonRef, isDisabled, onClear, slots } =
    useContext(AutocompleteContext);
  const state = useContext(SelectStateContext);
  const clearButtonRefCallback = useCallback(
    (node: HTMLButtonElement | null) => {
      if (clearButtonRef) clearButtonRef.current = node;
    },
    [clearButtonRef],
  );

  // Merge context ref callback with user-provided ref
  const mergedRef = mergeRefs<HTMLButtonElement>(clearButtonRefCallback, ref);
  const handleClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    state?.selectionManager.setSelectedKeys(new Set());
    onClear?.();
    onClick?.(e);
  };
  const isEmpty = state?.selectionManager.selectedKeys.size === 0;
  return (
    <dom.button
      ref={mergedRef}
      aria-hidden={isEmpty || undefined}
      aria-label="Clear selection"
      className={cx(slots?.clearButton, className)}
      data-empty={dataAttr(isEmpty)}
      data-slot="autocomplete-clear-button"
      disabled={isDisabled ?? false}
      tabIndex={-1}
      type="button"
      onClick={handleClick}
      {...props}
    >
      <CloseIcon data-slot="autocomplete-clear-button-icon" />
    </dom.button>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const Autocomplete = Object.assign(AutocompleteRoot, {
  Root: AutocompleteRoot,
  Trigger: AutocompleteTrigger,
  Value: AutocompleteValue,
  Indicator: AutocompleteIndicator,
  Popover: AutocompletePopover,
  Filter: AutocompleteFilter,
  ClearButton: AutocompleteClearButton,
});

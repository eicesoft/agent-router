import {
  cloneElement,
  createContext,
  isValidElement,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  type ComponentPropsWithRef,
  type ContextType,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  Button as ButtonPrimitive,
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

/** 与 @heroui/styles 的 selectVariants 同值的 BEM 类名。 */
export type SelectVariant = "primary" | "secondary";

interface SelectSlots {
  base: string;
  clearButton: string;
  indicator: string;
  popover: string;
  trigger: string;
  value: string;
}

/** 类名顺序对齐 tv()：base → fullWidth → variant（声明顺序）。 */
function selectSlots(
  fullWidth?: boolean,
  variant?: SelectVariant | null,
): SelectSlots {
  return {
    base: cx(
      "select",
      fullWidth ? "select--full-width" : undefined,
      variant === "secondary" ? "select--secondary" : "select--primary",
    ),
    clearButton: "select__clear-button",
    indicator: "select__indicator",
    popover: "select__popover",
    trigger: cx(
      "select__trigger",
      fullWidth ? "select__trigger--full-width" : undefined,
    ),
    value: "select__value",
  };
}

interface SelectContextValue {
  hasClearButton?: () => boolean;
  isDisabled?: boolean;
  onClear?: () => void;
  registerClearButton?: () => () => void;
  slots?: SelectSlots;
}

const SelectContext = createContext<SelectContextValue>({});

/**
 * Exposes the trigger's resolved disabled state, which accounts for both the root's
 * `isDisabled` and a `Select.Trigger`-level override.
 */
const SelectTriggerContext = createContext<{ isDisabled?: boolean }>({});

type SelectStateLike = NonNullable<ContextType<typeof SelectStateContext>>;

const isSelectionEmpty = (state: SelectStateLike | null) =>
  (state?.selectionManager.selectedKeys.size ?? 0) === 0;

const clearSelection = (
  state: SelectStateLike | null,
  onClear?: () => void,
) => {
  state?.selectionManager.setSelectedKeys(new Set());
  onClear?.();
};

/* -------------------------------------------------------------------------------------------------
 * Select Root
 * -----------------------------------------------------------------------------------------------*/

export interface SelectRootProps<
  T extends object = object,
  M extends "single" | "multiple" = "single",
> extends ComponentPropsWithRef<typeof SelectPrimitive<T, M>> {
  items?: Iterable<T, M>;
  fullWidth?: boolean;
  variant?: SelectVariant | null;
  /** Handler that is called when the selection is cleared. */
  onClear?: () => void;
}

export function SelectRoot<
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
}: SelectRootProps<T, M>) {
  const slots = useMemo(
    () => selectSlots(fullWidth, variant),
    [fullWidth, variant],
  );
  const clearButtonCount = useRef(0);

  // Counted rather than a boolean so unmounting one clear button cannot switch the
  // shortcut off while another is still mounted.
  const registerClearButton = useCallback(() => {
    clearButtonCount.current += 1;
    return () => {
      clearButtonCount.current -= 1;
    };
  }, []);
  const hasClearButton = useCallback(() => clearButtonCount.current > 0, []);
  const context = useMemo(
    () => ({ hasClearButton, isDisabled, onClear, registerClearButton, slots }),
    [hasClearButton, isDisabled, onClear, registerClearButton, slots],
  );
  return (
    <FieldSlotsGate>
      <SelectContext value={context}>
        <SelectPrimitive
          data-slot="select"
          {...props}
          className={composeTwRenderProps(className, slots.base)}
          isDisabled={isDisabled}
        >
          {(values) => (
            <>{typeof children === "function" ? children(values) : children}</>
          )}
        </SelectPrimitive>
      </SelectContext>
    </FieldSlotsGate>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Select Trigger
 * -----------------------------------------------------------------------------------------------*/

type SelectTriggerProps = ComponentPropsWithRef<typeof ButtonPrimitive>;
type SelectKeyDownHandler = NonNullable<SelectTriggerProps["onKeyDown"]>;
type SelectKeyDownEvent = Parameters<SelectKeyDownHandler>[0];

export function SelectTrigger({
  children,
  className,
  onKeyDown,
  ...props
}: SelectTriggerProps) {
  const { hasClearButton, onClear, slots } = use(SelectContext);
  const state = use(SelectStateContext);

  // ARIA treats the children of a button as presentational, so Select.ClearButton can
  // never take focus. This shortcut is the only pointer-free way to clear, and it is
  // gated on a clear button being composed so a plain Select stays non-clearable.
  const handleKeyDown = (e: SelectKeyDownEvent) => {
    onKeyDown?.(e);
    const isClearKey = e.key === "Backspace" || e.key === "Delete";
    const canClear = Boolean(hasClearButton?.()) && !isSelectionEmpty(state);
    if (!isClearKey || !canClear || state?.isOpen) {
      // react-aria's useKeyboard swallows propagation unless a handler opts back in,
      // and the trigger had no keydown handler at all before this shortcut existed.
      e.continuePropagation();
      return;
    }
    e.preventDefault();
    clearSelection(state, onClear);
  };
  return (
    <ButtonPrimitive
      className={composeTwRenderProps(className, slots?.trigger)}
      data-slot="select-trigger"
      {...props}
      onKeyDown={handleKeyDown}
    >
      {(values) => (
        <SelectTriggerContext value={{ isDisabled: values.isDisabled }}>
          {typeof children === "function" ? children(values) : children}
        </SelectTriggerContext>
      )}
    </ButtonPrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Select Value
 * -----------------------------------------------------------------------------------------------*/

export type SelectValueProps = ComponentPropsWithRef<
  typeof SelectValuePrimitive
>;

export function SelectValue({
  children,
  className,
  ...props
}: SelectValueProps) {
  const { slots } = use(SelectContext);
  return (
    <SelectValuePrimitive
      className={composeTwRenderProps(className, slots?.value)}
      data-slot="select-value"
      {...props}
    >
      {children}
    </SelectValuePrimitive>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Select Indicator
 * -----------------------------------------------------------------------------------------------*/

export type SelectIndicatorProps = {
  children?: ReactNode;
  className?: string;
} & Omit<React.JSX.IntrinsicElements["svg"], "children" | "className">;

export function SelectIndicator({
  children,
  className,
  ...props
}: SelectIndicatorProps) {
  const { slots } = use(SelectContext);
  const state = use(SelectStateContext);
  if (children && isValidElement(children)) {
    return cloneElement(children as ReactElement<Record<string, unknown>>, {
      ...props,
      className: cx(slots?.indicator, className),
      "data-slot": "select-indicator",
      "data-open": dataAttr(state?.isOpen),
    });
  }
  return (
    <IconChevronDown
      className={cx(slots?.indicator, className)}
      data-open={dataAttr(state?.isOpen)}
      data-slot="select-default-indicator"
      {...props}
    />
  );
}

/* -------------------------------------------------------------------------------------------------
 * Select Clear Button
 * -----------------------------------------------------------------------------------------------*/

export type SelectClearButtonProps = {
  children?: ReactNode;
  className?: string;
} & Omit<ComponentPropsWithRef<"span">, "children" | "className">;

export function SelectClearButton({
  children,
  className,
  onClick,
  onPointerDown,
  ...props
}: SelectClearButtonProps) {
  const {
    isDisabled: rootDisabled,
    onClear,
    registerClearButton,
    slots,
  } = use(SelectContext);
  const { isDisabled: triggerDisabled } = use(SelectTriggerContext);
  const state = use(SelectStateContext);
  const isDisabled = triggerDisabled ?? rootDisabled ?? false;
  const isEmpty = isSelectionEmpty(state);

  // Opts the trigger into the Backspace/Delete shortcut, which is how keyboard and
  // screen reader users clear. See SelectTrigger.
  useEffect(() => registerClearButton?.(), [registerClearButton]);
  const handlePointerDown = (e: React.PointerEvent<HTMLSpanElement>) => {
    // Select.Trigger is a <button>, and nesting a <button> in one is invalid HTML, so
    // this control is a span. Keep the press off the trigger so the menu stays closed.
    // The default is left intact so focus still lands on the trigger, which is where
    // the clear shortcut lives.
    e.stopPropagation();
    onPointerDown?.(e);
  };
  const handleClick = (e: React.MouseEvent<HTMLSpanElement>) => {
    e.preventDefault();
    e.stopPropagation();

    // Clearing happens here rather than on pointer down so that only a primary-button
    // click clears; `pointerdown` also fires for secondary and middle buttons.
    if (!isDisabled && !isEmpty) {
      clearSelection(state, onClear);
    }
    onClick?.(e);
  };
  return (
    <dom.span
      aria-hidden={true}
      className={cx(slots?.clearButton, className)}
      data-empty={dataAttr(isEmpty)}
      data-slot="select-clear-button"
      {...props}
      onClick={handleClick}
      onPointerDown={handlePointerDown}
    >
      {children ?? <CloseIcon data-slot="select-clear-button-icon" />}
    </dom.span>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Select Popover
 * -----------------------------------------------------------------------------------------------*/

export type SelectPopoverProps = Omit<
  ComponentPropsWithRef<typeof PopoverPrimitive>,
  "children"
> & {
  children: ReactNode;
};

export function SelectPopover({
  children,
  className,
  placement = "bottom",
  ...props
}: SelectPopoverProps) {
  const { slots } = use(SelectContext);
  return (
    <SurfaceContext value={{ variant: "default" }}>
      <PopoverPrimitive
        {...props}
        className={composeTwRenderProps(className, slots?.popover)}
        data-slot="select-popover"
        placement={placement}
      >
        {children}
      </PopoverPrimitive>
    </SurfaceContext>
  );
}

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const Select = Object.assign(SelectRoot, {
  Root: SelectRoot,
  Trigger: SelectTrigger,
  Value: SelectValue,
  Indicator: SelectIndicator,
  ClearButton: SelectClearButton,
  Popover: SelectPopover,
});

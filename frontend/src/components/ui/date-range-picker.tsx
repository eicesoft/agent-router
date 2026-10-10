import type { ComponentPropsWithRef, ReactNode, Ref } from "react";
import type { DateValue } from "react-aria-components/Calendar";
import {
  createContext,
  Fragment,
  useCallback,
  useEffect,
  use,
  useRef,
  useState,
} from "react";
import { Button } from "react-aria-components";
import {
  DateRangePicker as DateRangePickerPrimitive,
  Popover,
} from "react-aria-components/DateRangePicker";
import { composeTwRenderProps, cx, dataAttr } from "./utils";
import { dom } from "./dom";
import { IconCalendar } from "./icons";
import { SurfaceContext } from "./surface";

/**
 * 从 @heroui/react 的 date-range-picker/{index,date-range-picker}.js 移植。
 * 类名来自 @heroui/styles 的 dateRangePickerVariants（纯 BEM、无 variants），
 * 本地化为常量，顺序与 tv() 一致：base 先、调用方 className 在后。
 */
const dateRangePickerSlots = {
  base: "date-range-picker",
  popover: "date-range-picker__popover",
  rangeSeparator: "date-range-picker__range-separator",
  trigger: "date-range-picker__trigger",
  triggerIndicator: "date-range-picker__trigger-indicator",
} as const;

const DateRangePickerContext = createContext<{
  triggerRef: { current: HTMLElement | null };
}>({ triggerRef: { current: null } });

function mergeRefs<T>(...refs: Array<Ref<T> | undefined>): Ref<T> {
  return (node) => {
    for (const ref of refs) {
      if (typeof ref === "function") ref(node);
      else if (ref) (ref as { current: T | null }).current = node;
    }
  };
}

/* -------------------------------------------------------------------------------------------------
 * DateRangePicker Root
 * -----------------------------------------------------------------------------------------------*/

export interface DateRangePickerRootProps<
  T extends DateValue,
> extends ComponentPropsWithRef<typeof DateRangePickerPrimitive<T>> {}

export function DateRangePickerRoot<T extends DateValue>({
  children,
  className,
  onOpenChange,
  ...props
}: DateRangePickerRootProps<T>) {
  const triggerRef = useRef<HTMLElement | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const shouldRestoreFocusToTriggerRef = useRef(false);
  useEffect(() => {
    if (!isOpen) return;
    const handleGlobalKeyDown = (event: KeyboardEvent) => {
      if (!event.metaKey && !event.ctrlKey && !event.altKey) {
        shouldRestoreFocusToTriggerRef.current = true;
      }
    };
    window.addEventListener("keydown", handleGlobalKeyDown, true);
    return () => {
      window.removeEventListener("keydown", handleGlobalKeyDown, true);
    };
  }, [isOpen]);
  const handleOpenChange = (nextIsOpen: boolean) => {
    setIsOpen(nextIsOpen);
    if (!nextIsOpen && shouldRestoreFocusToTriggerRef.current) {
      window.requestAnimationFrame(() => {
        triggerRef.current?.focus();
      });
    }
    shouldRestoreFocusToTriggerRef.current = false;
    onOpenChange?.(nextIsOpen);
  };
  return (
    <DateRangePickerContext value={{ triggerRef }}>
      <DateRangePickerPrimitive
        data-required={dataAttr(props.isRequired)}
        data-slot="date-range-picker"
        {...props}
        className={composeTwRenderProps(className, dateRangePickerSlots.base)}
        onOpenChange={handleOpenChange}
      >
        {(values) => (
          <Fragment>
            {typeof children === "function" ? children(values) : children}
          </Fragment>
        )}
      </DateRangePickerPrimitive>
    </DateRangePickerContext>
  );
}
DateRangePickerRoot.displayName = "AgentRouter.DateRangePicker";

/* -------------------------------------------------------------------------------------------------
 * DateRangePicker Trigger
 * -----------------------------------------------------------------------------------------------*/

export interface DateRangePickerTriggerProps extends ComponentPropsWithRef<
  typeof Button
> {}

export const DateRangePickerTrigger = ({
  children,
  className,
  ref,
  ...props
}: DateRangePickerTriggerProps) => {
  const { triggerRef } = use(DateRangePickerContext);
  const contextRefCallback = useCallback(
    (node: HTMLButtonElement | null) => {
      triggerRef.current = node;
    },
    [triggerRef],
  );
  const mergedRef = mergeRefs<HTMLButtonElement>(contextRefCallback, ref);
  return (
    <Button
      ref={mergedRef}
      className={composeTwRenderProps(className, dateRangePickerSlots.trigger)}
      data-slot="date-range-picker-trigger"
      {...props}
    >
      {(values) => (
        <Fragment>
          {typeof children === "function" ? children(values) : children}
        </Fragment>
      )}
    </Button>
  );
};
DateRangePickerTrigger.displayName = "AgentRouter.DateRangePicker.Trigger";

/* -------------------------------------------------------------------------------------------------
 * DateRangePicker Trigger Indicator
 * -----------------------------------------------------------------------------------------------*/

export interface DateRangePickerTriggerIndicatorProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

export const DateRangePickerTriggerIndicator = ({
  children,
  className,
  ...props
}: DateRangePickerTriggerIndicatorProps) => {
  return (
    <dom.span
      aria-hidden="true"
      className={cx(dateRangePickerSlots.triggerIndicator, className)}
      data-slot="date-range-picker-trigger-indicator"
      {...props}
    >
      {children || <IconCalendar />}
    </dom.span>
  );
};
DateRangePickerTriggerIndicator.displayName =
  "AgentRouter.DateRangePicker.TriggerIndicator";

/* -------------------------------------------------------------------------------------------------
 * DateRangePicker Range Separator
 * -----------------------------------------------------------------------------------------------*/

export interface DateRangePickerRangeSeparatorProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

export const DateRangePickerRangeSeparator = ({
  children = " - ",
  className,
  ...props
}: DateRangePickerRangeSeparatorProps) => {
  return (
    <dom.span
      aria-hidden="true"
      className={cx(dateRangePickerSlots.rangeSeparator, className)}
      data-slot="date-range-picker-range-separator"
      {...props}
    >
      {children}
    </dom.span>
  );
};
DateRangePickerRangeSeparator.displayName =
  "AgentRouter.DateRangePicker.RangeSeparator";

/* -------------------------------------------------------------------------------------------------
 * DateRangePicker Popover
 * -----------------------------------------------------------------------------------------------*/

export interface DateRangePickerPopoverProps extends Omit<
  ComponentPropsWithRef<typeof Popover>,
  "children"
> {
  children: ReactNode;
}

export const DateRangePickerPopover = ({
  children,
  className,
  placement = "bottom",
  ...props
}: DateRangePickerPopoverProps) => {
  return (
    <SurfaceContext value={{ variant: "default" }}>
      <Popover
        {...props}
        className={composeTwRenderProps(
          className,
          dateRangePickerSlots.popover,
        )}
        data-slot="date-range-picker-popover"
        placement={placement}
      >
        {children}
      </Popover>
    </SurfaceContext>
  );
};
DateRangePickerPopover.displayName = "AgentRouter.DateRangePicker.Popover";

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const DateRangePicker = Object.assign(DateRangePickerRoot, {
  Root: DateRangePickerRoot,
  Trigger: DateRangePickerTrigger,
  TriggerIndicator: DateRangePickerTriggerIndicator,
  RangeSeparator: DateRangePickerRangeSeparator,
  Popover: DateRangePickerPopover,
});

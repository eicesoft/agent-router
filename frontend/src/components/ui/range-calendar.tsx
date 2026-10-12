import type { ComponentPropsWithRef, ReactNode } from "react";
import type { DateValue } from "react-aria-components/Calendar";
import { useMemo, useRef } from "react";
import { Button, useLocale } from "react-aria-components";
import {
  CalendarCell,
  CalendarGrid,
  CalendarGridBody,
  CalendarGridHeader,
  CalendarHeaderCell,
  CalendarHeading,
  RangeCalendar as RangeCalendarPrimitive,
} from "react-aria-components/RangeCalendar";
import {
  CalendarDate,
  DateFormatter,
  createCalendar,
} from "@internationalized/date";
import { composeTwRenderProps, cx, dataAttr } from "./utils";
import { dom } from "./dom";
import { IconChevronLeft, IconChevronRight } from "./icons";

/**
 * 从 @heroui/react 的 range-calendar/{index,range-calendar}.js 移植。
 * 类名来自 @heroui/styles 的 rangeCalendarVariants（纯 BEM、无 variants），
 * 本地化为常量，顺序与 tv() 一致：base 先、调用方 className 在后。
 * HeroUI 经 RangeCalendarContext 传递 slots/dayView——slots 是常量、dayView
 * 仅服务 `visibleDuration.days` 的 day-view 分支（该分支要牵连 calendar 目录的
 * CalendarDayViewGrid* 件），按依赖最小化不移植，故上下文整体省略。
 * 同理省略 calendar-year-picker 的 YearPicker* 成员与 isYearPickerOpen 状态。
 */
const rangeCalendarSlots = {
  base: "range-calendar",
  cell: "range-calendar__cell",
  cellIndicator: "range-calendar__cell-indicator",
  grid: "range-calendar__grid",
  gridBody: "range-calendar__grid-body",
  gridHeader: "range-calendar__grid-header",
  header: "range-calendar__header",
  headerCell: "range-calendar__header-cell",
  heading: "range-calendar__heading",
  navButton: "range-calendar__nav-button",
  navButtonIcon: "range-calendar__nav-button-icon",
} as const;

/**
 * 从 @heroui/react 的 utils/calendar.js 内联：Gregorian 与给定历法之间的
 * 年份偏移，用于计算默认 min/max 年界。
 */
function getGregorianYearOffset(identifier: string): number {
  switch (identifier) {
    case "buddhist":
      return 543;
    case "ethiopic":
    case "ethioaa":
      return -8;
    case "coptic":
      return -284;
    case "hebrew":
      return 3760;
    case "indian":
      return -78;
    case "islamic-civil":
    case "islamic-tbla":
    case "islamic-umalqura":
      return -579;
    case "persian":
      return -600;
    case "roc":
    case "japanese":
    case "gregory":
    default:
      return 0;
  }
}

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Root
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarRootProps<
  T extends DateValue = DateValue,
> extends ComponentPropsWithRef<typeof RangeCalendarPrimitive<T>> {}

export function RangeCalendarRoot<T extends DateValue = DateValue>({
  children,
  className,
  firstDayOfWeek,
  maxValue: maxValueProp,
  minValue: minValueProp,
  visibleDuration,
  ...rest
}: RangeCalendarRootProps<T>) {
  const isWeekView = visibleDuration?.weeks != null;
  const isDayView = visibleDuration?.days != null;
  const { locale } = useLocale();
  const calendarRef = useRef<HTMLDivElement | null>(null);
  const calendarProp = useMemo(() => {
    // Intl.resolvedOptions().calendar 声明为 string，收窄到 createCalendar 的入参类型。
    const calendarIdentifier = new DateFormatter(locale).resolvedOptions()
      .calendar as Parameters<typeof createCalendar>[0];
    return createCalendar(calendarIdentifier);
  }, [locale]);
  const gregorianYearOffset = useMemo(
    () => getGregorianYearOffset(calendarProp.identifier),
    [calendarProp.identifier],
  );
  // React Aria memoizes calendar state against minValue/maxValue identity, so
  // these bounds must be stable across renders.
  const minValue = useMemo(
    () =>
      minValueProp ??
      new CalendarDate(calendarProp, 1900 + gregorianYearOffset, 1, 1),
    [minValueProp, calendarProp, gregorianYearOffset],
  );
  const maxValue = useMemo(
    () =>
      maxValueProp ??
      new CalendarDate(calendarProp, 2099 + gregorianYearOffset, 12, 31),
    [maxValueProp, calendarProp, gregorianYearOffset],
  );
  return (
    <RangeCalendarPrimitive
      ref={calendarRef}
      data-slot="range-calendar"
      firstDayOfWeek={firstDayOfWeek}
      maxValue={maxValue}
      minValue={minValue}
      visibleDuration={visibleDuration}
      {...rest}
      className={composeTwRenderProps(
        className,
        cx(
          rangeCalendarSlots.base,
          isWeekView && "range-calendar--week-view",
          isDayView && "range-calendar--day-view",
        ),
      )}
    >
      {(values) =>
        typeof children === "function" ? children(values) : children
      }
    </RangeCalendarPrimitive>
  );
}
RangeCalendarRoot.displayName = "AgentRouter.RangeCalendar";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Header
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarHeaderProps extends ComponentPropsWithRef<"header"> {}

export const RangeCalendarHeader = ({
  children,
  className,
  ...props
}: RangeCalendarHeaderProps) => {
  return (
    <dom.header
      className={cx(rangeCalendarSlots.header, className)}
      data-slot="range-calendar-header"
      {...props}
    >
      {children}
    </dom.header>
  );
};
RangeCalendarHeader.displayName = "AgentRouter.RangeCalendar.Header";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Heading
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarHeadingProps extends ComponentPropsWithRef<
  typeof CalendarHeading
> {}

export const RangeCalendarHeading = ({
  className,
  ...props
}: RangeCalendarHeadingProps) => {
  return (
    <CalendarHeading
      data-slot="range-calendar-heading"
      {...props}
      className={cx(rangeCalendarSlots.heading, className)}
    />
  );
};
RangeCalendarHeading.displayName = "AgentRouter.RangeCalendar.Heading";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Nav Button
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarNavButtonProps extends ComponentPropsWithRef<
  typeof Button
> {
  slot?: "previous" | "next";
}

export const RangeCalendarNavButton = ({
  children,
  className,
  slot,
  ...props
}: RangeCalendarNavButtonProps) => {
  return (
    <Button
      data-slot="range-calendar-nav-button"
      slot={slot}
      {...props}
      className={composeTwRenderProps(className, rangeCalendarSlots.navButton)}
    >
      {children ||
        (slot === "previous" ? (
          <IconChevronLeft
            className={rangeCalendarSlots.navButtonIcon}
            data-slot="range-calendar-nav-button-icon"
          />
        ) : (
          <IconChevronRight
            className={rangeCalendarSlots.navButtonIcon}
            data-slot="range-calendar-nav-button-icon"
          />
        ))}
    </Button>
  );
};
RangeCalendarNavButton.displayName = "AgentRouter.RangeCalendar.NavButton";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Grid
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarGridProps extends ComponentPropsWithRef<
  typeof CalendarGrid
> {}

export const RangeCalendarGrid = ({
  children,
  className,
  weekdayStyle = "short",
  ...props
}: RangeCalendarGridProps) => {
  return (
    <CalendarGrid
      data-slot="range-calendar-grid"
      weekdayStyle={weekdayStyle}
      {...props}
      className={cx(rangeCalendarSlots.grid, className)}
    >
      {children}
    </CalendarGrid>
  );
};
RangeCalendarGrid.displayName = "AgentRouter.RangeCalendar.Grid";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Grid Header
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarGridHeaderProps extends ComponentPropsWithRef<
  typeof CalendarGridHeader
> {}

export const RangeCalendarGridHeader = ({
  children,
  className,
  ...props
}: RangeCalendarGridHeaderProps) => {
  return (
    <CalendarGridHeader
      data-slot="range-calendar-grid-header"
      {...props}
      className={cx(rangeCalendarSlots.gridHeader, className)}
    >
      {children}
    </CalendarGridHeader>
  );
};
RangeCalendarGridHeader.displayName = "AgentRouter.RangeCalendar.GridHeader";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Grid Body
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarGridBodyProps extends ComponentPropsWithRef<
  typeof CalendarGridBody
> {}

export const RangeCalendarGridBody = ({
  children,
  className,
  ...props
}: RangeCalendarGridBodyProps) => {
  return (
    <CalendarGridBody
      data-slot="range-calendar-grid-body"
      {...props}
      className={cx(rangeCalendarSlots.gridBody, className)}
    >
      {children}
    </CalendarGridBody>
  );
};
RangeCalendarGridBody.displayName = "AgentRouter.RangeCalendar.GridBody";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Header Cell
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarHeaderCellProps extends ComponentPropsWithRef<
  typeof CalendarHeaderCell
> {}

export const RangeCalendarHeaderCell = ({
  className,
  ...props
}: RangeCalendarHeaderCellProps) => {
  return (
    <CalendarHeaderCell
      data-slot="range-calendar-header-cell"
      {...props}
      className={cx(rangeCalendarSlots.headerCell, className)}
    />
  );
};
RangeCalendarHeaderCell.displayName = "AgentRouter.RangeCalendar.HeaderCell";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Cell
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarCellProps extends ComponentPropsWithRef<
  typeof CalendarCell
> {}

export const RangeCalendarCell = ({
  children,
  className,
  ...props
}: RangeCalendarCellProps) => {
  return (
    <CalendarCell
      data-slot="range-calendar-cell"
      {...props}
      className={composeTwRenderProps(className, rangeCalendarSlots.cell)}
    >
      {(values) => {
        const {
          formattedDate,
          isDisabled,
          isHovered,
          isPressed,
          isSelectionEnd,
          isSelectionStart,
          isUnavailable,
        } = values;
        const content =
          typeof children === "function"
            ? children(values)
            : children || formattedDate;
        return (
          <span
            className="range-calendar__cell-button"
            data-disabled={dataAttr(isDisabled)}
            data-hovered={dataAttr(isHovered)}
            data-pressed={dataAttr(isPressed)}
            data-selected={dataAttr(isSelectionStart || isSelectionEnd)}
            data-slot="range-calendar-cell-button"
            data-unavailable={dataAttr(isUnavailable)}
          >
            {content}
          </span>
        );
      }}
    </CalendarCell>
  );
};
RangeCalendarCell.displayName = "AgentRouter.RangeCalendar.Cell";

/* -------------------------------------------------------------------------------------------------
 * RangeCalendar Cell Indicator
 * -----------------------------------------------------------------------------------------------*/

export interface RangeCalendarCellIndicatorProps extends ComponentPropsWithRef<"span"> {
  children?: ReactNode;
}

export const RangeCalendarCellIndicator = ({
  className,
  ...props
}: RangeCalendarCellIndicatorProps) => {
  return (
    <dom.span
      aria-hidden="true"
      className={cx(rangeCalendarSlots.cellIndicator, className)}
      data-slot="range-calendar-cell-indicator"
      {...props}
    />
  );
};
RangeCalendarCellIndicator.displayName =
  "AgentRouter.RangeCalendar.CellIndicator";

/* -------------------------------------------------------------------------------------------------
 * Compound Component
 * -----------------------------------------------------------------------------------------------*/

export const RangeCalendar = Object.assign(RangeCalendarRoot, {
  Root: RangeCalendarRoot,
  Header: RangeCalendarHeader,
  Heading: RangeCalendarHeading,
  NavButton: RangeCalendarNavButton,
  Grid: RangeCalendarGrid,
  GridHeader: RangeCalendarGridHeader,
  GridBody: RangeCalendarGridBody,
  HeaderCell: RangeCalendarHeaderCell,
  Cell: RangeCalendarCell,
  CellIndicator: RangeCalendarCellIndicator,
});

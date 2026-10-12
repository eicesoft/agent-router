import {
  DateRangePicker,
  Label,
  RangeCalendar,
  DateRangePickerTrigger,
  DateRangePickerTriggerIndicator,
} from "./ui";
import { Group } from "react-aria-components/DateRangePicker";
import {
  getLocalTimeZone,
  parseDate,
  today,
  type DateDuration,
} from "@internationalized/date";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import type { DateValue } from "react-aria-components/Calendar";

const triggerFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "2-digit",
  day: "2-digit",
});

/** Parses a YYYY-MM-DD filter string into an RAC DateValue, or null when empty. */
function toDateValue(value: string): DateValue | null {
  if (!value) return null;
  try {
    return parseDate(value);
  } catch {
    return null;
  }
}

function fromDateValue(value: DateValue): string {
  return `${value.year}-${String(value.month).padStart(2, "0")}-${String(value.day).padStart(2, "0")}`;
}

/** 弹层里的一列：该月标题 + 该月网格。offset 指明这是可见范围内的第几个月。 */
function MonthColumn({ offset }: { offset?: DateDuration }) {
  return (
    <div className="date-range-month">
      <RangeCalendar.Heading offset={offset} />
      <RangeCalendar.Grid offset={offset}>
        <RangeCalendar.GridHeader>
          {(day) => <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>}
        </RangeCalendar.GridHeader>
        <RangeCalendar.GridBody>
          {(date) => (
            <RangeCalendar.Cell date={date}>
              {(cell) => <>{cell.formattedDate}</>}
            </RangeCalendar.Cell>
          )}
        </RangeCalendar.GridBody>
      </RangeCalendar.Grid>
    </div>
  );
}

export function DateRangeField({
  from,
  to,
  onChange,
}: {
  from: string;
  to: string;
  onChange: (from: string, to: string) => void;
}) {
  const start = toDateValue(from);
  const end = toDateValue(to);
  const value = start && end ? { start, end } : null;
  return (
    <DateRangePicker
      aria-label="日期范围"
      value={value}
      onChange={(range) => {
        if (!range?.start || !range?.end) {
          onChange("", "");
          return;
        }
        onChange(fromDateValue(range.start), fromDateValue(range.end));
      }}
    >
      <Label>日期范围</Label>
      {/* RAC 把弹层锚定在 GroupContext 的 groupRef 上,而该 ref 只有 RAC Group
          组件挂载时才会绑定;HeroUI 的组合漏掉了 Group,弹层因此停在 (0,0)。 */}
      <Group className="date-range-group">
        <DateRangePicker.Trigger>
          {start && end
            ? `${triggerFormat.format(start.toDate(getLocalTimeZone()))} ~ ${triggerFormat.format(end.toDate(getLocalTimeZone()))}`
            : "全部时间"}
          {/* Trigger 是 <button>，故沿用 HeroUI Select.ClearButton 的做法：span +
              aria-hidden，靠 stopPropagation 的 click 清除，避免嵌套交互元素。 */}
          <span
            aria-hidden="true"
            data-empty={value ? undefined : "true"}
            className="date-range-trigger-clear"
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              if (value) onChange("", "");
            }}
          >
            <X size={14} />
          </span>
          <DateRangePicker.TriggerIndicator />
        </DateRangePicker.Trigger>
      </Group>
      <DateRangePicker.Popover>
        <RangeCalendar
          isDateUnavailable={(date) =>
            date.compare(today(getLocalTimeZone())) > 0
          }
          visibleDuration={{ months: 2 }}
        >
          <RangeCalendar.Header>
            <RangeCalendar.NavButton slot="previous">
              <ChevronLeft size={14} />
            </RangeCalendar.NavButton>
            <RangeCalendar.NavButton slot="next">
              <ChevronRight size={14} />
            </RangeCalendar.NavButton>
          </RangeCalendar.Header>
          {/* 两个月并排：RAC 的翻页步长等于 visibleDuration（等于两个月），
              可见范围里的每个月都必须自己渲染一份网格，否则翻页会跳过月份。 */}
          <div className="date-range-calendars">
            <MonthColumn />
            <MonthColumn offset={{ months: 1 }} />
          </div>
        </RangeCalendar>
      </DateRangePicker.Popover>
    </DateRangePicker>
  );
}

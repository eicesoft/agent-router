import { Header, Label, ListBox, Select } from "@heroui/react";
import type { ReactNode } from "react";

export type SelectOption<T extends string> = {
  value: T;
  label: ReactNode;
  // 悬停提示。列表项里只能用它（原生 title），嵌 HeroUI Tooltip 会在
  // option 里放一个 role="button" 的可聚焦元素，点击会被它抢走。
  tooltip?: string;
};

export function FieldSelect<T extends string>({
  label,
  placeholder = "请选择",
  value,
  onChange,
  options,
  groups,
  isDisabled,
  isClearable,
  fullWidth,
  renderValue,
  popoverClassName,
  className,
}: {
  label?: string;
  placeholder?: string;
  value: T | null;
  onChange: (value: T | null) => void;
  options?: SelectOption<T>[];
  // 分组展示（按提供商等）。与 options 二选一。
  groups?: { title: string; options: SelectOption<T>[] }[];
  isDisabled?: boolean;
  isClearable?: boolean;
  fullWidth?: boolean;
  renderValue?: (value: T) => ReactNode;
  popoverClassName?: string;
  className?: string;
}) {
  const flat = groups
    ? groups.flatMap((group) => group.options)
    : (options ?? []);
  const item = (option: SelectOption<T>) => (
    <ListBox.Item
      id={option.value}
      textValue={typeof option.label === "string" ? option.label : option.value}
    >
      <span title={option.tooltip}>{option.label}</span>
    </ListBox.Item>
  );
  return (
    <Select
      className={className}
      placeholder={placeholder}
      selectedKey={value ?? null}
      onSelectionChange={(key) => onChange(key === null ? null : (key as T))}
      isDisabled={isDisabled}
      onClear={() => onChange(null)}
      fullWidth={fullWidth}
    >
      {label && <Label>{label}</Label>}
      <Select.Trigger>
        <Select.Value>
          {({ isPlaceholder, selectedText }) =>
            isPlaceholder
              ? placeholder
              : (renderValue?.(value as T) ?? selectedText)
          }
        </Select.Value>
        <Select.Indicator />
        {isClearable && <Select.ClearButton />}
      </Select.Trigger>
      <Select.Popover className={popoverClassName}>
        {groups ? (
          <ListBox aria-label={label ?? placeholder}>
            {groups.map((group) => (
              <ListBox.Section key={group.title}>
                <Header>{group.title}</Header>
                {group.options.map(item)}
              </ListBox.Section>
            ))}
          </ListBox>
        ) : (
          <ListBox items={flat}>
            {(option: SelectOption<T>) => item(option)}
          </ListBox>
        )}
      </Select.Popover>
    </Select>
  );
}

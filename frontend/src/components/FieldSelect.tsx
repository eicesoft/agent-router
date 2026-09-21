import {
  Autocomplete,
  Header,
  Label,
  ListBox,
  SearchField,
  Select,
  Tooltip,
} from "@heroui/react";
import type { ReactNode } from "react";

export type SelectOption<T extends string> = {
  value: T;
  label: ReactNode;
  // 省略后可悬停查看完整文本。
  tooltip?: string;
};

function WithTooltip({
  text,
  className,
  children,
}: {
  text: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <Tooltip.Trigger className={className} role={undefined} tabIndex={-1}>
        {children}
      </Tooltip.Trigger>
      <Tooltip.Content className="field-select-tooltip">{text}</Tooltip.Content>
    </Tooltip>
  );
}

export function FieldSelect<T extends string>({
  label,
  placeholder = "请选择",
  value,
  onChange,
  options,
  groups,
  isDisabled,
  isClearable,
  isSearchable,
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
  // 长模型列表打开搜索框；短枚举仍用普通 Select，避免无意义的输入区。
  isSearchable?: boolean;
  fullWidth?: boolean;
  renderValue?: (value: T) => ReactNode;
  popoverClassName?: string;
  className?: string;
}) {
  const flat = groups
    ? groups.flatMap((group) => group.options)
    : (options ?? []);
  const item = (option: SelectOption<T>) => {
    const tooltip =
      option.tooltip ??
      (typeof option.label === "string" ? option.label : undefined);
    const label = (
      <span className="field-select-option-label">{option.label}</span>
    );
    return (
      <ListBox.Item
        // 列表项必须自带 key：分组分支直接 map 渲染，缺 key 时 React 会告警。
        key={option.value}
        id={option.value}
        textValue={
          typeof option.label === "string" ? option.label : option.value
        }
      >
        {tooltip ? (
          <WithTooltip text={tooltip} className="field-select-option-trigger">
            {label}
          </WithTooltip>
        ) : (
          label
        )}
      </ListBox.Item>
    );
  };
  const listBox = (
    <ListBox
      aria-label={label ?? placeholder}
      items={groups ? undefined : flat}
    >
      {groups
        ? groups.map((group) => (
            <ListBox.Section key={group.title}>
              <Header>{group.title}</Header>
              {group.options.map(item)}
            </ListBox.Section>
          ))
        : (option: SelectOption<T>) => item(option)}
    </ListBox>
  );
  const selected = flat.find((option) => option.value === value);
  const selectedTooltip =
    selected?.tooltip ??
    (typeof selected?.label === "string" ? selected.label : undefined);
  const withTooltip = (select: ReactNode) =>
    selectedTooltip ? (
      <WithTooltip
        text={selectedTooltip}
        className="field-select-tooltip-anchor"
      >
        {select}
      </WithTooltip>
    ) : (
      select
    );
  if (isSearchable) {
    return withTooltip(
      <Autocomplete
        className={className}
        placeholder={placeholder}
        selectedKey={value ?? null}
        onSelectionChange={(key) => onChange(key === null ? null : (key as T))}
        isDisabled={isDisabled}
        onClear={() => onChange(null)}
        fullWidth={fullWidth}
      >
        {label && <Label>{label}</Label>}
        <Autocomplete.Trigger>
          <Autocomplete.Value>
            {({ isPlaceholder, selectedText }) =>
              isPlaceholder
                ? placeholder
                : (renderValue?.(value as T) ?? selectedText)
            }
          </Autocomplete.Value>
          <Autocomplete.Indicator />
          {isClearable && <Autocomplete.ClearButton />}
        </Autocomplete.Trigger>
        <Autocomplete.Popover className={popoverClassName}>
          <Autocomplete.Filter
            filter={(textValue, inputValue) =>
              textValue.toLowerCase().includes(inputValue.trim().toLowerCase())
            }
          >
            <SearchField aria-label={`搜索${label ?? placeholder}`}>
              <SearchField.Group>
                <SearchField.SearchIcon />
                <SearchField.Input />
                <SearchField.ClearButton />
              </SearchField.Group>
            </SearchField>
            {listBox}
          </Autocomplete.Filter>
        </Autocomplete.Popover>
      </Autocomplete>,
    );
  }
  return withTooltip(
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
      <Select.Popover className={popoverClassName}>{listBox}</Select.Popover>
    </Select>,
  );
}

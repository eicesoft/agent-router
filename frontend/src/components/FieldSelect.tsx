import { Label, ListBox, Select } from "@heroui/react";
import type { ReactNode } from "react";

export function FieldSelect<T extends string>({
  label,
  placeholder = "请选择",
  value,
  onChange,
  options,
  isDisabled,
  isClearable,
  fullWidth,
}: {
  label?: string;
  placeholder?: string;
  value: T | null;
  onChange: (value: T | null) => void;
  options: { value: T; label: ReactNode }[];
  isDisabled?: boolean;
  isClearable?: boolean;
  fullWidth?: boolean;
}) {
  return (
    <Select
      aria-label={label ?? placeholder}
      placeholder={placeholder}
      selectedKey={value ?? null}
      onSelectionChange={(key) => onChange(key === null ? null : (key as T))}
      isDisabled={isDisabled}
      onClear={() => onChange(null)}
      fullWidth={fullWidth}
    >
      {label && <Label>{label}</Label>}
      <Select.Trigger>
        <Select.Value />
        <Select.Indicator />
        {isClearable && <Select.ClearButton />}
      </Select.Trigger>
      <Select.Popover>
        <ListBox items={options}>
          {(option) => (
            <ListBox.Item
              id={option.value}
              textValue={
                typeof option.label === "string" ? option.label : option.value
              }
            >
              {option.label}
            </ListBox.Item>
          )}
        </ListBox>
      </Select.Popover>
    </Select>
  );
}

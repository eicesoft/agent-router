/**
 * react-aria-components 薄适配层的统一出口。
 * 应用代码原先 `import { … } from "@heroui/react"`，现改为 from 本模块；
 * 组件实现全部基于 react-aria-components，保留 HeroUI 的组件 API、
 * data-slot 属性与 BEM 类名，使 styles.css 的选择器契约继续成立。
 * 各组件的复合子件形状对照 @heroui/react 的 index.js 移植。
 */
export { Autocomplete } from "./autocomplete";
export { Button } from "./button";
export { Card } from "./card";
export { Checkbox } from "./checkbox";
export { CheckboxGroup } from "./checkbox-group";
export { Chip } from "./chip";
export {
  DateRangePicker,
  DateRangePickerTrigger,
  DateRangePickerTriggerIndicator,
} from "./date-range-picker";
export { Header } from "./header";
export { Input } from "./input";
export { Label } from "./label";
export { ListBox } from "./list-box";
export { Modal } from "./modal";
export { Pagination } from "./pagination";
export { Popover } from "./popover";
export { RangeCalendar } from "./range-calendar";
export { ScrollShadow } from "./scroll-shadow";
export { SearchField } from "./search-field";
export { Select } from "./select";
export { Separator } from "./separator";
export { Slider } from "./slider";
export { Switch } from "./switch";
export { Tabs } from "./tabs";
export { Tag } from "./tag";
export { TagGroup } from "./tag-group";
export { TextField } from "./textfield";
export { Tooltip } from "./tooltip";

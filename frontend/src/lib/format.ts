// 数字格式化：用量面板与 skills 面板共用同一套 token 计数显示规则。
export const num = new Intl.NumberFormat("en-US");
export const compactNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 1,
});

export function formatTokenCount(value: number) {
  if (value >= 10_000_000) return `${compactNum.format(value / 1_000_000)}M`;
  if (value >= 1_000) return `${compactNum.format(value / 1_000)}K`;
  return num.format(value);
}

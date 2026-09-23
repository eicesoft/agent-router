// 数字格式化：用量面板与 skills 面板共用同一套 token 计数显示规则。
export const num = new Intl.NumberFormat("en-US");
export const compactNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 1,
});

export function formatTokenCount(value: number) {
  // ≥1_000_000 用 M：1000K 这种写法没有信息量，1M 才是常规读法。
  if (value >= 1_000_000) return `${compactNum.format(value / 1_000_000)}M`;
  if (value >= 1_000) return `${compactNum.format(value / 1_000)}K`;
  return num.format(value);
}

// 单价（USD / 百万 tokens）展示：去掉多余尾零，2.5 → $2.5，0.0025 → $0.0025。
const priceNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 6,
  useGrouping: false,
});
export function formatPrice(value: number) {
  return `$${priceNum.format(value)}`;
}

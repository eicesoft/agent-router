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

// 单价（每百万 tokens）展示：去掉多余尾零，2.5 → 2.5，0.0025 → 0.0025。
// 不带货币符号——映射里填的不一定是美元。
const priceNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 6,
  useGrouping: false,
});
export function formatPrice(value: number) {
  return priceNum.format(value);
}

// 费用金额展示：小额保留足够精度（避免四舍五入成 0），大额按常规小数位数。
// 同 formatPrice，不加货币符号。
const costNum = new Intl.NumberFormat("en-US", {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});
const costSmallNum = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 6,
  useGrouping: false,
});
export function formatCost(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value < 0.01) return costSmallNum.format(value);
  return costNum.format(value);
}

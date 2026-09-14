import type { Provider } from "./types";

// 客户端模型名的默认生成规则：提供商配置了模型前缀时使用「前缀/模型」，
// 否则回退到「提供商名称 / 模型」，避免不同提供商出现重名客户端模型。
export function defaultClientModel(provider: Provider, model: string): string {
  const prefix = provider.modelPrefix?.trim();
  return prefix ? `${prefix}/${model}` : `${provider.name} / ${model}`;
}

import { createContext, use, type ReactNode } from "react";
import { TextContext } from "react-aria-components";

/**
 * 从 @heroui/react 的 utils/use-has-text-slot.js + field-slots-gate.js 移植。
 * Collection 根（TagGroup 等）在可见渲染前会先挂 Hidden 树，用 FieldSlotsGate
 * 包住根节点，让 Description / ErrorMessage 等 Text 槽位等到可见 pass。
 */
export const FieldSlotsGateContext = createContext(false);

export function useHasTextSlot(slot: string): boolean {
  const gated = use(FieldSlotsGateContext);
  // Slotted 场景下才有 slots；非 slotted 的 Text 上下文不带该字段。
  const textContext = use(TextContext) as
    { slots?: Record<string, unknown> } | null | undefined;
  if (!gated) return true;
  return textContext?.slots?.[slot] != null;
}

export function FieldSlotsGate({ children }: { children: ReactNode }) {
  return <FieldSlotsGateContext value={true}>{children}</FieldSlotsGateContext>;
}

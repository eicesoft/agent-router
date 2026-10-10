/** Joins class names, dropping falsy entries (tailwind-variants `cx` 的等价物). */
export function cx(
  ...classes: Array<string | false | null | undefined>
): string {
  return classes.filter(Boolean).join(" ");
}

type ClassValue = string | undefined | null | false;

/**
 * 与 HeroUI 的 composeTwRenderProps 同义：合并组件自身类名与调用方传入的
 * className（调用方在后，等权重时覆盖）。任一侧是 render-prop 函数时返回
 * 函数，供 RAC 在渲染期求值——语义对齐 react-aria 的 composeRenderProps。
 */
export function composeTwRenderProps<P extends object>(
  className: string | ((renderProps: P) => ClassValue) | undefined,
  own: ClassValue | ((renderProps: P) => ClassValue),
): string | ((renderProps: P) => string) {
  if (typeof className === "function" || typeof own === "function") {
    return (renderProps: P) =>
      cx(
        typeof own === "function" ? own(renderProps) : own,
        typeof className === "function" ? className(renderProps) : className,
      );
  }
  return cx(own, className);
}

/** `condition ? "true" : undefined` — HeroUI 的 dataAttr。 */
export function dataAttr(condition: unknown): "true" | undefined {
  return condition ? "true" : undefined;
}

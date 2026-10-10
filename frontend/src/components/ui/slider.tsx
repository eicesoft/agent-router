import type { ComponentPropsWithRef } from "react";
import { createContext, use, useMemo } from "react";
import {
  Slider as SliderPrimitive,
  SliderOutput as SliderOutputPrimitive,
  SliderThumb as SliderThumbPrimitive,
  SliderTrack as SliderTrackPrimitive,
} from "react-aria-components/Slider";
import type {
  SliderRenderProps,
  SliderState,
} from "react-aria-components/Slider";
import { dom } from "./dom";
import { composeTwRenderProps, cx, dataAttr } from "./utils";

/** 与 @heroui/styles 的 sliderVariants 同值的 BEM 类名映射。 */
interface SliderSlots {
  base: string;
  fill: string;
  marks: string;
  output: string;
  thumb: string;
  track: string;
}

const sliderSlots: SliderSlots = {
  base: "slider",
  fill: "slider__fill",
  marks: "slider__marks",
  output: "slider__output",
  thumb: "slider__thumb",
  track: "slider__track",
};

const SliderContext = createContext<{
  slots?: SliderSlots;
  state?: SliderRenderProps;
}>({ slots: sliderSlots });

/** 区间偏移（与 hero 相同：[start, end] 后默认排序）。 */
function offsetsOf(state?: SliderRenderProps): [number, number] {
  const { getThumbPercent, values } =
    state?.state ?? ({} as Partial<SliderState>);
  const [startOffset, endOffset] = [
    values?.length && values.length > 1 ? getThumbPercent?.(0) : 0,
    getThumbPercent?.(values?.length ? values.length - 1 : 0),
  ] as number[];
  return [startOffset, endOffset].sort() as [number, number];
}

export interface SliderRootProps extends ComponentPropsWithRef<
  typeof SliderPrimitive
> {}

export function SliderRoot({
  children,
  className,
  orientation = "horizontal",
  ...props
}: SliderRootProps) {
  const slots = useMemo(() => sliderSlots, []);
  return (
    <SliderPrimitive
      data-slot="slider"
      orientation={orientation}
      {...props}
      className={composeTwRenderProps(className, slots.base)}
    >
      {(values) => (
        <SliderContext value={{ slots, state: values }}>
          {typeof children === "function" ? children(values) : children}
        </SliderContext>
      )}
    </SliderPrimitive>
  );
}

export interface SliderOutputProps extends ComponentPropsWithRef<
  typeof SliderOutputPrimitive
> {}

export function SliderOutput({
  children,
  className,
  ...props
}: SliderOutputProps) {
  const { slots } = use(SliderContext);
  return (
    <SliderOutputPrimitive
      className={composeTwRenderProps(className, slots?.output)}
      data-slot="slider-output"
      {...props}
    >
      {children
        ? (values) => (
            <>{typeof children === "function" ? children(values) : children}</>
          )
        : ({ state }) =>
            state.values.map((_, i) => state.getThumbValueLabel(i)).join(" – ")}
    </SliderOutputPrimitive>
  );
}

export interface SliderTrackProps extends ComponentPropsWithRef<
  typeof SliderTrackPrimitive
> {}

export function SliderTrack({
  children,
  className,
  ...props
}: SliderTrackProps) {
  const { slots, state } = use(SliderContext);
  const singleThumb = state?.state?.values?.length === 1;
  const [startOffset, endOffset] = offsetsOf(state);
  const fillWidth = (endOffset - startOffset) * 100;
  return (
    <SliderTrackPrimitive
      className={composeTwRenderProps(className, slots?.track)}
      data-disabled={dataAttr(state?.isDisabled)}
      data-slot="slider-track"
      {...(singleThumb
        ? {
            "data-fill-start": dataAttr(fillWidth > 0),
            "data-fill-end": dataAttr(fillWidth == 100),
          }
        : {
            "data-fill-start": dataAttr(startOffset == 0),
            "data-fill-end": dataAttr(startOffset * 100 + fillWidth == 100),
          })}
      {...props}
    >
      {(values) => (
        <>{typeof children === "function" ? children(values) : children}</>
      )}
    </SliderTrackPrimitive>
  );
}

export interface SliderFillProps extends ComponentPropsWithRef<"div"> {}

export function SliderFill({ className, style, ...props }: SliderFillProps) {
  const { slots, state } = use(SliderContext);
  const isVertical = state?.state?.orientation === "vertical";
  const [startOffset, endOffset] = offsetsOf(state);
  return (
    <dom.div
      className={cx(slots?.fill, className)}
      data-disabled={dataAttr(state?.isDisabled)}
      data-slot="slider-fill"
      style={{
        ...style,
        // TODO: rtl support
        [isVertical ? "bottom" : "left"]: `${startOffset * 100}%`,
        ...(isVertical
          ? { height: `${(endOffset - startOffset) * 100}%` }
          : { width: `${(endOffset - startOffset) * 100}%` }),
      }}
      {...props}
    />
  );
}

export interface SliderThumbProps extends ComponentPropsWithRef<
  typeof SliderThumbPrimitive
> {}

export function SliderThumb({
  children,
  className,
  ...props
}: SliderThumbProps) {
  const { slots } = use(SliderContext);
  return (
    <SliderThumbPrimitive
      className={composeTwRenderProps(className, slots?.thumb)}
      data-slot="slider-thumb"
      {...props}
    >
      {(values) => (
        <>{typeof children === "function" ? children(values) : children}</>
      )}
    </SliderThumbPrimitive>
  );
}

export interface SliderMarksProps extends ComponentPropsWithRef<"div"> {}

export function SliderMarks({ className, ...props }: SliderMarksProps) {
  const { slots } = use(SliderContext);
  return (
    <dom.div
      className={cx(slots?.marks, className)}
      data-slot="slider-marks"
      {...props}
    />
  );
}

export const Slider = Object.assign(SliderRoot, {
  Root: SliderRoot,
  Output: SliderOutput,
  Track: SliderTrack,
  Fill: SliderFill,
  Thumb: SliderThumb,
  Marks: SliderMarks,
});

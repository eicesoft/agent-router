import {
  cloneElement,
  createContext,
  isValidElement,
  use,
  useMemo,
  type ComponentPropsWithRef,
  type ReactNode,
} from "react";
import {
  Dialog,
  DialogTrigger,
  Heading,
  OverlayArrow,
  Popover as PopoverPrimitive,
  Pressable,
} from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";
import { dom } from "./dom";
import { SurfaceContext } from "./surface";

/** 与 @heroui/styles 的 popoverVariants 同值的 BEM 类名。 */
interface PopoverSlots {
  base: string;
  dialog: string;
  heading: string;
  trigger: string;
}

const popoverSlots: PopoverSlots = {
  base: "popover",
  dialog: "popover__dialog",
  heading: "popover__heading",
  trigger: "popover__trigger",
};

/** composeSlotClassName 的字符串版：slots 缺省时原样返回 className（可为 undefined）。 */
function slotClass(slot: string | undefined, className: string | undefined) {
  return slot === undefined ? className : cx(slot, className);
}

const PopoverContext = createContext<{ slots?: PopoverSlots }>({});

/* -------------------------------------------------------------------------------------------------
 * Popover Root
 * -----------------------------------------------------------------------------------------------*/

type PopoverRootProps = ComponentPropsWithRef<typeof DialogTrigger>;

/** 从 @heroui/react 的 popover/popover.js 移植。 */
const PopoverRoot = ({ children, ...props }: PopoverRootProps) => {
  const slots = useMemo(() => popoverSlots, []);
  return (
    <PopoverContext value={{ slots }}>
      <DialogTrigger data-slot="popover-root" {...props}>
        {children}
      </DialogTrigger>
    </PopoverContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Popover Content
 * -----------------------------------------------------------------------------------------------*/

interface PopoverContentProps extends Omit<
  ComponentPropsWithRef<typeof PopoverPrimitive>,
  "children"
> {
  children: ReactNode;
}

const PopoverContent = ({
  children,
  className,
  ...props
}: PopoverContentProps) => {
  const { slots } = use(PopoverContext);
  return (
    <PopoverContext value={{ slots }}>
      <SurfaceContext value={{ variant: "default" }}>
        <PopoverPrimitive
          {...props}
          className={composeTwRenderProps(className, slots?.base)}
        >
          {children}
        </PopoverPrimitive>
      </SurfaceContext>
    </PopoverContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Popover Arrow
 * -----------------------------------------------------------------------------------------------*/

type PopoverArrowProps = Omit<
  ComponentPropsWithRef<typeof OverlayArrow>,
  "children"
> & {
  children?: ReactNode;
};

const PopoverArrow = ({ children, className, ...props }: PopoverArrowProps) => {
  const defaultArrow = (
    <svg
      data-slot="popover-overlay-arrow"
      fill="none"
      height="12"
      viewBox="0 0 12 12"
      width="12"
      xmlns="http://www.w3.org/2000/svg"
    >
      <path d="M0 0C5.48483 8 6.5 8 12 0Z" />
    </svg>
  );
  const arrow = isValidElement(children)
    ? cloneElement(children as React.ReactElement<{ "data-slot"?: string }>, {
        "data-slot": "popover-overlay-arrow",
      })
    : defaultArrow;
  return (
    <OverlayArrow
      data-slot="popover-overlay-arrow-group"
      {...props}
      className={className}
    >
      {arrow}
    </OverlayArrow>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Popover Dialog
 * -----------------------------------------------------------------------------------------------*/

type PopoverDialogProps = Omit<
  ComponentPropsWithRef<typeof Dialog>,
  "children"
> & {
  children: ReactNode;
};

const PopoverDialog = ({
  children,
  className,
  ...props
}: PopoverDialogProps) => {
  const { slots } = use(PopoverContext);
  return (
    <Dialog
      data-slot="popover-dialog"
      {...props}
      className={slotClass(slots?.dialog, className)}
    >
      {children}
    </Dialog>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Popover Trigger
 * -----------------------------------------------------------------------------------------------*/

type PopoverTriggerProps = ComponentPropsWithRef<"div">;

const PopoverTrigger = ({
  children,
  className,
  ...props
}: PopoverTriggerProps) => {
  const { slots } = use(PopoverContext);
  return (
    <Pressable>
      <dom.div
        className={slotClass(slots?.trigger, className)}
        data-slot="popover-trigger"
        role="button"
        {...props}
        children={children}
      />
    </Pressable>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Popover Heading
 * -----------------------------------------------------------------------------------------------*/

type PopoverHeadingProps = ComponentPropsWithRef<typeof Heading>;

const PopoverHeading = ({
  children,
  className,
  ...props
}: PopoverHeadingProps) => {
  const { slots } = use(PopoverContext);
  return (
    <Heading
      slot="title"
      {...props}
      className={slotClass(slots?.heading, className)}
    >
      {children}
    </Heading>
  );
};

export {
  PopoverRoot,
  PopoverTrigger,
  PopoverDialog,
  PopoverArrow,
  PopoverContent,
  PopoverHeading,
};

/** 复合组件形状与 @heroui/react 的 popover/index.js 一致。 */
export const Popover = Object.assign(PopoverRoot, {
  Root: PopoverRoot,
  Trigger: PopoverTrigger,
  Dialog: PopoverDialog,
  Arrow: PopoverArrow,
  Content: PopoverContent,
  Heading: PopoverHeading,
});

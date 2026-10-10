import { createContext, use, useMemo, type ComponentPropsWithRef } from "react";
import {
  Button as ButtonPrimitive,
  Dialog,
  DialogTrigger,
  Heading,
  Modal as ModalPrimitive,
  ModalOverlay,
  Pressable,
} from "react-aria-components";
import { composeTwRenderProps, cx } from "./utils";
import { dom } from "./dom";
import { CloseIcon } from "./icons";
import { SurfaceContext } from "./surface";

/** 与 @heroui/styles 的 modalVariants 同值的 BEM 类名映射。 */
export type ModalScroll = "inside" | "outside";
export type ModalSize = "cover" | "full" | "lg" | "md" | "sm" | "xs";
export type ModalVariant = "blur" | "opaque" | "transparent";
export type ModalPlacement = "auto" | "top" | "center" | "bottom";

interface ModalSlots {
  backdrop: string;
  body: string;
  closeTrigger: string;
  container: string;
  dialog: string;
  footer: string;
  header: string;
  heading: string;
  icon: string;
  trigger: string;
}

const backdropVariantClass: Record<ModalVariant, string> = {
  blur: "modal__backdrop--blur",
  opaque: "modal__backdrop--opaque",
  transparent: "modal__backdrop--transparent",
};

const scrollClass: Record<
  ModalScroll,
  { body: string; dialog: string; container?: string }
> = {
  inside: {
    body: "modal__body--scroll-inside",
    dialog: "modal__dialog--scroll-inside",
  },
  outside: {
    body: "modal__body--scroll-outside",
    container: "modal__container--scroll-outside",
    dialog: "modal__dialog--scroll-outside",
  },
};

const sizeClass: Record<ModalSize, { dialog: string; container?: string }> = {
  cover: { dialog: "modal__dialog--cover" },
  full: { container: "modal__container--full", dialog: "modal__dialog--full" },
  lg: { dialog: "modal__dialog--lg" },
  md: { dialog: "modal__dialog--md" },
  sm: { dialog: "modal__dialog--sm" },
  xs: { dialog: "modal__dialog--xs" },
};

/** tv() 的等价物：base 先、variants 按声明顺序（scroll → size → variant）。 */
function modalVariants({
  scroll = "inside",
  size = "md",
  variant = "opaque",
}: {
  scroll?: ModalScroll;
  size?: ModalSize;
  variant?: ModalVariant;
} = {}): ModalSlots {
  return {
    backdrop: cx("modal__backdrop", backdropVariantClass[variant]),
    body: cx("modal__body", scrollClass[scroll].body),
    closeTrigger: "modal__close-trigger",
    container: cx(
      "modal__container",
      scrollClass[scroll].container,
      sizeClass[size].container,
    ),
    dialog: cx(
      "modal__dialog",
      scrollClass[scroll].dialog,
      sizeClass[size].dialog,
    ),
    footer: "modal__footer",
    header: "modal__header",
    heading: "modal__heading",
    icon: "modal__icon",
    trigger: "modal__trigger",
  };
}

/** composeSlotClassName 的字符串版：slots 缺省时原样返回 className（可为 undefined）。 */
function slotClass(slot: string | undefined, className: string | undefined) {
  return slot === undefined ? className : cx(slot, className);
}

interface ModalContextValue {
  slots?: ModalSlots;
  placement?: ModalPlacement;
}

const ModalContext = createContext<ModalContextValue>({});

/* -------------------------------------------------------------------------------------------------
 * Modal Root
 * -----------------------------------------------------------------------------------------------*/

type ModalRootProps = ComponentPropsWithRef<typeof DialogTrigger> & {
  /** heroui 的受控 state 桥（isOpen/setOpen）；应用侧未使用。 */
  state?: { isOpen: boolean; setOpen: (open: boolean) => void };
};

/** 从 @heroui/react 的 modal/modal.js 移植。 */
const ModalRoot = ({ children, state, ...props }: ModalRootProps) => {
  const modalContext = useMemo<ModalContextValue>(
    () => ({ slots: modalVariants(), placement: undefined }),
    [],
  );
  const controlledProps = state
    ? { isOpen: state.isOpen, onOpenChange: state.setOpen }
    : {};
  return (
    <ModalContext value={modalContext}>
      <DialogTrigger data-slot="modal-root" {...props} {...controlledProps}>
        {children}
      </DialogTrigger>
    </ModalContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Trigger
 * -----------------------------------------------------------------------------------------------*/

type ModalTriggerProps = ComponentPropsWithRef<"div">;

const ModalTrigger = ({ children, className, ...props }: ModalTriggerProps) => {
  const { slots } = use(ModalContext);
  return (
    <Pressable>
      <dom.div
        className={slotClass(slots?.trigger, className)}
        data-slot="modal-trigger"
        role="button"
        {...props}
        children={children}
      />
    </Pressable>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Backdrop
 * -----------------------------------------------------------------------------------------------*/

type ModalBackdropProps = Omit<
  ComponentPropsWithRef<typeof ModalOverlay>,
  "isDismissable"
> & {
  variant?: ModalVariant;
  /** @default true */
  isDismissable?: boolean;
};

const ModalBackdrop = ({
  children,
  className,
  isDismissable = true,
  onClick,
  variant,
  ...props
}: ModalBackdropProps) => {
  const { slots: contextSlots } = use(ModalContext);
  const updatedSlots = useMemo(() => modalVariants({ variant }), [variant]);
  const updatedModalContext = useMemo<ModalContextValue>(
    () => ({
      slots: { ...contextSlots, ...updatedSlots },
    }),
    [contextSlots, updatedSlots],
  );
  return (
    <ModalOverlay
      className={composeTwRenderProps(className, updatedSlots.backdrop)}
      data-slot="modal-backdrop"
      isDismissable={isDismissable}
      onClick={(e) => {
        e.stopPropagation();
        onClick?.(e);
      }}
      {...props}
    >
      {(renderProps) => (
        <ModalContext value={updatedModalContext}>
          {typeof children === "function"
            ? children(renderProps)
            : children}{" "}
        </ModalContext>
      )}
    </ModalOverlay>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Container
 * -----------------------------------------------------------------------------------------------*/

type ModalContainerProps = Omit<
  ComponentPropsWithRef<typeof ModalPrimitive>,
  "isOpen" | "onOpenChange" | "defaultOpen"
> & {
  placement?: ModalPlacement;
  scroll?: ModalScroll;
  size?: ModalSize;
};

const ModalContainer = ({
  children,
  className,
  placement = "auto",
  scroll,
  size,
  ...props
}: ModalContainerProps) => {
  const { slots: contextSlots } = use(ModalContext);
  const updatedSlots = useMemo(
    () => modalVariants({ scroll, size }),
    [scroll, size],
  );
  const updatedModalContext = useMemo<ModalContextValue>(
    () => ({
      placement,
      slots: { ...contextSlots, ...updatedSlots },
    }),
    [contextSlots, placement, updatedSlots],
  );
  return (
    <ModalPrimitive
      className={composeTwRenderProps(className, updatedSlots.container)}
      data-placement={placement}
      data-slot="modal-container"
      {...props}
    >
      {(renderProps) => (
        <ModalContext value={updatedModalContext}>
          {typeof children === "function" ? children(renderProps) : children}
        </ModalContext>
      )}
    </ModalPrimitive>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Dialog
 * -----------------------------------------------------------------------------------------------*/

type ModalDialogProps = ComponentPropsWithRef<typeof Dialog>;

const ModalDialog = ({ children, className, ...props }: ModalDialogProps) => {
  const { placement, slots } = use(ModalContext);
  return (
    <SurfaceContext value={{ variant: "default" }}>
      <Dialog
        className={slotClass(slots?.dialog, className)}
        data-placement={placement}
        data-slot="modal-dialog"
        {...props}
      >
        {children}
      </Dialog>
    </SurfaceContext>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Header / Body / Footer / Icon
 * -----------------------------------------------------------------------------------------------*/

type ModalPartProps = ComponentPropsWithRef<"div">;

const ModalHeader = ({ children, className, ...props }: ModalPartProps) => {
  const { slots } = use(ModalContext);
  return (
    <dom.div
      className={slotClass(slots?.header, className)}
      data-slot="modal-header"
      {...props}
      children={children}
    />
  );
};

const ModalBody = ({ children, className, ...props }: ModalPartProps) => {
  const { slots } = use(ModalContext);
  return (
    <dom.div
      className={slotClass(slots?.body, className)}
      data-slot="modal-body"
      {...props}
      children={children}
    />
  );
};

const ModalFooter = ({ children, className, ...props }: ModalPartProps) => {
  const { slots } = use(ModalContext);
  return (
    <dom.div
      className={slotClass(slots?.footer, className)}
      data-slot="modal-footer"
      {...props}
      children={children}
    />
  );
};

const ModalIcon = ({ children, className, ...props }: ModalPartProps) => {
  const { slots } = use(ModalContext);
  return (
    <dom.div
      className={slotClass(slots?.icon, className)}
      data-slot="modal-icon"
      {...props}
      children={children}
    />
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Heading
 * -----------------------------------------------------------------------------------------------*/

type ModalHeadingProps = ComponentPropsWithRef<typeof Heading>;

const ModalHeading = ({ children, className, ...props }: ModalHeadingProps) => {
  const { slots } = use(ModalContext);
  return (
    <Heading
      className={slotClass(slots?.heading, className)}
      data-slot="modal-heading"
      slot="title"
      {...props}
    >
      {children}
    </Heading>
  );
};

/* -------------------------------------------------------------------------------------------------
 * Close Button（heroui close-button 内联，规则要求不单建文件）
 * -----------------------------------------------------------------------------------------------*/

type CloseButtonProps = ComponentPropsWithRef<typeof ButtonPrimitive>;

const CloseButton = ({
  children,
  className,
  slot,
  style,
  ...rest
}: CloseButtonProps) => {
  const own = "close-button close-button--default";
  return (
    <ButtonPrimitive
      aria-label="Close"
      className={composeTwRenderProps(className, own)}
      data-slot="close-button"
      slot={slot}
      style={style}
      type="button"
      {...rest}
      children={(renderProps) =>
        typeof children === "function"
          ? children(renderProps)
          : (children ?? <CloseIcon data-slot="close-button-icon" />)
      }
    />
  );
};

/* -------------------------------------------------------------------------------------------------
 * Modal Close Trigger
 * -----------------------------------------------------------------------------------------------*/

type ModalCloseTriggerProps = ComponentPropsWithRef<typeof ButtonPrimitive>;

const ModalCloseTrigger = ({ className, ...rest }: ModalCloseTriggerProps) => {
  const { slots } = use(ModalContext);
  return (
    <CloseButton
      className={composeTwRenderProps(className, slots?.closeTrigger)}
      data-slot="modal-close-trigger"
      slot="close"
      {...rest}
    />
  );
};

export {
  ModalRoot,
  ModalTrigger,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalIcon,
  ModalHeading,
  ModalBody,
  ModalFooter,
  ModalCloseTrigger,
};

/** 复合组件形状与 @heroui/react 的 modal/index.js 一致。 */
export const Modal = Object.assign(ModalRoot, {
  Root: ModalRoot,
  Trigger: ModalTrigger,
  Backdrop: ModalBackdrop,
  Container: ModalContainer,
  Dialog: ModalDialog,
  Header: ModalHeader,
  Icon: ModalIcon,
  Heading: ModalHeading,
  Body: ModalBody,
  Footer: ModalFooter,
  CloseTrigger: ModalCloseTrigger,
});

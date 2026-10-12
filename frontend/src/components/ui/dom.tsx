import {
  useMemo,
  useRef,
  type ComponentType,
  type ReactNode,
  type MutableRefObject,
  type Ref,
} from "react";

type AnyProps = Record<string, unknown> & { ref?: Ref<unknown> };

function assignRef(ref: Ref<unknown> | undefined, node: unknown) {
  if (typeof ref === "function") ref(node);
  else if (ref && typeof ref === "object")
    (ref as MutableRefObject<unknown>).current = node;
}

/**
 * 从 @heroui/react 的 utils/dom.js 移植：生成透传 ref 的元素代理
 * （dom.span / dom.div …）。heroui 用它支持自定义 `render` prop；适配层
 * 保留同一行为，保证移植组件可逐行对照源码。
 */
function DOMElement(ElementType: string, props: AnyProps): ReactNode {
  const {
    ref: forwardedRef,
    render,
    ...otherProps
  } = props as AnyProps & {
    render?: (domProps: AnyProps, _: undefined) => ReactNode;
  };
  const elementRef = useRef<HTMLElement | null>(null);
  const ref = (node: HTMLElement | null) => {
    elementRef.current = node;
    assignRef(forwardedRef, node);
  };
  const domProps: AnyProps = { ...otherProps, ref };
  if (render) return render(domProps, undefined);
  const Element = ElementType as unknown as ComponentType<AnyProps>;
  return <Element {...domProps} />;
}

const domComponentCache: Record<string, ComponentType<AnyProps>> = {};

export const dom: Record<string, ComponentType<AnyProps>> = new Proxy(
  {},
  {
    get(_target, elementType) {
      if (typeof elementType !== "string") return undefined;
      let res = domComponentCache[elementType];
      if (!res) {
        const type = elementType;
        res = (props: AnyProps) =>
          DOMElement(type, props) as React.ReactElement;
        domComponentCache[elementType] = res;
      }
      return res;
    },
  },
) as Record<string, ComponentType<AnyProps>>;

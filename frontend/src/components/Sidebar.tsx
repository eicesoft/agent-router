import {
  Bot,
  Cable,
  ChartNoAxesCombined,
  KeyRound,
  LayoutDashboard,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Settings2,
  SlidersHorizontal,
} from "lucide-react";
import { Button, Tooltip } from "@heroui/react";

export type Page =
  "overview" | "providers" | "keys" | "mappings" | "agents" | "logs" | "usage";
const items = [
  ["overview", "控制台", LayoutDashboard],
  ["providers", "提供商", Cable],
  ["keys", "本地密钥", KeyRound],
  ["mappings", "模型映射", SlidersHorizontal],
  ["agents", "Agent", Bot],
] as const;
export function Sidebar({
  page,
  setPage,
  collapsed,
  onToggle,
  providerCount,
  keyCount,
  mappingCount,
  proxyRunning,
  width,
}: {
  page: Page;
  setPage: (page: Page) => void;
  collapsed: boolean;
  onToggle: () => void;
  providerCount: number;
  keyCount: number;
  mappingCount: number;
  proxyRunning: boolean;
  width: number;
}) {
  if (collapsed)
    return (
      <aside className="icon-rail">
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          className="titlebar-sidebar-toggle"
          onPress={onToggle}
          aria-label="展开侧栏"
        >
          <PanelLeftOpen size={18} />
        </Button>
        <div className="rail-top-spacer" />
        {items.map(([id, label, Icon]) => (
          <Button
            isIconOnly
            size="sm"
            variant="ghost"
            key={id}
            className={page === id ? "rail-action current" : "rail-action"}
            aria-label={label}
            onPress={() => setPage(id)}
          >
            <Icon size={18} />
          </Button>
        ))}
        <div className="rail-divider" />
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          className={page === "logs" ? "rail-action current" : "rail-action"}
          aria-label="请求日志"
          onPress={() => setPage("logs")}
        >
          <ScrollText size={18} />
        </Button>
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          className={page === "usage" ? "rail-action current" : "rail-action"}
          aria-label="使用情况"
          onPress={() => setPage("usage")}
        >
          <ChartNoAxesCombined size={18} />
        </Button>
        <div className="rail-spacer" />
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          className="rail-action"
          aria-label="设置"
        >
          <Settings2 size={18} />
        </Button>
        <span
          className={
            proxyRunning ? "rail-proxy-status" : "rail-proxy-status off"
          }
          title={
            proxyRunning ? "本地代理运行中 127.0.0.1:9400" : "本地代理已停止"
          }
        />
      </aside>
    );
  return (
    <aside className="sidebar" style={{ width, flexBasis: width }}>
      <Button
        isIconOnly
        size="sm"
        variant="ghost"
        className="titlebar-sidebar-toggle"
        onPress={onToggle}
        aria-label="收缩侧栏"
      >
        <PanelLeftClose size={17} />
      </Button>
      <div className="nav-label nav-label-first">导航</div>
      <nav>
        {items.map(([id, label, Icon]) => (
          <Button
            size="sm"
            variant="ghost"
            key={id}
            className={page === id ? "nav-item active" : "nav-item"}
            onPress={() => setPage(id)}
          >
            <Icon size={17} />
            {label}
            {id === "providers" && providerCount > 0 && (
              <span className="nav-count">{providerCount}</span>
            )}
            {id === "keys" && keyCount > 0 && (
              <span className="nav-count">{keyCount}</span>
            )}
            {id === "mappings" && mappingCount > 0 && (
              <span className="nav-count">{mappingCount}</span>
            )}
          </Button>
        ))}
      </nav>
      <div className="nav-label nav-label-space">资源</div>
      <Button
        size="sm"
        variant="ghost"
        className={page === "logs" ? "nav-item active" : "nav-item muted"}
        onPress={() => setPage("logs")}
      >
        <ScrollText size={17} />
        请求日志
      </Button>
      <Button
        size="sm"
        variant="ghost"
        className={page === "usage" ? "nav-item active" : "nav-item muted"}
        onPress={() => setPage("usage")}
      >
        <ChartNoAxesCombined size={17} />
        使用情况
      </Button>
      <div className="sidebar-foot">
        <Tooltip>
          <Tooltip.Trigger>
            <div className="sidebar-foot-status">
              <span
                className={proxyRunning ? "online-dot" : "online-dot off"}
              />
              <span>{proxyRunning ? "运行中" : "已停止"}</span>
            </div>
          </Tooltip.Trigger>
          <Tooltip.Content>
            {proxyRunning ? "监听 127.0.0.1:9400" : "本地代理已停止"}
          </Tooltip.Content>
        </Tooltip>
        <Button
          size="sm"
          variant="ghost"
          className="collapse-button"
          aria-label="设置"
        >
          <Settings2 size={15} />
        </Button>
      </div>
    </aside>
  );
}

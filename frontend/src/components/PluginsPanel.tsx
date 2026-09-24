import { useEffect, useState } from "react";
import { Button, Chip, Switch, Tooltip } from "@heroui/react";
import { RefreshCw, Settings2 } from "lucide-react";
import { listPlugins, setPluginConfig, togglePlugin } from "../lib/api";
import type { PluginInfo } from "../lib/types";
import { formatTokenCount } from "../lib/format";
import { FieldSelect } from "./FieldSelect";

const kindLabel: Record<PluginInfo["kind"], string> = {
  input: "输入",
  output: "输出",
};

// caveman 六档：与 backend/plugin CavemanLevels 对齐。
const CAVEMAN_LEVELS: { value: string; label: string }[] = [
  { value: "lite", label: "Lite" },
  { value: "full", label: "Full" },
  { value: "ultra", label: "Ultra" },
  { value: "wenyan-lite", label: "文Lite" },
  { value: "wenyan-full", label: "文Full" },
  { value: "wenyan-ultra", label: "文Ultra" },
];

function cavemanLevelLabel(level: string): string {
  return CAVEMAN_LEVELS.find((item) => item.value === level)?.label ?? level;
}

// 功能插件列表：类型 / 启停 / 累计压缩统计；有配置才显示设置入口。
export function PluginsPanel() {
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [error, setError] = useState("");
  const [toggling, setToggling] = useState<string | null>(null);
  const [configuring, setConfiguring] = useState<string | null>(null);
  const [savingConfig, setSavingConfig] = useState<string | null>(null);

  const reload = () => {
    listPlugins()
      .then((list) => {
        setPlugins(list);
        setError("");
      })
      .catch((e) => setError(String(e)));
  };
  useEffect(reload, []);

  const onToggle = async (id: string, enabled: boolean) => {
    setToggling(id);
    try {
      const next = await togglePlugin(id, enabled);
      setPlugins(next);
      setError("");
    } catch (e) {
      setError(String(e));
    } finally {
      setToggling(null);
    }
  };

  const onLevelChange = async (id: string, level: string) => {
    setSavingConfig(id);
    try {
      const next = await setPluginConfig(id, { level });
      setPlugins(next);
      setError("");
    } catch (e) {
      setError(String(e));
    } finally {
      setSavingConfig(null);
    }
  };

  if (!plugins) return <div className="loading">正在加载插件…</div>;

  const pct = (rate: number) =>
    rate > 0 ? `${(rate * 100).toFixed(1)}%` : "0%";

  return (
    <div className="plugins-panel">
      {error && <div className="skills-error">{error}</div>}
      <div className="skills-toolbar">
        <span className="plugins-toolbar-hint">
          插件在转发前处理请求；会话去重无损省略，Caveman
          压长工具/日志并注入短话术（内嵌免安装）。
        </span>
        <Button size="sm" variant="ghost" onPress={reload} aria-label="刷新">
          <RefreshCw size={15} />
          刷新
        </Button>
      </div>
      <div className="plugins-list">
        {plugins.map((p) => (
          <div key={p.id} className="plugins-card">
            <div className="plugins-card-head">
              <div className="plugins-card-title">
                <span className="plugins-card-name">{p.name}</span>
                <Chip size="sm" variant="soft">
                  {kindLabel[p.kind] ?? p.kind}
                </Chip>
                {p.hasConfig && (
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="ghost"
                        className="plugins-config-btn"
                        aria-label={`配置${p.name}`}
                        onPress={() =>
                          setConfiguring(configuring === p.id ? null : p.id)
                        }
                      >
                        <Settings2 size={14} />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>插件设置</Tooltip.Content>
                  </Tooltip>
                )}
              </div>
              <Tooltip>
                <Tooltip.Trigger>
                  <Switch
                    size="sm"
                    isSelected={p.enabled}
                    isDisabled={toggling === p.id}
                    aria-label={`启用${p.name}`}
                    onChange={(enabled) => void onToggle(p.id, enabled)}
                  >
                    <Switch.Content>
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                    </Switch.Content>
                  </Switch>
                </Tooltip.Trigger>
                <Tooltip.Content>
                  {p.enabled ? "已开启" : "已关闭"}
                </Tooltip.Content>
              </Tooltip>
            </div>
            <p className="plugins-card-desc">{p.description}</p>
            {p.hasConfig && configuring === p.id && p.id === "caveman" && (
              <div className="plugins-card-config">
                <FieldSelect
                  label="处理级别"
                  value={p.config?.level ?? "full"}
                  onChange={(value) => {
                    if (value) void onLevelChange(p.id, value);
                  }}
                  options={CAVEMAN_LEVELS}
                  isDisabled={savingConfig === p.id}
                  fullWidth
                />
                <div className="plugins-config-current">
                  当前：{cavemanLevelLabel(p.config?.level ?? "full")}
                </div>
              </div>
            )}
            <div className="plugins-stats">
              <div className="plugins-stat">
                <span className="plugins-stat-label">输入Token</span>
                <span className="plugins-stat-value">
                  {formatTokenCount(p.inputTokens)}
                </span>
              </div>
              <div className="plugins-stat">
                <span className="plugins-stat-label">压缩后Token</span>
                <span className="plugins-stat-value">
                  {formatTokenCount(p.outputTokens)}
                </span>
              </div>
              <div className="plugins-stat">
                <span className="plugins-stat-label">压缩率</span>
                <span className="plugins-stat-value">
                  {pct(p.compressionRate)}
                </span>
              </div>
              <div className="plugins-stat">
                <span className="plugins-stat-label">差异值</span>
                <span className="plugins-stat-value">
                  {formatTokenCount(p.savedTokens)}
                </span>
              </div>
            </div>
            {p.id === "caveman" && (
              <div className="plugins-stats plugins-stats-output">
                <div className="plugins-stat">
                  <span className="plugins-stat-label">输出均值·开启</span>
                  <span className="plugins-stat-value">
                    {p.responseCount > 0
                      ? formatTokenCount(Math.round(p.responseAvg))
                      : "—"}
                  </span>
                </div>
                <div className="plugins-stat">
                  <span className="plugins-stat-label">输出均值·关闭</span>
                  <span className="plugins-stat-value">
                    {p.baselineCount > 0
                      ? formatTokenCount(Math.round(p.baselineAvg))
                      : "—"}
                  </span>
                </div>
                <div className="plugins-stat">
                  <span className="plugins-stat-label">输出节省</span>
                  <span className="plugins-stat-value">
                    {p.outputSavingsRate > 0 ? pct(p.outputSavingsRate) : "—"}
                  </span>
                </div>
                <div className="plugins-stat">
                  <span className="plugins-stat-label">样本·开/关</span>
                  <span className="plugins-stat-value">
                    {p.responseCount}/{p.baselineCount}
                  </span>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

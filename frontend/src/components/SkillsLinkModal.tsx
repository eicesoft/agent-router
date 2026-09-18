import { useEffect, useState } from "react";
import { Button, Checkbox, Modal } from "@heroui/react";
import { RefreshCw } from "lucide-react";
import { listSkillLinks, setSkillLink } from "../lib/api";
import type { SkillLink, SkillLinkState, ToolSkillLinks } from "../lib/types";

// 把 app 管理的 skills 以符号链接发布到某个 CLI 的 skills 目录。
// 切换即时生效（symlink 操作足够轻，不需要「保存」按钮），每次切换后重新
// 拉取状态，避免本地猜测链接是否真的建成。
export function SkillsLinkModal({
  toolId,
  toolName,
  isOpen,
  onClose,
}: {
  toolId: string;
  toolName: string;
  isOpen: boolean;
  onClose: () => void;
}) {
  const [data, setData] = useState<ToolSkillLinks>({
    targetDir: "",
    links: [],
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  const reload = () => {
    listSkillLinks(toolId)
      .then((d) => {
        setData(d);
        setError("");
      })
      .catch((e) => setError(String(e)));
  };
  useEffect(() => {
    if (isOpen) reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, toolId]);

  const toggle = async (link: SkillLink, on: boolean) => {
    setBusy(link.name);
    try {
      await setSkillLink(toolId, link.dir, on);
      setData(await listSkillLinks(toolId));
      setError("");
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy("");
    }
  };

  // 只有异常态需要标签：正常链接与未链接由勾选框本身表达。
  const stateLabel = (state: SkillLinkState): string =>
    state === "broken" ? "失效" : state === "external" ? "外部" : "";
  const stateHint = (state: SkillLinkState): string | undefined =>
    state === "broken"
      ? "源目录已被删除，取消勾选即可清理这个链接"
      : state === "external"
        ? "不是本应用创建的条目，不会改动"
        : undefined;

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={(open: boolean) => {
        if (!open) onClose();
      }}
    >
      <Modal.Backdrop>
        <Modal.Container size="md">
          <Modal.Dialog>
            <Modal.Header>
              <Modal.Heading>Skills · {toolName}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="skills-item-path-row">
                <code className="skills-item-path" title={data.targetDir}>
                  {data.targetDir}
                </code>
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  onPress={reload}
                  aria-label="刷新"
                >
                  <RefreshCw size={14} />
                </Button>
              </div>
              {error && <div className="skills-error">{error}</div>}
              <div className="skills-link-list">
                {data.links.length === 0 && (
                  <div className="skills-empty">没有可链接的 skill</div>
                )}
                {data.links.map((link) => (
                  <div className="skills-link-row" key={link.name}>
                    <Checkbox
                      isSelected={
                        link.state === "linked" || link.state === "broken"
                      }
                      isDisabled={
                        link.state === "external" || busy === link.name
                      }
                      onChange={(on: boolean) => void toggle(link, on)}
                    >
                      <Checkbox.Content>
                        <Checkbox.Control>
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                        <span className="skills-link-name" title={link.dir}>
                          {link.name}
                        </span>
                      </Checkbox.Content>
                    </Checkbox>
                    {stateLabel(link.state) && (
                      <span
                        className="skills-link-state"
                        title={stateHint(link.state)}
                      >
                        {stateLabel(link.state)}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

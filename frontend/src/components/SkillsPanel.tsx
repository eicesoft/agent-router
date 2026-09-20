import { useEffect, useMemo, useState } from "react";
import { Button, Modal, Switch, Tooltip } from "@heroui/react";
import {
  Check,
  Copy,
  RefreshCw,
  Save,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { ClipboardSetText } from "../../wailsjs/runtime/runtime";
import {
  deleteSkill,
  getSkill,
  listSkills,
  saveSkillBody,
  toggleSkill,
} from "../lib/api";
import { renderMarkdown } from "../lib/markdown";
import { formatTokenCount, num } from "../lib/format";
import type { Skill, SkillDetail, SkillSummary } from "../lib/types";

// Skills 管理页。数据在挂载时自取（文件系统扫描无缓存，每次进入都刷新），
// 不走 App.tsx 的 Bootstrap 通道。
export function SkillsPanel({
  onSummary,
}: {
  onSummary?: (summary: SkillSummary) => void;
}) {
  const [summary, setSummary] = useState<SkillSummary>({
    roots: [],
    skills: [],
    conflicts: [],
  });
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState("all");
  const [detail, setDetail] = useState<SkillDetail | null>(null);
  const [editBody, setEditBody] = useState("");
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [toDelete, setToDelete] = useState<Skill | null>(null);
  const [copiedPath, setCopiedPath] = useState(false);
  useEffect(() => {
    if (!copiedPath) return;
    const timer = setTimeout(() => setCopiedPath(false), 1400);
    return () => clearTimeout(timer);
  }, [copiedPath]);
  const copyPath = async () => {
    if (!detail) return;
    try {
      await ClipboardSetText(detail.dir);
      setCopiedPath(true);
    } catch {
      // 剪贴板失败时静默，图标不变即可
    }
  };

  const reload = () => {
    listSkills()
      .then((s) => {
        setSummary(s);
        onSummary?.(s);
      })
      .then(() => setError(""))
      .catch((e) => setError(String(e)));
  };
  useEffect(reload, []);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return summary.skills.filter((s) => {
      if (sourceFilter !== "all" && s.source !== sourceFilter) return false;
      if (!q) return true;
      return (
        s.name.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q)
      );
    });
  }, [summary.skills, search, sourceFilter]);

  const openDetail = (skill: Skill) => {
    setDetailError("");
    setEditing(false);
    getSkill(skill.dir)
      .then((d) => {
        setDetail(d);
        setEditBody(d.body);
      })
      .catch((e) => setDetailError(String(e)));
  };

  const persistBody = async () => {
    if (!detail) return;
    setSaving(true);
    try {
      await saveSkillBody(detail.dir, editBody);
      setEditing(false);
      const d = await getSkill(detail.dir);
      setDetail(d);
      setEditBody(d.body);
      reload();
    } catch (e) {
      setDetailError(String(e));
    } finally {
      setSaving(false);
    }
  };

  const conflictSet = useMemo(
    () => new Set(summary.conflicts),
    [summary.conflicts],
  );

  const confirmDelete = async () => {
    if (!toDelete) return;
    try {
      await deleteSkill(toDelete.dir);
      setToDelete(null);
      if (detail?.dir === toDelete.dir) setDetail(null);
      reload();
    } catch (e) {
      setError(String(e));
    }
  };

  const sourceLabel = (s: string) =>
    s === "user"
      ? "用户"
      : s === "plugin"
        ? "插件"
        : s === "disabled"
          ? "已禁用"
          : s;
  const isManaged = (s: string) => s === "user" || s === "disabled";

  return (
    <div className="skills-panel">
      {error && <div className="skills-error">{error}</div>}
      {summary.conflicts.length > 0 && (
        <div className="skills-conflict-banner">
          <TriangleAlert size={15} />
          <span>
            同名 skill：{summary.conflicts.join("、")}
            （多个根目录下重名，触发优先级取决于加载顺序）
          </span>
        </div>
      )}
      <div className="skills-toolbar">
        <input
          className="skills-search"
          placeholder="搜索名称或描述…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <select
          className="skills-source-filter"
          value={sourceFilter}
          onChange={(e) => setSourceFilter(e.target.value)}
        >
          <option value="all">全部来源</option>
          <option value="user">用户</option>
          <option value="plugin">插件</option>
          <option value="disabled">已禁用</option>
        </select>
        <Button size="sm" variant="ghost" onPress={reload} aria-label="刷新">
          <RefreshCw size={15} />
          刷新
        </Button>
      </div>

      <div className="skills-layout">
        <div className="skills-list">
          {filtered.length === 0 && (
            <div className="skills-empty">没有匹配的 skill</div>
          )}
          {filtered.map((skill) => (
            <button
              type="button"
              key={skill.dir}
              className={`skills-item${detail?.dir === skill.dir ? " active" : ""}${
                skill.enabled ? "" : " disabled"
              }`}
              onClick={() => openDetail(skill)}
            >
              <div className="skills-item-head">
                <span className="skills-item-heading">
                  <span className="skills-item-name">
                    <span className="skills-item-label" title={skill.name}>
                      {skill.name}
                    </span>
                    {conflictSet.has(skill.name) && (
                      <Tooltip>
                        <Tooltip.Trigger>
                          <TriangleAlert
                            size={13}
                            className="skills-conflict-icon"
                          />
                        </Tooltip.Trigger>
                        <Tooltip.Content>
                          与其他根目录下的同名 skill 冲突
                        </Tooltip.Content>
                      </Tooltip>
                    )}
                    {skill.missingFrontmatter && (
                      <Tooltip>
                        <Tooltip.Trigger>
                          <TriangleAlert
                            size={13}
                            className="skills-warn-icon"
                          />
                        </Tooltip.Trigger>
                        <Tooltip.Content>
                          SKILL.md 缺少 frontmatter
                        </Tooltip.Content>
                      </Tooltip>
                    )}
                  </span>
                  <Tooltip>
                    <Tooltip.Trigger>
                      <span className="skills-token-tag">
                        ~{formatTokenCount(skill.tokenEstimate)} tokens
                      </span>
                    </Tooltip.Trigger>
                    <Tooltip.Content>
                      每次会话约占用 {num.format(skill.tokenEstimate)}{" "}
                      Tokens（按 SKILL.md 的 frontmatter 估算）
                    </Tooltip.Content>
                  </Tooltip>
                  <span className="skills-token-tag">
                    {skill.files.length} 个文件
                  </span>
                </span>
                <span
                  className={
                    skill.enabled
                      ? "skills-source-tag"
                      : "skills-source-tag disabled"
                  }
                >
                  {sourceLabel(skill.source)}
                </span>
              </div>
              <p className="skills-item-desc one-line">{skill.description}</p>
            </button>
          ))}
        </div>

        <div className="skills-detail">
          {!detail ? (
            <div className="skills-empty">选择左侧的 skill 查看详情</div>
          ) : (
            <>
              <div className="skills-detail-head">
                <div>
                  <div className="skills-detail-title">
                    <h2>{detail.name}</h2>
                    <Tooltip>
                      <Tooltip.Trigger>
                        <span className="skills-token-tag">
                          ~{formatTokenCount(detail.tokenEstimate)} tokens
                        </span>
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        每次会话约占用 {num.format(detail.tokenEstimate)}{" "}
                        Tokens（按 SKILL.md 的 frontmatter 估算）
                      </Tooltip.Content>
                    </Tooltip>
                    <span className="skills-token-tag">
                      {detail.files.length} 个文件
                    </span>
                  </div>
                  <p className="skills-item-desc">{detail.description}</p>
                  <div className="skills-item-path-row">
                    <code className="skills-item-path" title={detail.dir}>
                      {detail.dir}
                    </code>
                    <Tooltip>
                      <Tooltip.Trigger>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="skills-path-copy"
                          onPress={copyPath}
                          aria-label="复制路径"
                        >
                          {copiedPath ? (
                            <Check size={13} />
                          ) : (
                            <Copy size={13} />
                          )}
                        </Button>
                      </Tooltip.Trigger>
                      <Tooltip.Content>
                        {copiedPath ? "已复制" : "复制路径"}
                      </Tooltip.Content>
                    </Tooltip>
                  </div>
                </div>
                <div className="skills-detail-actions">
                  {isManaged(detail.source) && (
                    <>
                      <Switch
                        size="sm"
                        isSelected={detail.enabled}
                        onChange={() => {
                          // 后端把目录移到 skills-disabled/ 或移回来，并返回
                          // 移动后的 skill（新 dir），用它更新详情，避免下次
                          // 操作拿旧路径报“目录不存在”。
                          toggleSkill(detail.dir, !detail.enabled)
                            .then((moved) => {
                              setDetail({ ...moved, body: detail.body });
                              setEditBody(detail.body);
                              reload();
                            })
                            .catch((e) => setError(String(e)));
                        }}
                      >
                        <Switch.Content>
                          <Switch.Control>
                            <Switch.Thumb />
                          </Switch.Control>
                        </Switch.Content>
                      </Switch>
                      <Button
                        size="sm"
                        variant="ghost"
                        isIconOnly
                        aria-label="删除"
                        onPress={() => setToDelete(detail)}
                      >
                        <Trash2 size={15} />
                      </Button>
                    </>
                  )}
                </div>
              </div>
              {detailError && <div className="skills-error">{detailError}</div>}
              {editing ? (
                <div className="skills-editor">
                  <textarea
                    value={editBody}
                    onChange={(e) => setEditBody(e.target.value)}
                    spellCheck={false}
                  />
                  <div className="skills-editor-actions">
                    <Button
                      size="sm"
                      className="skills-add"
                      isDisabled={saving}
                      onPress={persistBody}
                    >
                      <Save size={15} />
                      保存
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      isDisabled={saving}
                      onPress={() => {
                        setEditing(false);
                        setEditBody(detail.body);
                      }}
                    >
                      取消
                    </Button>
                  </div>
                </div>
              ) : (
                <div
                  className="skills-body markdown-body"
                  onDoubleClick={() => {
                    if (isManaged(detail.source)) setEditing(true);
                  }}
                  title={isManaged(detail.source) ? "双击进入编辑" : undefined}
                  dangerouslySetInnerHTML={{
                    __html: renderMarkdown(detail.body),
                  }}
                />
              )}
            </>
          )}
        </div>
      </div>

      <Modal
        isOpen={toDelete !== null}
        onOpenChange={(isOpen: boolean) => {
          if (!isOpen) setToDelete(null);
        }}
      >
        <Modal.Backdrop>
          <Modal.Container size="sm">
            <Modal.Dialog>
              <Modal.Header>
                <Modal.Heading>删除 Skill？</Modal.Heading>
              </Modal.Header>
              <Modal.Body>
                <p>
                  将删除 <code>{toDelete?.name}</code>（{toDelete?.dir}
                  ）及其全部文件，此操作不可恢复。
                </p>
              </Modal.Body>
              <Modal.Footer>
                <Button
                  size="sm"
                  variant="outline"
                  onPress={() => setToDelete(null)}
                >
                  取消
                </Button>
                <Button size="sm" variant="danger" onPress={confirmDelete}>
                  删除
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}

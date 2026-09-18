import DOMPurify from "dompurify";
import { marked } from "marked";

// 模型回复是 Markdown。marked 的 HTML 直接进 dangerouslySetInnerHTML，所以必须
// 先过 DOMPurify：模型可以把脚本写进代码块之外的任何地方，这段 HTML 又和网关
// 同源（wails:// 应用页），注入即等于拿到 App 绑定。
export function renderMarkdown(source: string): string {
  const html = marked.parse(source, { gfm: true, breaks: true, async: false });
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } });
}

// Help texts of the tray views: a small Markdown renderer for the texts in
// src/help/<view>.md. Pure, so it runs without Vite; helpTexts.ts loads the
// files.

export type ViewId = "projects" | "server-types" | "settings" | "logs";

function escape(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

/** inline renders code spans, bold text and links of one block of text. */
function inline(text: string): string {
  const parts = text.split(/(`[^`]*`)/);
  return parts
    .map((part) => {
      if (part.length > 1 && part.startsWith("`") && part.endsWith("`")) {
        return `<code>${escape(part.slice(1, -1))}</code>`;
      }
      return escape(part)
        .replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>")
        .replace(
          /\[([^\]]+)\]\(([^)\s]+)\)/g,
          (_m, label: string, href: string) => `<a href="${href}" target="_blank" rel="noreferrer">${label}</a>`,
        );
    })
    .join("");
}

/**
 * renderHelp turns the Markdown of a help text into HTML: headings,
 * paragraphs, unordered lists, fenced code blocks, inline code, bold text
 * and links. Text is escaped, so the result is safe to insert.
 */
export function renderHelp(markdown: string): string {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];
  let paragraph: string[] = [];
  let list: string[] = [];

  const flush = () => {
    if (paragraph.length > 0) out.push(`<p>${inline(paragraph.join("\n"))}</p>`);
    if (list.length > 0) out.push(`<ul>${list.map((item) => `<li>${inline(item)}</li>`).join("")}</ul>`);
    paragraph = [];
    list = [];
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith("```")) {
      flush();
      const code: string[] = [];
      for (i++; i < lines.length && !lines[i].startsWith("```"); i++) code.push(lines[i]);
      out.push(`<pre><code>${escape(code.join("\n"))}\n</code></pre>`);
      continue;
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    const item = /^[-*]\s+(.*)$/.exec(line);
    if (heading) {
      flush();
      const level = heading[1].length;
      out.push(`<h${level}>${inline(heading[2].trim())}</h${level}>`);
    } else if (item) {
      if (paragraph.length > 0) flush();
      list.push(item[1]);
    } else if (line.trim() === "") {
      flush();
    } else if (list.length > 0 && /^\s+/.test(line)) {
      list[list.length - 1] += " " + line.trim();
    } else {
      if (list.length > 0) flush();
      paragraph.push(line.trim());
    }
  }
  flush();
  return out.join("\n");
}

/** helpFor returns the rendered help text of a view, or undefined when it has none. */
export function helpFor(view: string, texts: Record<string, string>): string | undefined {
  const markdown = texts[view];
  return markdown === undefined ? undefined : renderHelp(markdown);
}

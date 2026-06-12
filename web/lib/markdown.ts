// Slice 672 — minimal, safe markdown -> HTML renderer for policy
// `body_md`.
//
// Why a hand-rolled renderer (D2 in the decisions log): the repo ships
// NO markdown library today (`grep -i markdown package.json` is empty;
// `body_md` is never rendered as markdown anywhere on `main` — it is
// only ever exported raw or PDF-rendered server-side). The policy
// detail page is the FIRST markdown render surface. Pulling in
// `react-markdown` + `remark-gfm` (+ their unified/micromark transitive
// tree) for a read-only render of trusted-but-defensively-treated
// operator-authored policy text is over-engineering for v1 (constitution
// Article VII Simplicity Gate). This renderer covers the subset the
// seeded policies use — headings, bold/italic, inline code, fenced code
// blocks, unordered + ordered lists, links, paragraphs, horizontal
// rules — and is a pure, vitest-coverable function.
//
// SECURITY (load-bearing): the input is HTML-escaped FIRST, before any
// markdown transform runs. The transforms only ever EMIT a fixed set of
// safe tags around already-escaped text — they never pass raw input
// through. A `body_md` containing `<script>` or `<img onerror=...>`
// renders as inert visible text, not as live markup. Links are rendered
// with `rel="noopener noreferrer"` and only `http(s):` / relative hrefs
// survive (a `javascript:` href is dropped to plain text). The caller
// injects the result via `dangerouslySetInnerHTML`, which is safe
// precisely because every byte of attacker-controllable input was
// escaped before this function built the markup.

/** Escape the five HTML-significant characters so input renders inert. */
export function escapeHtml(raw: string): string {
  return raw
    .replace(/\x00/g, "") // strip NUL so input cannot forge the code-span sentinel
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Allow only http(s) and root/relative hrefs. Anything else (e.g.
// `javascript:`, `data:`) is rejected so the link renders as plain text.
function safeHref(href: string): string | null {
  const trimmed = href.trim();
  if (/^https?:\/\//i.test(trimmed)) return trimmed;
  if (/^\/[^/]/.test(trimmed) || trimmed.startsWith("/")) return trimmed;
  if (/^#/.test(trimmed)) return trimmed;
  if (/^mailto:/i.test(trimmed)) return trimmed;
  return null;
}

// Inline transforms applied to an ALREADY-escaped line: links, bold,
// italic, inline code. Order matters — inline code is extracted first
// so its contents are not re-interpreted as bold/italic.
function renderInline(escaped: string): string {
  // Inline code `...` — placeholder out so emphasis inside code is literal.
  const codeSpans: string[] = [];
  let out = escaped.replace(/`([^`]+)`/g, (_m, code: string) => {
    const idx = codeSpans.push(`<code>${code}</code>`) - 1;
    return `\x00CODE${idx}\x00`;
  });

  // Links [text](href) — the escaped `href` is unescaped for the URL
  // check only (it was escaped as display text); rejected hrefs fall
  // back to the bracketed text rendered plainly.
  out = out.replace(
    /\[([^\]]+)\]\(([^)\s]+)\)/g,
    (_m, text: string, rawHref: string) => {
      // Unescape in the REVERSE order of escapeHtml — named entities
      // first, `&amp;` -> `&` LAST — so a literal like `&amp;lt;` is not
      // double-unescaped into `<` (CodeQL js/double-escaping).
      const unescaped = rawHref
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .replace(/&quot;/g, '"')
        .replace(/&#39;/g, "'")
        .replace(/&amp;/g, "&");
      const href = safeHref(unescaped);
      if (!href) return `[${text}](${rawHref})`;
      const safeAttr = escapeHtml(href);
      return `<a href="${safeAttr}" rel="noopener noreferrer" target="_blank">${text}</a>`;
    },
  );

  // Bold **...** then italic *...* / _..._
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  out = out.replace(/(^|[^_])_([^_]+)_/g, "$1<em>$2</em>");

  // Restore inline-code placeholders.
  out = out.replace(
    /\x00CODE(\d+)\x00/g,
    (_m, i: string) => codeSpans[Number(i)] ?? "",
  );
  return out;
}

/**
 * Render a markdown string to a safe HTML string. The input is escaped
 * before any transform; the output is a fixed grammar of safe tags.
 */
export function renderMarkdown(md: string): string {
  if (!md) return "";
  const lines = md.replace(/\r\n/g, "\n").split("\n");
  const html: string[] = [];

  let i = 0;
  let inUl = false;
  let inOl = false;
  let paragraph: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      const text = paragraph
        .map((l) => renderInline(escapeHtml(l)))
        .join("<br/>");
      html.push(`<p>${text}</p>`);
      paragraph = [];
    }
  };
  const closeLists = () => {
    if (inUl) {
      html.push("</ul>");
      inUl = false;
    }
    if (inOl) {
      html.push("</ol>");
      inOl = false;
    }
  };

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block ```
    const fence = line.match(/^```(.*)$/);
    if (fence) {
      flushParagraph();
      closeLists();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) {
        codeLines.push(escapeHtml(lines[i]));
        i++;
      }
      i++; // skip closing fence (or EOF)
      html.push(`<pre><code>${codeLines.join("\n")}</code></pre>`);
      continue;
    }

    // Blank line — paragraph / list break.
    if (/^\s*$/.test(line)) {
      flushParagraph();
      closeLists();
      i++;
      continue;
    }

    // Horizontal rule
    if (/^\s*(---|\*\*\*|___)\s*$/.test(line)) {
      flushParagraph();
      closeLists();
      html.push("<hr/>");
      i++;
      continue;
    }

    // Heading #..######
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flushParagraph();
      closeLists();
      const level = heading[1].length;
      const text = renderInline(escapeHtml(heading[2].trim()));
      html.push(`<h${level}>${text}</h${level}>`);
      i++;
      continue;
    }

    // Unordered list item
    const ul = line.match(/^\s*[-*+]\s+(.*)$/);
    if (ul) {
      flushParagraph();
      if (inOl) {
        html.push("</ol>");
        inOl = false;
      }
      if (!inUl) {
        html.push("<ul>");
        inUl = true;
      }
      html.push(`<li>${renderInline(escapeHtml(ul[1].trim()))}</li>`);
      i++;
      continue;
    }

    // Ordered list item
    const ol = line.match(/^\s*\d+\.\s+(.*)$/);
    if (ol) {
      flushParagraph();
      if (inUl) {
        html.push("</ul>");
        inUl = false;
      }
      if (!inOl) {
        html.push("<ol>");
        inOl = true;
      }
      html.push(`<li>${renderInline(escapeHtml(ol[1].trim()))}</li>`);
      i++;
      continue;
    }

    // Otherwise — accumulate into the current paragraph.
    closeLists();
    paragraph.push(line.trim());
    i++;
  }

  flushParagraph();
  closeLists();
  return html.join("\n");
}

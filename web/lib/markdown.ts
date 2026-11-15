// Safe markdown -> HTML renderer for every long-text field (policy
// `body_md`, risk descriptions, vendor and review notes, action-plan
// descriptions). Slice 672 introduced it for policy bodies; #2094 widened
// it to tables, block quotes, autolinks and reference links, narrowed the
// link allow-list, and wrapped it in the shared `<Markdown>` component
// (`web/components/markdown.tsx`).
//
// Why a hand-rolled renderer (slice 672 D2): the repo ships no markdown
// library, and `react-markdown` + `remark-gfm` pull a large
// unified/micromark tree for a read-only render. This covers the subset
// operators write — headings, emphasis, inline + fenced code, lists,
// tables, block quotes, links, rules — as a pure, vitest-coverable
// function.
//
// SECURITY (load-bearing):
//   * Input is HTML-escaped FIRST. Transforms only ever emit a fixed set
//     of attribute-free tags around already-escaped text, plus <a> whose
//     only attributes are an allow-listed, re-escaped href and fixed
//     rel/target. Raw HTML in the input renders as inert visible text.
//   * Links: only `http:`, `https:` and `mailto:` hrefs survive. Inline
//     links, reference-style links and `<autolinks>` share one check
//     (`safeHref`). Everything else — `javascript:` in any case,
//     `data:`, `vbscript:`, entity-encoded schemes, protocol-relative
//     `//host`, and relative or `#fragment` links — is dropped to plain
//     text. Relative links are dropped on purpose: the ticket's
//     allow-list is three schemes, and a relative href in operator text
//     has no stable meaning across the pages that render it.
//   * Images are never rendered. `![alt](src)` shows its alt text only.
//     An <img> in operator-authored text would fetch a third-party URL
//     from every viewer's browser (a tracking pixel), and nothing here
//     needs inline images.
//   * Every link opens with `rel="noopener noreferrer" target="_blank"`.
// The caller injects the result via `dangerouslySetInnerHTML`, which is
// safe because every byte of input was escaped before markup was built.

/** Escape the five HTML-significant characters so input renders inert. */
export function escapeHtml(raw: string): string {
  return raw
    .replace(/\x00/g, "") // strip NUL so input cannot forge a placeholder sentinel
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Reverse escapeHtml for a URL check. Named entities first, `&amp;` LAST,
// so `&amp;lt;` is not double-unescaped into `<` (CodeQL
// js/double-escaping).
function unescapeHtml(escaped: string): string {
  return escaped
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&");
}

/**
 * The link allow-list: `http://`, `https://` or `mailto:`, with no
 * whitespace or control characters anywhere. Returns the href to use, or
 * null when the link must render as plain text.
 */
export function safeHref(href: string): string | null {
  const trimmed = href.trim();
  // Whitespace or control characters inside a URL are how scheme
  // filters get smuggled past (`java\tscript:`); refuse them outright.
  if (/[\s\x00-\x1f\x7f]/.test(trimmed)) return null;
  if (/^https?:\/\/[^/]/i.test(trimmed)) return trimmed;
  if (/^mailto:[^/]/i.test(trimmed)) return trimmed;
  return null;
}

function anchor(href: string, innerHtml: string): string {
  return `<a href="${escapeHtml(
    href,
  )}" rel="noopener noreferrer" target="_blank">${innerHtml}</a>`;
}

function emphasis(s: string): string {
  let out = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  out = out.replace(/(^|[^_\w])_([^_]+)_(?!\w)/g, "$1<em>$2</em>");
  return out;
}

type RefMap = Map<string, string>;

// Inline transforms applied to an ALREADY-escaped string. Code spans and
// finished anchors are swapped for NUL-delimited placeholders (NUL is
// stripped from input by escapeHtml, so input cannot forge one) so later
// passes never rewrite inside them — e.g. an `_` in a URL never becomes
// <em>, and autolink text inside a link never nests a second <a>.
function renderInline(escaped: string, refs: RefMap): string {
  const held: string[] = [];
  const hold = (html: string) => `\x00${held.push(html) - 1}\x00`;

  // Every pattern below stops scanning at the next opening delimiter
  // (`[^[\]]`, `[^()\s]`, `[^\s&]`), so a hostile line of repeated `[`,
  // `](` or `<` is matched in linear time rather than rescanned to the
  // end of the line from each opener. The price: hrefs cannot contain
  // parentheses and link text cannot contain brackets.
  let out = escaped.replace(/`([^`]+)`/g, (_m, code: string) =>
    hold(`<code>${code}</code>`),
  );

  // Images: alt text only, never an <img> (see header).
  out = out.replace(/!\[([^[\]]*)\]\([^()]*\)/g, "$1");
  out = out.replace(/!\[([^[\]]*)\]\[[^[\]]*\]/g, "$1");

  // Inline links [text](href). A rejected href leaves the source text.
  out = out.replace(
    /\[([^[\]]+)\]\(([^()\s]+)\)/g,
    (m, text: string, rawHref: string) => {
      const href = safeHref(unescapeHtml(rawHref));
      return href ? hold(anchor(href, emphasis(text))) : m;
    },
  );

  // Reference links [text][label] and [text][] (label = text).
  out = out.replace(
    /\[([^[\]]+)\]\[([^[\]]*)\]/g,
    (m, text: string, label: string) => {
      const target = refs.get((label || text).trim().toLowerCase());
      const href = target === undefined ? null : safeHref(target);
      return href ? hold(anchor(href, emphasis(text))) : m;
    },
  );

  // Autolinks <https://...> / <mailto:...>, escaped to &lt;...&gt; by now.
  // The body allows `&amp;` but stops at any other entity, including the
  // next `&lt;`.
  out = out.replace(/&lt;((?:[^\s&]|&amp;)+)&gt;/g, (m, raw: string) => {
    const href = safeHref(unescapeHtml(raw));
    return href ? hold(anchor(href, raw)) : m;
  });

  out = emphasis(out);

  // Restore placeholders. Held anchors may contain held code spans, so
  // repeat until none are left (bounded by the number held).
  for (let n = 0; n <= held.length && out.includes("\x00"); n++) {
    out = out.replace(
      /\x00(\d+)\x00/g,
      (_m, i: string) => held[Number(i)] ?? "",
    );
  }
  return out;
}

// GFM table helpers -------------------------------------------------------

function splitRow(line: string): string[] {
  let row = line.trim();
  if (row.startsWith("|")) row = row.slice(1);
  if (row.endsWith("|") && !row.endsWith("\\|")) row = row.slice(0, -1);
  // Split on pipes not escaped as `\|`.
  return row.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, "|"));
}

// Checked cell by cell rather than with one whole-line pattern, whose
// adjacent optional whitespace runs backtrack on a long line.
function isTableSeparator(line: string): boolean {
  return splitRow(line).every((cell) => /^:?-+:?$/.test(cell));
}

function isTableStart(lines: string[], i: number): boolean {
  return (
    i + 1 < lines.length &&
    lines[i].includes("|") &&
    lines[i + 1].includes("|") &&
    isTableSeparator(lines[i + 1])
  );
}

// Reference definitions `[label]: url "title"` are collected once over the
// whole document, outside fenced code, and removed from the rendered
// output. Inside a fence they are code and stay as written.
const REF_DEF = /^\s{0,3}\[([^[\]]+)\]:\s*<?([^\s>]+)>?(?:\s+.*)?$/;
const FENCE = /^\s*```/;

// Longer input renders as escaped preformatted text with no markdown
// transforms: a cap on the work any one stored field can cost a viewer.
export const MAX_MARKDOWN_CHARS = 100_000;

// Block quotes nest by recursion; past this depth the remaining `>`
// markers render as plain text, so a hostile `>>>>…` cannot blow the stack.
const MAX_QUOTE_DEPTH = 8;

/**
 * Render a markdown string to a safe HTML string. The input is escaped
 * before any transform; the output is a fixed grammar of safe tags.
 */
export function renderMarkdown(md: string): string {
  if (!md) return "";
  if (md.length > MAX_MARKDOWN_CHARS) return `<pre>${escapeHtml(md)}</pre>`;
  const refs: RefMap = new Map();
  let inFence = false;
  // Split on every character `.` refuses to match (\n, \r, U+2028,
  // U+2029), so no line holds a character that `\s` matches but `.` does
  // not. Otherwise the `\s+ … .* … $` block patterns backtrack
  // quadratically on a line like "- " + spaces + "x\u2028x".
  const lines = md.split(/\r\n?|[\n\u2028\u2029]/).filter((line) => {
    if (FENCE.test(line)) inFence = !inFence;
    if (inFence) return true;
    const def = line.match(REF_DEF);
    if (!def) return true;
    const label = def[1].trim().toLowerCase();
    if (!refs.has(label)) refs.set(label, def[2]);
    return false;
  });
  return renderBlocks(lines, refs, 0);
}

function renderBlocks(lines: string[], refs: RefMap, depth: number): string {
  const html: string[] = [];
  const inline = (s: string) => renderInline(escapeHtml(s), refs);

  let i = 0;
  let inUl = false;
  let inOl = false;
  let paragraph: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      html.push(`<p>${paragraph.map(inline).join("<br/>")}</p>`);
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
  const closeBlock = () => {
    flushParagraph();
    closeLists();
  };

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block ```
    if (/^\s*```/.test(line)) {
      closeBlock();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) {
        codeLines.push(escapeHtml(lines[i]));
        i++;
      }
      i++; // skip closing fence (or EOF)
      html.push(`<pre><code>${codeLines.join("\n")}</code></pre>`);
      continue;
    }

    // Blank line — paragraph / list break.
    if (/^\s*$/.test(line)) {
      closeBlock();
      i++;
      continue;
    }

    // Block quote: gather consecutive `>` lines and render them as blocks.
    if (/^\s*>/.test(line) && depth < MAX_QUOTE_DEPTH) {
      closeBlock();
      const inner: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        inner.push(lines[i].replace(/^\s*>\s?/, ""));
        i++;
      }
      html.push(
        `<blockquote>${renderBlocks(inner, refs, depth + 1)}</blockquote>`,
      );
      continue;
    }

    // GFM table: header row, separator row, then body rows with pipes.
    if (isTableStart(lines, i)) {
      closeBlock();
      const header = splitRow(lines[i]);
      i += 2;
      const rows: string[][] = [];
      while (
        i < lines.length &&
        lines[i].includes("|") &&
        !/^\s*$/.test(lines[i])
      ) {
        rows.push(splitRow(lines[i]));
        i++;
      }
      const th = header.map((c) => `<th>${inline(c)}</th>`).join("");
      const body = rows
        .map((r) => {
          const cells = header.map((_h, k) => `<td>${inline(r[k] ?? "")}</td>`);
          return `<tr>${cells.join("")}</tr>`;
        })
        .join("");
      html.push(
        `<table><thead><tr>${th}</tr></thead>${
          body ? `<tbody>${body}</tbody>` : ""
        }</table>`,
      );
      continue;
    }

    // Horizontal rule
    if (/^\s*(---|\*\*\*|___)\s*$/.test(line)) {
      closeBlock();
      html.push("<hr/>");
      i++;
      continue;
    }

    // Heading #..######
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      closeBlock();
      const level = heading[1].length;
      html.push(`<h${level}>${inline(heading[2].trim())}</h${level}>`);
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
      html.push(`<li>${inline(ul[1].trim())}</li>`);
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
      html.push(`<li>${inline(ol[1].trim())}</li>`);
      i++;
      continue;
    }

    // Otherwise — accumulate into the current paragraph.
    closeLists();
    paragraph.push(line.trim());
    i++;
  }

  closeBlock();
  return html.join("\n");
}

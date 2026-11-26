// Slice 672 — vitest coverage for the minimal markdown renderer
// (`web/lib/markdown.ts`).
//
// The load-bearing property is SECURITY: the input is HTML-escaped
// before any transform, so a `body_md` carrying `<script>` or an
// `onerror` handler renders inert. The tests pin that property plus the
// markdown subset the seeded policies use.

import { describe, expect, test } from "vitest";

import { MAX_MARKDOWN_CHARS, escapeHtml, renderMarkdown } from "./markdown";

describe("escapeHtml", () => {
  test("escapes the five HTML-significant characters", () => {
    expect(escapeHtml(`<a href="x" id='y'>&`)).toBe(
      "&lt;a href=&quot;x&quot; id=&#39;y&#39;&gt;&amp;",
    );
  });
});

describe("renderMarkdown — security (load-bearing)", () => {
  test("inert-renders a raw <script> tag", () => {
    const html = renderMarkdown("<script>alert(1)</script>");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  test("inert-renders an img onerror payload", () => {
    const html = renderMarkdown('![x](y) <img src=z onerror="alert(1)">');
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  test("drops a javascript: link href to plain text", () => {
    const html = renderMarkdown("[click](javascript:alert(1))");
    expect(html).not.toContain("<a ");
    // The bracketed text survives as visible (escaped) text.
    expect(html).toContain("click");
  });

  test("keeps an http link with safe rel + target", () => {
    const html = renderMarkdown("[docs](https://example.com/a)");
    expect(html).toContain('href="https://example.com/a"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  // #2094 narrowed the allow-list to http, https and mailto: a relative
  // link now renders as its source text (see the header in markdown.ts).
  test("drops a relative link to plain text", () => {
    const html = renderMarkdown("[home](/dashboard)");
    expect(html).not.toContain("<a ");
    expect(html).toContain("[home](/dashboard)");
  });
});

describe("renderMarkdown — block grammar", () => {
  test("headings h1..h3", () => {
    const html = renderMarkdown("# A\n## B\n### C");
    expect(html).toContain("<h1>A</h1>");
    expect(html).toContain("<h2>B</h2>");
    expect(html).toContain("<h3>C</h3>");
  });

  test("unordered list", () => {
    const html = renderMarkdown("- one\n- two");
    expect(html).toContain("<ul>");
    expect(html).toContain("<li>one</li>");
    expect(html).toContain("<li>two</li>");
    expect(html).toContain("</ul>");
  });

  test("ordered list", () => {
    const html = renderMarkdown("1. first\n2. second");
    expect(html).toContain("<ol>");
    expect(html).toContain("<li>first</li>");
    expect(html).toContain("</ol>");
  });

  test("fenced code block escapes its contents", () => {
    const html = renderMarkdown("```\n<b>not bold</b>\n```");
    expect(html).toContain("<pre><code>");
    expect(html).toContain("&lt;b&gt;not bold&lt;/b&gt;");
  });

  test("horizontal rule", () => {
    const html = renderMarkdown("a\n\n---\n\nb");
    expect(html).toContain("<hr/>");
  });

  test("paragraph with bold + italic + inline code", () => {
    const html = renderMarkdown("This is **bold**, _italic_, and `code`.");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<em>italic</em>");
    expect(html).toContain("<code>code</code>");
    expect(html).toContain("<p>");
  });

  test("empty / falsy input returns empty string", () => {
    expect(renderMarkdown("")).toBe("");
  });

  test("multi-line paragraph joins a soft break with a space (#2455)", () => {
    const html = renderMarkdown("line one\nline two");
    expect(html).toBe("<p>line one line two</p>");
  });

  test("inline code is not re-interpreted as emphasis", () => {
    const html = renderMarkdown("use `a_b_c` here");
    expect(html).toContain("<code>a_b_c</code>");
    // The underscores inside code must NOT become <em>.
    expect(html).not.toContain("<em>");
  });
});

// #2094 — the shared <Markdown> component's renderer. Each case on the
// security list for the ticket is pinned below.
describe("renderMarkdown — raw HTML is escaped (#2094)", () => {
  test.each([
    ["script", "<script>alert(1)</script>", "<script", "&lt;script&gt;"],
    ["img onerror", '<img src=x onerror="alert(1)">', "<img", "&lt;img"],
    [
      "iframe",
      '<iframe src="https://evil.example"></iframe>',
      "<iframe",
      "&lt;iframe",
    ],
    [
      "html inside a table cell",
      "| a |\n| - |\n| <b>x</b> |",
      "<b>",
      "&lt;b&gt;",
    ],
    [
      "html inside a block quote",
      "> <script>x</script>",
      "<script",
      "&lt;script&gt;",
    ],
  ])("%s", (_name, md, banned, expected) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain(banned);
    expect(html).toContain(expected);
  });

  test("html inside a fenced code block stays escaped", () => {
    const html = renderMarkdown("```\n<script>alert(1)</script>\n```");
    expect(html).toContain(
      "<pre><code>&lt;script&gt;alert(1)&lt;/script&gt;</code></pre>",
    );
  });

  test("html inside an inline code span stays escaped", () => {
    expect(renderMarkdown("`<iframe>`")).toContain(
      "<code>&lt;iframe&gt;</code>",
    );
  });

  test("an input NUL cannot forge a placeholder", () => {
    const html = renderMarkdown("a \x000\x00 [x](https://e.example) b");
    expect(html.match(/<a /g)).toHaveLength(1);
  });
});

describe("renderMarkdown — link allow-list (#2094)", () => {
  test.each([
    ["javascript:", "[x](javascript:alert(1))"],
    ["mixed-case JaVaScRiPt:", "[x](JaVaScRiPt:alert(1))"],
    ["data:", "[x](data:text/html;base64,PHNjcmlwdD4=)"],
    ["vbscript:", "[x](vbscript:msgbox(1))"],
    ["entity-encoded javascript&#58;", "[x](javascript&#58;alert(1))"],
    ["entity-encoded colon &colon;", "[x](javascript&colon;alert(1))"],
    ["protocol-relative //evil", "[x](//evil.example/a)"],
    ["relative /path (dropped by design)", "[x](/dashboard)"],
    ["fragment #id (dropped by design)", "[x](#top)"],
    ["scheme-less host", "[x](evil.example)"],
    ["javascript: autolink", "<javascript:alert(1)>"],
    ["data: autolink", "<data:text/html,x>"],
    ["javascript: reference link", "[x][r]\n\n[r]: javascript:alert(1)"],
    ["protocol-relative reference link", "[x][r]\n\n[r]: //evil.example"],
    ["https: with no host", "[x](https:/evil)"],
  ])("drops %s", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain("<a ");
    expect(html).not.toMatch(/href=/i);
  });

  test("keeps a mailto: link", () => {
    const html = renderMarkdown("[mail us](mailto:security@example.com)");
    expect(html).toContain(
      '<a href="mailto:security@example.com" rel="noopener noreferrer" target="_blank">mail us</a>',
    );
  });

  test.each([
    ["inline http", "[x](http://example.com)", "http://example.com"],
    [
      "inline https",
      "[x](https://example.com/a_b_c?q=1&r=2)",
      "https://example.com/a_b_c?q=1&amp;r=2",
    ],
    ["autolink", "<https://example.com/x>", "https://example.com/x"],
    ["mailto autolink", "<mailto:a@example.com>", "mailto:a@example.com"],
    [
      "reference link",
      "[x][docs]\n\n[docs]: https://example.com/d",
      "https://example.com/d",
    ],
    [
      "collapsed reference link",
      "[Docs][]\n\n[docs]: <https://example.com/c>",
      "https://example.com/c",
    ],
  ])("keeps %s with rel + target", (_name, md, href) => {
    const html = renderMarkdown(md);
    expect(html).toContain(`href="${href}"`);
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  test("a reference definition is not rendered as text", () => {
    expect(
      renderMarkdown("[x][d]\n\n[d]: https://example.com/def"),
    ).not.toContain("[d]:");
  });

  test("an underscore in a URL is not turned into emphasis", () => {
    const html = renderMarkdown("[x](https://example.com/a_b_c)");
    expect(html).not.toContain("<em>");
  });

  test("every anchor carries rel and target", () => {
    const html = renderMarkdown(
      "[a](https://a.example) <https://b.example> [c][c]\n\n[c]: mailto:c@example.com",
    );
    const anchors = html.match(/<a [^>]*>/g) ?? [];
    expect(anchors).toHaveLength(3);
    for (const a of anchors) {
      expect(a).toContain('rel="noopener noreferrer"');
      expect(a).toContain('target="_blank"');
    }
  });

  test("a quote in an allowed href cannot break out of the attribute", () => {
    const html = renderMarkdown('[x](https://e.example/"onmouseover="alert.x)');
    // The link forms, and the quote stays an entity inside href, so no
    // attribute is opened.
    expect(html).toContain(
      'href="https://e.example/&quot;onmouseover=&quot;alert.x"',
    );
    expect(html).not.toContain('"onmouseover="');
  });
});

describe("renderMarkdown — images are never rendered (#2094)", () => {
  test.each([
    ["https image", "![logo](https://example.com/p.png)"],
    ["javascript image", "![x](javascript:alert(1))"],
    ["reference image", "![pixel][p]\n\n[p]: https://tracker.example/p.gif"],
  ])("%s renders no <img> and no link", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<a ");
  });

  test("alt text is kept", () => {
    expect(renderMarkdown("![diagram](https://example.com/d.png)")).toBe(
      "<p>diagram</p>",
    );
  });
});

describe("renderMarkdown — tables and block quotes (#2094)", () => {
  test("a GFM table renders as a table", () => {
    const html = renderMarkdown(
      "| Control | Owner |\n| :--- | ---: |\n| MFA | **IT** |\n| Backups | Ops |",
    );
    expect(html).toContain(
      "<table><thead><tr><th>Control</th><th>Owner</th></tr></thead>",
    );
    expect(html).toContain("<tr><td>MFA</td><td><strong>IT</strong></td></tr>");
    expect(html).toContain("<tr><td>Backups</td><td>Ops</td></tr>");
    expect(html).not.toContain("|");
  });

  test("an escaped pipe stays inside its cell; short rows are padded", () => {
    const html = renderMarkdown("a | b\n--|--\nx \\| y\n");
    expect(html).toContain("<td>x | y</td><td></td>");
  });

  test("a block quote renders as a blockquote with inner markdown", () => {
    const html = renderMarkdown("> Note: **read** this\n> - one\n> - two");
    expect(html).toContain("<blockquote>");
    expect(html).toContain("<strong>read</strong>");
    expect(html).toContain("<li>one</li>");
    expect(html).not.toContain("&gt;");
  });

  test("nested block quotes nest, and depth is bounded", () => {
    expect(renderMarkdown(">> deep")).toContain("<blockquote><blockquote>");
    const html = renderMarkdown(">".repeat(5000) + " x");
    expect(html.match(/<blockquote>/g)).toHaveLength(8);
  });
});

describe("renderMarkdown — more link evasions (#2094 review)", () => {
  test.each([
    ["literal tab inside javascript:", "[x](java\tscript:alert(1))"],
    [
      "literal tab in a reference target",
      "[x][r]\n\n[r]: java\tscript:alert(1)",
    ],
    ["leading whitespace before javascript:", "[x]( javascript:alert(1))"],
    [
      "leading whitespace in a reference target",
      "[x][r]\n\n[r]:    javascript:alert(1)",
    ],
    ["percent-encoded %6Aavascript:", "[x](%6Aavascript:alert(1))"],
    ["percent-encoded autolink", "<%6Aavascript:alert(1)>"],
    [
      "percent-encoded reference target",
      "[x][r]\n\n[r]: %6Aavascript:alert(1)",
    ],
  ])("drops %s", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html).not.toContain("<a ");
    expect(html).not.toMatch(/href=/i);
  });
});

describe("renderMarkdown — reference definitions inside code (#2094 review)", () => {
  test("a definition-shaped line inside a fence stays in the code block", () => {
    const html = renderMarkdown("```\n[x]: https://example.com/a\n```");
    expect(html).toBe("<pre><code>[x]: https://example.com/a</code></pre>");
  });

  test("a definition inside a fence does not resolve a reference link", () => {
    const html = renderMarkdown(
      "[go][x]\n\n```\n[x]: https://example.com/a\n```",
    );
    expect(html).not.toContain("<a ");
  });

  test("a definition after a closed fence still applies", () => {
    const html = renderMarkdown(
      "```\ncode\n```\n\n[go][x]\n\n[x]: https://example.com/a",
    );
    expect(html).toContain('href="https://example.com/a"');
  });
});

describe("renderMarkdown — reference labels (#2316)", () => {
  test.each([
    ["an ampersand", "[Q&A][]\n\n[Q&A]: https://example.com/qa"],
    ["a less-than sign", "[a<b][]\n\n[a<b]: https://example.com/lt"],
    ["an apostrophe", "[Matt's][]\n\n[matt's]: https://example.com/ap"],
    ["a double quote", '[x][say "hi"]\n\n[say "hi"]: https://example.com/q'],
    ["mixed case", "[x][Docs]\n\n[DOCS]: https://example.com/case"],
    [
      "different inner whitespace",
      "[x][Access   Review]\n\n[access review]: https://example.com/ws",
    ],
    [
      "ß against SS (case fold)",
      "[x][STRASSE]\n\n[straße]: https://example.com/ss",
    ],
  ])("a label with %s resolves", (_name, md) => {
    const html = renderMarkdown(md);
    expect(html.match(/<a /g) ?? []).toHaveLength(1);
    expect(html).toContain('href="https://example.com/');
  });

  test("a label holding <script> resolves and injects nothing", () => {
    const html = renderMarkdown(
      "[<script>alert(1)</script>][]\n\n[<script>alert(1)</script>]: https://example.com/s",
    );
    expect(html).toBe(
      '<p><a href="https://example.com/s" rel="noopener noreferrer" target="_blank">&lt;script&gt;alert(1)&lt;/script&gt;</a></p>',
    );
  });

  test("an unsafe target behind an escaped label still does not link", () => {
    const html = renderMarkdown("[a&b][]\n\n[a&b]: javascript:alert(1)");
    expect(html).not.toMatch(/href=/i);
  });
});

describe("renderMarkdown — definitions inside block quotes (#2316)", () => {
  test("a definition in a quote resolves a link outside it", () => {
    const html = renderMarkdown(
      "[go][r]\n\n> Quoted text.\n> [r]: https://example.com/q",
    );
    expect(html).toContain('href="https://example.com/q"');
    expect(html).not.toContain("[r]:");
    expect(html).toContain("<blockquote><p>Quoted text.</p></blockquote>");
  });

  test("a definition in a nested quote resolves a link inside the quote", () => {
    const html = renderMarkdown("> [go][r]\n>\n> > [r]: https://example.com/n");
    expect(html).toContain('href="https://example.com/n"');
    expect(html).not.toContain("[r]:");
  });

  test("a quote holding only a definition renders empty", () => {
    expect(renderMarkdown("> [r]: https://example.com/e")).toBe(
      "<blockquote></blockquote>",
    );
  });

  test("a definition inside a quoted fence stays code", () => {
    const html = renderMarkdown(
      "[go][r]\n\n> ```\n> [r]: https://example.com/f\n> ```",
    );
    expect(html).not.toContain("<a ");
    expect(html).toContain(
      "<pre><code>[r]: https://example.com/f</code></pre>",
    );
  });

  test("a quoted fence left open ends with the quote", () => {
    const html = renderMarkdown(
      "> ```\n> code\n\n[go][r]\n\n[r]: https://example.com/after",
    );
    expect(html).toContain('href="https://example.com/after"');
  });

  test("a quote-shaped line inside a top-level fence stays code", () => {
    const html = renderMarkdown(
      "[go][r]\n\n```\n> [r]: https://example.com/c\n```",
    );
    expect(html).not.toContain("<a ");
    expect(html).toContain("&gt; [r]: https://example.com/c");
  });

  test("a javascript: definition in a quote does not link", () => {
    const html = renderMarkdown("[x][r]\n\n> [r]: javascript:alert(1)");
    expect(html).not.toMatch(/href=/i);
  });
});

describe("renderMarkdown — soft and hard line breaks (#2455)", () => {
  test("a soft break inside a paragraph is a space", () => {
    expect(renderMarkdown("one\ntwo\nthree")).toBe("<p>one two three</p>");
  });

  test("CRLF soft breaks are spaces too", () => {
    expect(renderMarkdown("one\r\ntwo")).toBe("<p>one two</p>");
  });

  test("two trailing spaces make a hard break", () => {
    expect(renderMarkdown("one  \ntwo")).toBe("<p>one<br/>two</p>");
  });

  test("more than two trailing spaces still make one hard break", () => {
    expect(renderMarkdown("one     \ntwo")).toBe("<p>one<br/>two</p>");
  });

  test("one trailing space is a soft break", () => {
    expect(renderMarkdown("one \ntwo")).toBe("<p>one two</p>");
  });

  test("a trailing backslash makes a hard break and is consumed", () => {
    expect(renderMarkdown("one\\\ntwo")).toBe("<p>one<br/>two</p>");
  });

  test("a hard break at the end of a paragraph is dropped", () => {
    expect(renderMarkdown("one  ")).toBe("<p>one</p>");
    expect(renderMarkdown("one  \n\ntwo")).toBe("<p>one</p>\n<p>two</p>");
    // CommonMark: a trailing backslash on the last line is literal text.
    expect(renderMarkdown("one\\")).toBe("<p>one\\</p>");
  });

  test("leading indentation of a continuation line is dropped", () => {
    expect(renderMarkdown("one\n   two")).toBe("<p>one two</p>");
  });

  test("breaks inside a list item's lazy continuation", () => {
    expect(renderMarkdown("- one\ntwo\n- three  \nfour")).toBe(
      "<ul>\n<li>one two</li>\n<li>three<br/>four</li>\n</ul>",
    );
    expect(renderMarkdown("1. one\\\ntwo")).toBe(
      "<ol>\n<li>one<br/>two</li>\n</ol>",
    );
  });

  test("a hard break at the end of a list item is dropped", () => {
    expect(renderMarkdown("- one  \n- two")).toBe(
      "<ul>\n<li>one</li>\n<li>two</li>\n</ul>",
    );
  });

  test("breaks inside a block quote", () => {
    expect(renderMarkdown("> one\n> two")).toBe(
      "<blockquote><p>one two</p></blockquote>",
    );
    expect(renderMarkdown("> one  \n> two")).toBe(
      "<blockquote><p>one<br/>two</p></blockquote>",
    );
    expect(renderMarkdown("> - a\\\n> b")).toBe(
      "<blockquote><ul>\n<li>a<br/>b</li>\n</ul></blockquote>",
    );
  });

  test("fenced code keeps its newlines and its trailing spaces", () => {
    expect(renderMarkdown("```\na  \nb\\\nc\n```")).toBe(
      "<pre><code>a  \nb\\\nc</code></pre>",
    );
  });

  test("inline code on a line before a soft break is untouched", () => {
    expect(renderMarkdown("`a  b`\nc")).toBe("<p><code>a  b</code> c</p>");
  });

  test("a <br> in the input is escaped, not a break", () => {
    expect(renderMarkdown("one<br/>two\nthree")).toBe(
      "<p>one&lt;br/&gt;two three</p>",
    );
  });

  test("raw HTML on a hard-break line stays escaped", () => {
    const html = renderMarkdown(
      "<script>alert(1)</script>  \n<img src=x onerror=y>\\\nz",
    );
    expect(html).toBe(
      "<p>&lt;script&gt;alert(1)&lt;/script&gt;<br/>&lt;img src=x onerror=y&gt;<br/>z</p>",
    );
  });
});

describe("renderMarkdown — random input emits only the safe grammar (#2316)", () => {
  // A seeded generator over markdown- and HTML-significant fragments. In
  // every output, each tag is one the renderer may emit and every href is
  // one the allow-list accepts.
  const FRAGMENTS = [
    "[",
    "]",
    "(",
    ")",
    "<",
    ">",
    "&",
    "'",
    '"',
    "`",
    "*",
    "_",
    "!",
    "|",
    "\n",
    "> ",
    "- ",
    "1. ",
    "# ",
    "```",
    ":",
    " ",
    "x",
    "&amp;",
    "&lt;",
    "[r]: ",
    "javascript:",
    "https://e.example/",
    "mailto:a@e.example",
    "<script>",
    "onerror=",
    "\x00",
    "data:",
    "//",
    "\t",
    "\\", // a hard break before a newline (#2455)
    "  ", // two trailing spaces: the other hard-break form (#2455)
  ];
  const TAG =
    /^<\/?(p|br\/|strong|em|code|pre|ul|ol|li|h[1-6]|hr\/|blockquote|table|thead|tbody|tr|th|td|a)( href="[^"]*" rel="noopener noreferrer" target="_blank")?>$/;

  let seed = 2316;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };

  test("2000 random documents", () => {
    for (let n = 0; n < 2000; n++) {
      let md = "";
      const len = 1 + Math.floor(rand() * 40);
      for (let k = 0; k < len; k++) {
        md += FRAGMENTS[Math.floor(rand() * FRAGMENTS.length)];
      }
      const html = renderMarkdown(md);
      const tags = html.match(/<[^>]*>/g) ?? [];
      // No `<` survives outside an emitted tag: every one starts a tag.
      expect((html.match(/</g) ?? []).length, md).toBe(tags.length);
      for (const tag of tags) {
        expect(tag, md).toMatch(TAG);
        const href = tag.match(/href="([^"]*)"/)?.[1];
        if (href !== undefined) {
          expect(href, md).toMatch(/^(https?:\/\/[^/]|mailto:[^/])/i);
        }
      }
    }
  });
});

describe("renderMarkdown — bounded work (#2094 review)", () => {
  // Each line is just under the size cap and shaped to make a naive
  // pattern rescan to the end of the line from every opener.
  const n = 24_000;
  test.each([
    ["repeated [", "[".repeat(n * 4)],
    ["repeated ![", "![".repeat(n * 2)],
    ["repeated [a](", "[a](".repeat(n)],
    ["repeated ](", "](".repeat(n * 2)],
    ["repeated [a][", "[a][".repeat(n)],
    ["repeated <", "<".repeat(n * 4)],
    ["repeated <a", "<a".repeat(n * 2)],
    ["repeated `", "`".repeat(n * 4)],
    ["repeated **a", "**a".repeat(n)],
    ["repeated _a", " _a".repeat(n)],
    [
      "table separator with a long whitespace run",
      "a|b\n|-" + " ".repeat(n * 4) + "x",
    ],
    ["repeated [x]: in definitions", "[x]:".repeat(n)],
    // `\s` matches U+2028/U+2029 and `.` does not; a `\s+ … .*$` block
    // pattern backtracks on these unless the line split breaks on them.
    ["repeated > ", "> ".repeat(n * 2)],
    ["repeated > [x]: in quoted definitions", "> [x]:".repeat(n * 0.6)],
    ...["- ", "# ", "1. ", "[x]: a ", "> [x]: a "].flatMap((prefix) =>
      [" ", "\u2028", "\u2029"].map((sep): [string, string] => [
        `${JSON.stringify(prefix)} + spaces + x${JSON.stringify(sep)}x`,
        prefix + " ".repeat(99_000) + "x" + sep + "x",
      ]),
    ),
  ])("%s renders in well under a second", (_name, md) => {
    expect(md.length).toBeLessThanOrEqual(MAX_MARKDOWN_CHARS);
    const start = performance.now();
    renderMarkdown(md);
    expect(performance.now() - start).toBeLessThan(1000);
  });

  test("input over the cap renders as escaped preformatted text", () => {
    const md = "**bold** <script>x</script> " + "a".repeat(MAX_MARKDOWN_CHARS);
    const html = renderMarkdown(md);
    expect(html.startsWith("<pre>**bold** &lt;script&gt;")).toBe(true);
    expect(html).not.toContain("<strong>");
    expect(html).not.toContain("<script");
  });

  test("U+2028 and U+2029 break lines like \\n", () => {
    expect(renderMarkdown("- a\u2028- b\u2029- c")).toBe(
      "<ul>\n<li>a</li>\n<li>b</li>\n<li>c</li>\n</ul>",
    );
  });
});

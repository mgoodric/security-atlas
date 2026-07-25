// Slice 672 — vitest coverage for the minimal markdown renderer
// (`web/lib/markdown.ts`).
//
// The load-bearing property is SECURITY: the input is HTML-escaped
// before any transform, so a `body_md` carrying `<script>` or an
// `onerror` handler renders inert. The tests pin that property plus the
// markdown subset the seeded policies use.

import { describe, expect, test } from "vitest";

import { escapeHtml, renderMarkdown } from "./markdown";

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

  test("keeps a relative link", () => {
    const html = renderMarkdown("[home](/dashboard)");
    expect(html).toContain('href="/dashboard"');
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

  test("multi-line paragraph joins with a soft break", () => {
    const html = renderMarkdown("line one\nline two");
    expect(html).toContain("line one<br/>line two");
  });

  test("inline code is not re-interpreted as emphasis", () => {
    const html = renderMarkdown("use `a_b_c` here");
    expect(html).toContain("<code>a_b_c</code>");
    // The underscores inside code must NOT become <em>.
    expect(html).not.toContain("<em>");
  });
});

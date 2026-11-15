// #2102 — the shared detail-page header.
//
// The vitest tier is node-only with no DOM (slice 069 P0-A3), so this
// renders the header to static markup with `createElement` (no JSX).
// The overflow menu and its inline confirm are covered in
// `components/ui/overflow-menu.test.ts`; the vendor page's Playwright
// specs cover the header in the real app.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { OverflowItem } from "@/components/ui/overflow-menu-model";

import { DetailHeader } from "./detail-header";

function items(): OverflowItem[] {
  return [
    { id: "archive", label: "Archive", onSelect: () => {} },
    {
      id: "delete",
      label: "Delete vendor",
      destructive: true,
      onSelect: () => {},
    },
  ];
}

/** The opening tag of the element carrying `testid`. */
function tag(html: string, testid: string): string {
  const at = html.indexOf(`data-testid="${testid}"`);
  expect(at, `no element with data-testid="${testid}"`).toBeGreaterThan(-1);
  const open = html.lastIndexOf("<", at);
  return html.slice(open, html.indexOf(">", at) + 1);
}

function render(overflow: OverflowItem[] = []) {
  return renderToStaticMarkup(
    h(DetailHeader, {
      "data-testid": "hdr",
      titleTestId: "hdr-title",
      title: "Acme Corp",
      status: h("span", { "data-testid": "hdr-status" }, "overdue"),
      edit: { href: "/vendors/v1/edit", "data-testid": "hdr-edit" },
      primaryAction: {
        label: "Record review",
        href: "/vendors/v1/reviews/new",
        "data-testid": "hdr-primary",
      },
      overflow,
    }),
  );
}

describe("DetailHeader", () => {
  it("renders the title, status, Edit and the primary action", () => {
    const html = render();
    expect(html).toContain('data-testid="hdr-title"');
    expect(html).toContain("Acme Corp");
    expect(html).toContain('data-testid="hdr-status"');
    expect(tag(html, "hdr-edit")).toContain('href="/vendors/v1/edit"');
    expect(html).toMatch(/data-testid="hdr-edit"[^>]*>Edit</);
    expect(tag(html, "hdr-primary")).toContain(
      'href="/vendors/v1/reviews/new"',
    );
    expect(html).toMatch(/data-testid="hdr-primary"[^>]*>Record review</);
  });

  it("sits in PageShell's header: title in the H1, back link first in actions", () => {
    const html = renderToStaticMarkup(
      h(
        DetailHeader,
        {
          title: "Acme Corp",
          titleTestId: "hdr-title",
          back: h("a", { href: "/vendors", "data-testid": "hdr-back" }, "Back"),
          edit: { href: "/vendors/v1/edit", "data-testid": "hdr-edit" },
        },
        h("p", { "data-testid": "hdr-body" }, "body"),
      ),
    );
    expect(html).toContain("data-page-shell");
    expect(html.match(/<header/g)).toHaveLength(1);
    expect(html).toMatch(/<h1[^>]*><span data-testid="hdr-title">Acme Corp</);
    expect(html.indexOf("hdr-back")).toBeLessThan(html.indexOf("hdr-edit"));
    expect(html.indexOf("</header>")).toBeLessThan(html.indexOf("hdr-body"));
  });

  it("omits the overflow trigger when there are no overflow items", () => {
    expect(render()).not.toContain("detail-header-overflow-trigger");
  });

  it("renders the overflow trigger as a menu button", () => {
    const html = render(items());
    const trigger = tag(html, "detail-header-overflow-trigger");
    expect(trigger).toMatch(/^<button/);
    expect(trigger).toContain('aria-haspopup="menu"');
    expect(trigger).toContain('aria-expanded="false"');
    expect(trigger).toContain('aria-label="More actions"');
  });

  it("renders a one-click primary action as a button", () => {
    const html = renderToStaticMarkup(
      h(DetailHeader, {
        title: "Plan",
        primaryAction: {
          label: "Start",
          onClick: () => {},
          "data-testid": "start",
        },
      }),
    );
    expect(tag(html, "start")).toMatch(/^<button[^>]*type="button"/);
  });
});

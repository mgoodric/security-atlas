// #2102 — the shared detail-page header.
//
// The vitest tier is node-only with no DOM (slice 069 P0-A3), so this
// renders the header to static markup with `createElement` (no JSX) and
// drives the confirm rule through its pure model. The overflow menu's
// popup is portalled and only mounts in a browser, so the items are
// asserted through the confirm-state render and the model; the vendor
// page's Playwright specs cover the header in the real app.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { DetailHeader, InlineConfirm } from "./detail-header";
import {
  confirmingItem,
  confirmPromptFor,
  IDLE,
  reduceConfirm,
  type OverflowItem,
} from "./detail-header-model";

function items() {
  const archive = vi.fn();
  const remove = vi.fn();
  const list: OverflowItem[] = [
    { id: "archive", label: "Archive", onSelect: archive },
    {
      id: "delete",
      label: "Delete vendor",
      destructive: true,
      confirmPrompt: "Delete this vendor? This cannot be undone.",
      onSelect: remove,
    },
  ];
  return { list, archive, remove };
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
    const html = render(items().list);
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

describe("overflow confirm rule", () => {
  it("runs a non-destructive item straight away", () => {
    const { list, archive } = items();
    const { state, run } = reduceConfirm(
      IDLE,
      { type: "select", item: list[0] },
      list,
    );
    expect(state).toEqual(IDLE);
    expect(run).toBe(list[0]);
    run?.onSelect();
    expect(archive).toHaveBeenCalledOnce();
  });

  it("never runs a destructive item on select; it asks for a confirm", () => {
    const { list } = items();
    const { state, run } = reduceConfirm(
      IDLE,
      { type: "select", item: list[1] },
      list,
    );
    expect(run).toBeNull();
    expect(confirmingItem(state, list)).toBe(list[1]);
  });

  it("runs the destructive item only on confirm", () => {
    const { list } = items();
    const asked = reduceConfirm(IDLE, { type: "select", item: list[1] }, list);
    const { state, run } = reduceConfirm(
      asked.state,
      { type: "confirm" },
      list,
    );
    expect(run).toBe(list[1]);
    expect(state).toEqual(IDLE);
  });

  it("runs nothing on cancel", () => {
    const { list } = items();
    const asked = reduceConfirm(IDLE, { type: "select", item: list[1] }, list);
    const { state, run } = reduceConfirm(asked.state, { type: "cancel" }, list);
    expect(run).toBeNull();
    expect(state).toEqual(IDLE);
  });

  it("runs nothing on confirm when no item is waiting", () => {
    const { list } = items();
    expect(reduceConfirm(IDLE, { type: "confirm" }, list).run).toBeNull();
  });

  it("falls back to a generic prompt", () => {
    expect(
      confirmPromptFor({ id: "x", label: "Revoke key", onSelect: () => {} }),
    ).toBe("Revoke key? This cannot be undone.");
  });
});

describe("InlineConfirm", () => {
  it("shows the prompt with Cancel and a destructive confirm button", () => {
    const { list } = items();
    const html = renderToStaticMarkup(
      h(InlineConfirm, {
        item: list[1],
        onCancel: () => {},
        onConfirm: () => {},
      }),
    );
    expect(html).toContain('role="group"');
    expect(html).toContain('aria-label="Confirm: Delete vendor"');
    expect(html).toContain("Delete this vendor? This cannot be undone.");
    expect(html).toMatch(
      /data-testid="detail-header-confirm-cancel"[^>]*>Cancel</,
    );
    expect(html).toMatch(
      /data-testid="detail-header-confirm-ok"[^>]*>Delete vendor</,
    );
  });
});

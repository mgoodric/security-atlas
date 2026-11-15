// #2102 — the overflow menu's inline-confirm rule, and the confirm.
//
// The vitest tier is node-only with no DOM (slice 069 P0-A3). The menu
// popup is portalled and mounts only in a browser, so the items'
// select / confirm / cancel behaviour is asserted through the pure
// model, and the confirm and trigger are rendered to static markup.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { InlineConfirm, OverflowMenu } from "./overflow-menu";
import {
  confirmingItem,
  confirmPromptFor,
  IDLE,
  reduceConfirm,
  type OverflowItem,
} from "./overflow-menu-model";

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

describe("OverflowMenu", () => {
  it("renders nothing without items", () => {
    expect(renderToStaticMarkup(h(OverflowMenu, { items: [] }))).toBe("");
  });

  it("renders its trigger as a menu button", () => {
    const html = renderToStaticMarkup(
      h(OverflowMenu, { items: items().list, testIdPrefix: "row" }),
    );
    const at = html.indexOf('data-testid="row-overflow-trigger"');
    expect(at).toBeGreaterThan(-1);
    const trigger = html.slice(
      html.lastIndexOf("<", at),
      html.indexOf(">", at),
    );
    expect(trigger).toMatch(/^<button/);
    expect(trigger).toContain('aria-haspopup="menu"');
    expect(trigger).toContain('aria-expanded="false"');
    expect(trigger).toContain('aria-label="More actions"');
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
    expect(html).toMatch(/data-testid="overflow-confirm-cancel"[^>]*>Cancel</);
    expect(html).toMatch(
      /data-testid="overflow-confirm-ok"[^>]*>Delete vendor</,
    );
  });
});

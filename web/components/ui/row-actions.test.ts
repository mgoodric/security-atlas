// #2090 — RowActions + ConfirmInline.
//
// The vitest tier is node-only with no DOM (slice 069 P0-A3), so this
// renders to static markup with `createElement` (no JSX) and drives the
// confirm and disabled rules through the pure model. The menu popup is
// portalled and mounts only in a browser; the admin and settings
// Playwright specs cover the menu in the running app.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { ConfirmInline, RowActions } from "./row-actions";
import {
  confirmingItem,
  confirmPromptFor,
  IDLE,
  reduceConfirm,
  visibleItems,
  type RowActionItem,
} from "./row-actions-model";

function items() {
  const rotate = vi.fn();
  const revoke = vi.fn();
  const demote = vi.fn();
  const list: RowActionItem[] = [
    { id: "rotate", label: "Rotate", onSelect: rotate },
    {
      id: "revoke",
      label: "Revoke",
      destructive: true,
      confirmPrompt: "Revoke this key?",
      onSelect: revoke,
    },
    {
      id: "demote",
      label: "Demote",
      destructive: true,
      disabledReason: "The last super admin cannot be demoted.",
      onSelect: demote,
    },
  ];
  return { list, rotate, revoke, demote };
}

describe("RowActions", () => {
  it("renders the trigger as a menu button", () => {
    const html = renderToStaticMarkup(
      h(RowActions, {
        items: items().list,
        label: "Actions for key ab12",
        "data-testid": "key-actions",
      }),
    );
    const at = html.indexOf('data-testid="key-actions"');
    const trigger = html.slice(
      html.lastIndexOf("<", at),
      html.indexOf(">", at),
    );
    expect(trigger).toMatch(/^<button/);
    expect(trigger).toContain('aria-haspopup="menu"');
    expect(trigger).toContain('aria-expanded="false"');
    expect(trigger).toContain('aria-label="Actions for key ab12"');
  });

  it("renders nothing when every item is hidden", () => {
    const html = renderToStaticMarkup(
      h(RowActions, {
        items: [{ id: "x", label: "X", hidden: true, onSelect: () => {} }],
      }),
    );
    expect(html).toBe("");
  });
});

describe("row-actions confirm rule", () => {
  it("runs a non-destructive item straight away", () => {
    const { list, rotate } = items();
    const { state, run } = reduceConfirm(
      IDLE,
      { type: "select", item: list[0] },
      list,
    );
    expect(state).toEqual(IDLE);
    run?.onSelect();
    expect(rotate).toHaveBeenCalledOnce();
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

  it("hands the pending item back on cancel so its state can be reset", () => {
    const { list } = items();
    const onCancel = vi.fn();
    const withCancel = list.map((i) =>
      i.id === "revoke" ? { ...i, onCancel } : i,
    );
    const asked = reduceConfirm(
      IDLE,
      { type: "select", item: withCancel[1] },
      withCancel,
    );
    const out = reduceConfirm(asked.state, { type: "cancel" }, withCancel);
    expect(out.cancelled).toBe(withCancel[1]);
    out.cancelled?.onCancel?.();
    expect(onCancel).toHaveBeenCalledOnce();
    // A confirm hands back no cancelled item.
    const again = reduceConfirm(asked.state, { type: "confirm" }, withCancel);
    expect(again.cancelled).toBeUndefined();
  });

  it("never runs or confirms a disabled item", () => {
    const { list } = items();
    const { state, run } = reduceConfirm(
      IDLE,
      { type: "select", item: list[2] },
      list,
    );
    expect(run).toBeNull();
    expect(state).toEqual(IDLE);
  });

  it("drops a pending confirm whose item became disabled", () => {
    const { list } = items();
    const asked = reduceConfirm(IDLE, { type: "select", item: list[1] }, list);
    const now = list.map((i) =>
      i.id === "revoke" ? { ...i, disabledReason: "no longer allowed" } : i,
    );
    expect(confirmingItem(asked.state, now)).toBeNull();
    expect(reduceConfirm(asked.state, { type: "confirm" }, now).run).toBeNull();
  });

  it("filters hidden items out of the menu", () => {
    const { list } = items();
    const withHidden = [...list, { ...list[0], id: "h", hidden: true }];
    expect(visibleItems(withHidden).map((i) => i.id)).toEqual([
      "rotate",
      "revoke",
      "demote",
    ]);
  });

  it("falls back to a generic prompt", () => {
    expect(
      confirmPromptFor({ id: "x", label: "Revoke key", onSelect: () => {} }),
    ).toBe("Revoke key? This cannot be undone.");
  });
});

describe("ConfirmInline", () => {
  it("shows the prompt, the detail, Cancel and a destructive confirm", () => {
    const html = renderToStaticMarkup(
      h(
        ConfirmInline,
        {
          prompt: "Revoke this key?",
          confirmLabel: "Revoke",
          onCancel: () => {},
          onConfirm: () => {},
          "data-testid": "k-confirm",
        },
        h("input", { "data-testid": "k-reason" }),
      ),
    );
    expect(html).toContain('role="group"');
    expect(html).toContain('aria-label="Confirm: Revoke"');
    expect(html).toContain("Revoke this key?");
    expect(html).toContain('data-testid="k-reason"');
    expect(html).toMatch(/data-testid="k-confirm-cancel"[^>]*>Cancel</);
    expect(html).toMatch(/data-testid="k-confirm-ok"[^>]*>Revoke</);
  });

  it("disables the confirm while busy", () => {
    const html = renderToStaticMarkup(
      h(ConfirmInline, {
        prompt: "p",
        confirmLabel: "Revoke",
        busy: true,
        onCancel: () => {},
        onConfirm: () => {},
      }),
    );
    expect(html).toMatch(
      /<button[^>]*disabled=""[^>]*data-testid="confirm-inline-ok"/,
    );
  });
});

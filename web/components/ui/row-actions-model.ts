// #2090 — the row-actions overflow menu's model.
//
// Pure, so the rules that matter are covered in the node vitest tier
// without a DOM:
//   * a destructive item never fires from the menu; it opens an inline
//     confirm first, and only the confirm runs it;
//   * a disabled item (one the backend would refuse) never runs, and
//     carries the reason it is unavailable.
//
// The item shape matches #2102's detail-header `OverflowItem` (id,
// label, destructive, confirmPrompt, confirmLabel, onSelect) so the
// detail header and a table row share one menu. `row-actions.tsx`
// renders it.

import type { ReactNode } from "react";

export type RowActionItem = {
  /** Stable key, unique within one menu. */
  id: string;
  label: string;
  /** Irreversible actions (revoke, disable, delete). They confirm inline first. */
  destructive?: boolean;
  /** Inline-confirm prompt, e.g. "Revoke this key? Connectors using it fail on their next push." */
  confirmPrompt?: string;
  /** Confirm button label. Defaults to the item's label. */
  confirmLabel?: string;
  /** Extra content inside the inline confirm (e.g. a reason field). */
  confirmDetail?: ReactNode;
  /**
   * Why the action is unavailable (e.g. the backend refuses it). A set
   * reason renders the item disabled with the reason as visible text,
   * so it is read by screen readers, not only shown on hover.
   */
  disabledReason?: string;
  /** Leave the item out of the menu entirely. */
  hidden?: boolean;
  onSelect: () => void;
  "data-testid"?: string;
};

/** Which destructive item, if any, is waiting on its inline confirm. */
export type ConfirmState = { confirmingId: string | null };

export const IDLE: ConfirmState = { confirmingId: null };

export type ConfirmEvent =
  | { type: "select"; item: RowActionItem }
  | { type: "confirm" }
  | { type: "cancel" };

/** The items the menu renders: hidden ones are dropped. */
export function visibleItems(items: readonly RowActionItem[]): RowActionItem[] {
  return items.filter((i) => !i.hidden);
}

export function isDisabled(item: RowActionItem): boolean {
  return Boolean(item.disabledReason);
}

/**
 * Advance the confirm state. `run` is the item whose `onSelect` the
 * caller must call now: never a destructive item on `select`, and never
 * a disabled or hidden item.
 */
export function reduceConfirm(
  state: ConfirmState,
  event: ConfirmEvent,
  items: readonly RowActionItem[],
): { state: ConfirmState; run: RowActionItem | null } {
  switch (event.type) {
    case "select":
      if (isDisabled(event.item) || event.item.hidden) {
        return { state, run: null };
      }
      if (event.item.destructive) {
        return { state: { confirmingId: event.item.id }, run: null };
      }
      return { state: IDLE, run: event.item };
    case "confirm": {
      const item = confirmingItem(state, items);
      return { state: IDLE, run: item };
    }
    case "cancel":
      return { state: IDLE, run: null };
  }
}

/** The item awaiting confirmation, or null. A disabled or hidden item
 *  never counts, so a row that became refusable mid-confirm cannot run. */
export function confirmingItem(
  state: ConfirmState,
  items: readonly RowActionItem[],
): RowActionItem | null {
  const item = items.find((i) => i.id === state.confirmingId) ?? null;
  if (!item || isDisabled(item) || item.hidden) return null;
  return item;
}

/** Prompt text shown beside the inline confirm buttons. */
export function confirmPromptFor(item: RowActionItem): string {
  return item.confirmPrompt ?? `${item.label}? This cannot be undone.`;
}

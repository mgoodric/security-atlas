// #2090 — the row-actions overflow menu's model.
//
// Pure, so the rules that matter are covered in the node vitest tier
// without a DOM:
//   * a destructive item never fires from the menu; it opens an inline
//     confirm first, and only the confirm runs it;
//   * a disabled item (one the backend would refuse) never runs, and
//     carries the reason it is unavailable.
//
// The detail-page header (#2102) and table rows share this one menu;
// the header renders it with `variant="header"`. `row-actions.tsx`
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
  /** Called when its inline confirm is cancelled (Cancel or Esc), so
   *  the caller can reset any state the confirm held, e.g. a reason. */
  onCancel?: () => void;
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
 * a disabled or hidden item. `cancelled` is the item whose `onCancel`
 * the caller must call now (a cancel of a pending confirm).
 */
export function reduceConfirm(
  state: ConfirmState,
  event: ConfirmEvent,
  items: readonly RowActionItem[],
): {
  state: ConfirmState;
  run: RowActionItem | null;
  cancelled?: RowActionItem | null;
} {
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
      return {
        state: IDLE,
        run: null,
        cancelled: items.find((i) => i.id === state.confirmingId) ?? null,
      };
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

/** Anything ConfirmInline can move focus to. */
export type Focusable = { focus: () => void };

/** #2518 — where ConfirmInline puts focus when it opens. A form field
 *  the caller names (`initialFocus`) wins, so a confirm that needs a
 *  reason lands in its textarea; ConfirmInline's own effect runs after
 *  the field's, so a child `autoFocus` alone would lose to Cancel.
 *  Otherwise Cancel takes focus, so a second Enter can't fire the
 *  action, unless the caller passed its own `cancelRef` (RowActions)
 *  and manages that focus itself. */
export function confirmOpenFocus(opts: {
  initialFocus?: Focusable | null;
  callerManagesCancel: boolean;
  ownCancel: Focusable | null;
}): Focusable | null {
  if (opts.initialFocus) return opts.initialFocus;
  if (opts.callerManagesCancel) return null;
  return opts.ownCancel;
}

/** #2518 — where focus goes when ConfirmInline closes. Only a cancel
 *  (button or Escape) hands focus back to the trigger: a confirm runs
 *  the action, whose page owns what comes next. */
export function confirmCloseFocus(opts: {
  cancelled: boolean;
  returnFocus?: () => Focusable | null | undefined;
}): Focusable | null {
  if (!opts.cancelled || !opts.returnFocus) return null;
  return opts.returnFocus() ?? null;
}

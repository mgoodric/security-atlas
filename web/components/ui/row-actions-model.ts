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

/** #2682 — the rows to try, in order, for focus after `removed` left the
 *  list: the rows that followed it, then the rows before it, nearest
 *  first. Only rows still in `after` count. The caller skips a row whose
 *  trigger can't take focus (disabled, or none), and falls back to the
 *  list's heading or empty state when none can. */
export function removalFocusOrder(
  before: readonly string[],
  removed: string,
  after: readonly string[],
): string[] {
  const at = before.indexOf(removed);
  if (at < 0) return [...after];
  const still = new Set(after);
  const next = before.slice(at + 1).filter((k) => still.has(k));
  const prev = before
    .slice(0, at)
    .reverse()
    .filter((k) => still.has(k));
  return [...next, ...prev];
}

/** A confirmed removal waiting for its row to leave the list. `before`
 *  is the list when it was confirmed; `sawBusy` records that the action
 *  has been seen in flight. */
export type RemovalWatch = {
  key: string;
  before: readonly string[];
  sawBusy: boolean;
} | null;

/** #2682 — advance a removal watch on each render of the list. It
 *  resolves only once the action has been in flight and has settled
 *  (`settled` is the caller's "not pending", and a caller keeps it
 *  pending until its refetch is in), so a render before the mutation
 *  starts can't drop the watch. Settled with the row still listed means
 *  the action failed or kept the row: the watch ends with no focus move.
 *  Settled with the row gone returns `focusOrder`. */
export function watchRemoval(
  watch: RemovalWatch,
  keys: readonly string[],
  settled: boolean,
): { watch: RemovalWatch; focusOrder: string[] | null } {
  if (!watch) return { watch: null, focusOrder: null };
  if (!settled) return { watch: { ...watch, sawBusy: true }, focusOrder: null };
  if (!watch.sawBusy) return { watch, focusOrder: null };
  if (keys.includes(watch.key)) return { watch: null, focusOrder: null };
  return {
    watch: null,
    focusOrder: removalFocusOrder(watch.before, watch.key, keys),
  };
}

/** #2682 — whether focus was lost with the removed row, so moving it is
 *  a repair and not a steal. Lost means nothing is focused, the page
 *  itself is (`<body>`, where a browser puts focus when the focused
 *  element is removed), or the focused element is no longer in the
 *  document. Anything else means the user moved focus while the delete
 *  was in flight, and it stays where they put it. */
export function focusWasLost(opts: {
  active: object | null;
  body: object | null;
  activeConnected: boolean;
}): boolean {
  return (
    opts.active === null || opts.active === opts.body || !opts.activeConnected
  );
}

/** Focus the first candidate that takes it (`isFocused` reads back
 *  `document.activeElement`, since a disabled button ignores focus()).
 *  Returns the one focused, or null. */
export function focusFirst<T extends Focusable>(
  candidates: readonly (T | null | undefined)[],
  isFocused: (f: T) => boolean,
): T | null {
  for (const c of candidates) {
    if (!c) continue;
    c.focus();
    if (isFocused(c)) return c;
  }
  return null;
}

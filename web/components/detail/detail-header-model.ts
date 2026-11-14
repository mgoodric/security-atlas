// #2102 — the shared detail-page header's overflow-menu model.
//
// Pure, so the rule that matters (a destructive item never fires from
// the menu; it opens an inline confirm first) is covered in the node
// vitest tier without a DOM. `detail-header.tsx` renders it.

export type OverflowItem = {
  /** Stable key, unique within one header. */
  id: string;
  label: string;
  /** Irreversible actions (delete, revoke). They confirm inline first. */
  destructive?: boolean;
  /** Inline-confirm prompt, e.g. "Delete this vendor? This cannot be undone." */
  confirmPrompt?: string;
  /** Confirm button label. Defaults to the item's label. */
  confirmLabel?: string;
  onSelect: () => void;
  "data-testid"?: string;
};

/** Which destructive item, if any, is waiting on its inline confirm. */
export type ConfirmState = { confirmingId: string | null };

export const IDLE: ConfirmState = { confirmingId: null };

export type ConfirmEvent =
  | { type: "select"; item: OverflowItem }
  | { type: "confirm" }
  | { type: "cancel" };

/**
 * Advance the confirm state. `run` is the item whose `onSelect` the
 * caller must call now; it is never a destructive item on `select`.
 */
export function reduceConfirm(
  state: ConfirmState,
  event: ConfirmEvent,
  items: readonly OverflowItem[],
): { state: ConfirmState; run: OverflowItem | null } {
  switch (event.type) {
    case "select":
      if (event.item.destructive) {
        return { state: { confirmingId: event.item.id }, run: null };
      }
      return { state: IDLE, run: event.item };
    case "confirm": {
      const item = items.find((i) => i.id === state.confirmingId) ?? null;
      return { state: IDLE, run: item };
    }
    case "cancel":
      return { state: IDLE, run: null };
  }
}

/** The item awaiting confirmation, or null. */
export function confirmingItem(
  state: ConfirmState,
  items: readonly OverflowItem[],
): OverflowItem | null {
  return items.find((i) => i.id === state.confirmingId) ?? null;
}

/** Prompt text shown beside the inline confirm buttons. */
export function confirmPromptFor(item: OverflowItem): string {
  return item.confirmPrompt ?? `${item.label}? This cannot be undone.`;
}

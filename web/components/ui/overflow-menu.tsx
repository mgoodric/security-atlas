"use client";

// #2102 — an overflow ("⋯") menu whose destructive items confirm inline.
//
// One implementation for the detail-page header (#2102) and row actions
// (#2090). Destructive items never fire from the menu: picking one
// closes the menu and opens an inline confirm next to the trigger (no
// native confirm(), no modal). The rule lives in `overflow-menu-model.ts`
// so it is unit-tested.
//
// The trigger is base-ui's Menu, which supplies the menu-button
// semantics: aria-haspopup="menu", aria-expanded, arrow-key navigation,
// Esc to close, and focus back on the trigger. The inline confirm takes
// focus on open, closes on Esc, and hands focus back to the trigger.

import { Menu } from "@base-ui/react/menu";
import { useEffect, useRef, useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  confirmingItem,
  confirmPromptFor,
  IDLE,
  reduceConfirm,
  type ConfirmEvent,
  type ConfirmState,
  type OverflowItem,
} from "./overflow-menu-model";

export type { OverflowItem } from "./overflow-menu-model";

export type OverflowMenuProps = {
  items: readonly OverflowItem[];
  /** Accessible name of the trigger. */
  label?: string;
  /** Prefix for the trigger, menu and confirm testids. */
  testIdPrefix?: string;
  className?: string;
};

/** Renders nothing when there are no items. */
export function OverflowMenu({
  items,
  label = "More actions",
  testIdPrefix = "overflow",
  className,
}: OverflowMenuProps) {
  const [state, setState] = useState<ConfirmState>(IDLE);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  // Read by the menu's finalFocus when it closes, which can run before
  // the confirm has rendered; state would still read idle there.
  const confirmOpening = useRef(false);
  const pending = confirmingItem(state, items);

  function dispatch(event: ConfirmEvent) {
    const { state: next, run } = reduceConfirm(state, event, items);
    confirmOpening.current = next.confirmingId !== null;
    setState(next);
    run?.onSelect();
  }

  useEffect(() => {
    if (pending) cancelRef.current?.focus();
  }, [pending]);

  function close(event: ConfirmEvent) {
    dispatch(event);
    triggerRef.current?.focus();
  }

  if (items.length === 0) return null;

  return (
    <div className={cn("inline-flex flex-col items-end gap-2", className)}>
      <Menu.Root>
        <Menu.Trigger
          ref={triggerRef}
          aria-label={label}
          className={buttonVariants({ variant: "outline", size: "icon" })}
          data-testid={`${testIdPrefix}-overflow-trigger`}
        >
          <span aria-hidden="true">⋯</span>
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner align="end" sideOffset={4} className="z-50">
            <Menu.Popup
              // A destructive pick moves focus to the inline confirm (the
              // effect above), so don't pull it back to the trigger.
              finalFocus={() => !confirmOpening.current}
              className="min-w-40 rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none"
              data-testid={`${testIdPrefix}-overflow-menu`}
            >
              {items.map((item) => (
                <Menu.Item
                  key={item.id}
                  onClick={() => dispatch({ type: "select", item })}
                  className={cn(
                    "flex cursor-default items-center rounded-md px-2 py-1.5 text-sm outline-none select-none data-highlighted:bg-muted",
                    item.destructive && "text-destructive",
                  )}
                  data-testid={item["data-testid"]}
                  data-destructive={item.destructive ? "" : undefined}
                >
                  {item.label}
                </Menu.Item>
              ))}
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
      {pending ? (
        <InlineConfirm
          item={pending}
          cancelRef={cancelRef}
          testIdPrefix={testIdPrefix}
          onCancel={() => close({ type: "cancel" })}
          onConfirm={() => close({ type: "confirm" })}
        />
      ) : null}
    </div>
  );
}

export function InlineConfirm({
  item,
  cancelRef,
  testIdPrefix = "overflow",
  onCancel,
  onConfirm,
}: {
  item: OverflowItem;
  cancelRef?: React.Ref<HTMLButtonElement>;
  testIdPrefix?: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="group"
      aria-label={`Confirm: ${item.label}`}
      className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm"
      data-testid={`${testIdPrefix}-confirm`}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onCancel();
        }
      }}
    >
      <span>{confirmPromptFor(item)}</span>
      <Button
        ref={cancelRef}
        type="button"
        variant="outline"
        size="sm"
        onClick={onCancel}
        data-testid={`${testIdPrefix}-confirm-cancel`}
      >
        Cancel
      </Button>
      <Button
        type="button"
        variant="destructive"
        size="sm"
        onClick={onConfirm}
        data-testid={`${testIdPrefix}-confirm-ok`}
      >
        {item.confirmLabel ?? item.label}
      </Button>
    </div>
  );
}

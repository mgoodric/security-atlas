"use client";

// #2090 — RowActions: a row's overflow menu, and ConfirmInline, the
// inline confirm step for irreversible actions (revoke, disable,
// delete, revoke role).
//
// Destructive items never fire from the menu: selecting one closes the
// menu and opens ConfirmInline beside the trigger (no native confirm(),
// no modal). Cancel or Esc runs nothing. An item the backend refuses
// (the last admin's role, the last super-admin) renders disabled, its
// reason as visible text inside the item so screen readers read it too;
// `title` adds the hover tooltip. Hiding or disabling is presentation
// only: the backend keeps every refusal it has. The rules live in
// `row-actions-model.ts` so they are unit-tested.
//
// The trigger is base-ui's Menu, which supplies the menu-button
// semantics: aria-haspopup="menu", aria-expanded, arrow-key navigation,
// Esc to close, and focus back on the trigger. ConfirmInline takes focus
// on Cancel when it opens and hands focus back to the trigger on close.
//
// #2518 — a page that renders ConfirmInline itself usually swaps its
// trigger out for the confirm (or disables it), so on Cancel focus would
// drop to <body>. Such a page passes `returnFocus`, a getter for the
// trigger: ConfirmInline calls it after a cancel, once the trigger is
// back in the DOM. A list keeps one trigger per row with
// `useTriggerRefs`. A confirm holding a required field passes
// `initialFocus` so focus lands in the field instead of on Cancel.

import { Menu } from "@base-ui/react/menu";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type Ref,
  type RefObject,
} from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  confirmCloseFocus,
  confirmingItem,
  confirmOpenFocus,
  confirmPromptFor,
  IDLE,
  isDisabled,
  reduceConfirm,
  visibleItems,
  type ConfirmEvent,
  type ConfirmState,
  type RowActionItem,
} from "./row-actions-model";

export type { RowActionItem } from "./row-actions-model";

export type RowActionsProps = {
  items: readonly RowActionItem[];
  /** Accessible name for the trigger, e.g. "Actions for key ab12". */
  label?: string;
  /** `row` (default): a compact ghost trigger for table rows. `header`:
   *  an outline trigger sized to sit beside page-header buttons (#2102). */
  variant?: "row" | "header";
  /** Testid on the trigger. The confirm's testids derive from it:
   *  `<id>-confirm`, `<id>-confirm-cancel`, `<id>-confirm-ok`. */
  "data-testid"?: string;
  /** The trigger button, for a page that returns focus to it from a
   *  confirm it renders itself (#2518). */
  triggerRef?: RefObject<HTMLButtonElement | null>;
};

// The confirm closes as soon as it is confirmed, so the caller surfaces
// the action's pending and error state on its own page (an Alert), not
// inside the menu.
export function RowActions({
  items,
  label = "More actions",
  variant = "row",
  "data-testid": testId = "row-actions",
  triggerRef: outerTriggerRef,
}: RowActionsProps) {
  const [state, setState] = useState<ConfirmState>(IDLE);
  const ownTriggerRef = useRef<HTMLButtonElement>(null);
  const triggerRef = outerTriggerRef ?? ownTriggerRef;
  const cancelRef = useRef<HTMLButtonElement>(null);
  // Read by the menu's finalFocus when it closes, which can run before
  // the confirm has rendered; state would still read idle there.
  const confirmOpening = useRef(false);
  const shown = visibleItems(items);
  const pending = confirmingItem(state, items);

  function dispatch(event: ConfirmEvent) {
    const { state: next, run, cancelled } = reduceConfirm(state, event, items);
    confirmOpening.current = next.confirmingId !== null;
    setState(next);
    cancelled?.onCancel?.();
    run?.onSelect();
  }

  useEffect(() => {
    if (pending) cancelRef.current?.focus();
  }, [pending]);

  function close(event: ConfirmEvent) {
    dispatch(event);
    triggerRef.current?.focus();
  }

  if (shown.length === 0) return null;

  return (
    <div className="inline-flex flex-col items-end gap-2">
      <Menu.Root>
        <Menu.Trigger
          ref={triggerRef}
          aria-label={label}
          className={
            variant === "header"
              ? buttonVariants({ variant: "outline", size: "icon" })
              : buttonVariants({ variant: "ghost", size: "icon-sm" })
          }
          data-testid={testId}
        >
          <span aria-hidden="true">⋯</span>
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner align="end" sideOffset={4} className="z-50">
            <Menu.Popup
              // A destructive pick moves focus to the inline confirm
              // (the effect above), so don't pull it back to the trigger.
              finalFocus={() => !confirmOpening.current}
              className="min-w-40 max-w-72 rounded-lg bg-popover p-1 text-left text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none"
              data-testid={`${testId}-menu`}
            >
              {shown.map((item) => {
                const disabled = isDisabled(item);
                return (
                  <Menu.Item
                    key={item.id}
                    disabled={disabled}
                    onClick={() => dispatch({ type: "select", item })}
                    title={item.disabledReason}
                    className={cn(
                      "flex cursor-default flex-col items-start rounded-md px-2 py-1.5 text-sm outline-none select-none data-highlighted:bg-muted",
                      item.destructive && !disabled && "text-destructive",
                      disabled && "text-muted-foreground",
                    )}
                    data-testid={item["data-testid"]}
                    data-destructive={item.destructive ? "" : undefined}
                  >
                    <span>{item.label}</span>
                    {disabled ? (
                      <span
                        className="text-xs text-muted-foreground"
                        data-testid={
                          item["data-testid"]
                            ? `${item["data-testid"]}-reason`
                            : undefined
                        }
                      >
                        {item.disabledReason}
                      </span>
                    ) : null}
                  </Menu.Item>
                );
              })}
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
      {pending ? (
        <ConfirmInline
          prompt={confirmPromptFor(pending)}
          confirmLabel={pending.confirmLabel ?? pending.label}
          ariaLabel={`Confirm: ${pending.label}`}
          cancelRef={cancelRef}
          onCancel={() => close({ type: "cancel" })}
          onConfirm={() => close({ type: "confirm" })}
          data-testid={`${testId}-confirm`}
        >
          {pending.confirmDetail}
        </ConfirmInline>
      ) : null}
    </div>
  );
}

export type ConfirmInlineProps = {
  prompt: ReactNode;
  confirmLabel: string;
  ariaLabel?: string;
  cancelRef?: Ref<HTMLButtonElement>;
  /** Focus this instead of Cancel on open: the confirm's own required
   *  field (a reason textarea). */
  initialFocus?: RefObject<HTMLElement | null>;
  /** The element that opened the confirm. Called after a cancel (button
   *  or Escape), once ConfirmInline has unmounted and the trigger is
   *  back, and focused. Pass it whenever the trigger is swapped out or
   *  disabled while the confirm is open. */
  returnFocus?: () => HTMLElement | null | undefined;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  /** Extra content between the prompt and the buttons. */
  children?: ReactNode;
  "data-testid"?: string;
};

/** The inline confirm step: a prompt with Cancel and a destructive
 *  confirm button. Esc cancels. Never a native confirm() or a modal.
 *  Focus lands on Cancel when it opens, so a second Enter can't fire the
 *  action; a caller passing `cancelRef` manages that focus itself, and
 *  `initialFocus` names a field to land in instead. After a cancel,
 *  `returnFocus` puts focus back on the trigger. */
export function ConfirmInline({
  prompt,
  confirmLabel,
  ariaLabel,
  cancelRef,
  initialFocus,
  returnFocus,
  busy = false,
  onCancel,
  onConfirm,
  children,
  "data-testid": testId = "confirm-inline",
}: ConfirmInlineProps) {
  const ownCancelRef = useRef<HTMLButtonElement>(null);
  const callerManagesCancel = Boolean(cancelRef);
  useEffect(() => {
    confirmOpenFocus({
      initialFocus: initialFocus?.current,
      callerManagesCancel,
      ownCancel: ownCancelRef.current,
    })?.focus();
  }, [initialFocus, callerManagesCancel]);

  // Passive cleanup runs after the commit that unmounts the confirm has
  // attached the trigger's ref, so the getter sees the trigger again.
  const cancelled = useRef(false);
  const latestReturnFocus = useRef(returnFocus);
  useEffect(() => {
    latestReturnFocus.current = returnFocus;
  });
  useEffect(
    () => () => {
      confirmCloseFocus({
        cancelled: cancelled.current,
        returnFocus: latestReturnFocus.current,
      })?.focus();
    },
    [],
  );
  function cancel() {
    cancelled.current = true;
    onCancel();
  }
  return (
    <div
      role="group"
      aria-label={ariaLabel ?? `Confirm: ${confirmLabel}`}
      className="flex max-w-sm flex-col gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-left text-sm whitespace-normal"
      data-testid={testId}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          cancel();
        }
      }}
    >
      <span>{prompt}</span>
      {children}
      <div className="flex justify-end gap-2">
        <Button
          ref={cancelRef ?? ownCancelRef}
          type="button"
          variant="outline"
          size="sm"
          onClick={cancel}
          data-testid={`${testId}-cancel`}
        >
          Cancel
        </Button>
        <Button
          type="button"
          variant="destructive"
          size="sm"
          disabled={busy}
          onClick={onConfirm}
          data-testid={`${testId}-ok`}
        >
          {confirmLabel}
        </Button>
      </div>
    </div>
  );
}

/** #2518 — one trigger ref per row, for a list whose rows each open a
 *  ConfirmInline in place of their trigger:
 *  `ref={triggers.ref(id)}` on the trigger and
 *  `returnFocus={() => triggers.get(id)}` on the confirm. */
export function useTriggerRefs() {
  const map = useRef(new Map<string, HTMLElement>());
  return useMemo(
    () => ({
      ref: (key: string) => (el: HTMLElement | null) => {
        if (el) map.current.set(key, el);
        else map.current.delete(key);
      },
      get: (key: string) => map.current.get(key) ?? null,
    }),
    [],
  );
}

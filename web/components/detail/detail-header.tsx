"use client";

// #2102 — the shared detail-page header (parent #2071, "finish the
// record lifecycles").
//
// Extracted from the vendor detail page, the pattern every record's
// detail page follows: status, the record title, **Edit** (a link to
// the record's edit route), one primary **lifecycle action** for the
// current state, and an overflow menu. Destructive overflow items never
// fire from the menu: selecting one closes the menu and opens an inline
// confirm beside the actions (no native confirm(), no modal). The
// confirm rule lives in `detail-header-model.ts` so it is unit-tested.
//
// The overflow trigger is base-ui's Menu, which supplies the menu-button
// semantics: aria-haspopup="menu", aria-expanded, arrow-key navigation,
// Esc to close, and focus back on the trigger. The inline confirm takes
// focus on open, closes on Esc, and hands focus back to the trigger.
//
// It renders through PageShell (#2080): the title in the H1, status and
// subtitle in the subtitle slot, and the back link, primary action, Edit
// and overflow in the actions slot. Every testid is caller-supplied so
// adopting pages keep the selectors their existing specs use.

import { Menu } from "@base-ui/react/menu";
import Link from "next/link";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { PageShell } from "@/components/shell/page-shell";
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
} from "./detail-header-model";

export type { OverflowItem } from "./detail-header-model";

export type DetailHeaderLink = {
  href: string;
  label?: string;
  "data-testid"?: string;
};

export type DetailHeaderAction = {
  label: string;
  /** A link to the action's own form route, or ... */
  href?: string;
  /** ... a one-click handler. */
  onClick?: () => void;
  disabled?: boolean;
  "data-testid"?: string;
};

export type DetailHeaderProps = {
  title: ReactNode;
  /** Status badges, rendered under the title. */
  status?: ReactNode;
  /** Secondary line under the status (e.g. a domain). */
  subtitle?: ReactNode;
  /** Leads the actions slot, where PageShell pages keep their back link. */
  back?: ReactNode;
  edit?: DetailHeaderLink;
  primaryAction?: DetailHeaderAction;
  overflow?: readonly OverflowItem[];
  /** Page body, rendered under the header by PageShell. */
  children?: ReactNode;
  tabs?: ReactNode;
  /** The PageShell root's testid. */
  "data-testid"?: string;
  titleTestId?: string;
  /** Testid on the status + subtitle block under the title. */
  headerTestId?: string;
};

/** A record's detail page: PageShell with the shared header in its
 *  title, subtitle and actions slots, not a second header row. */
export function DetailHeader({
  title,
  status,
  subtitle,
  back,
  edit,
  primaryAction,
  overflow,
  children,
  tabs,
  "data-testid": testId,
  titleTestId,
  headerTestId,
}: DetailHeaderProps) {
  return (
    <PageShell
      data-testid={testId}
      title={
        titleTestId ? <span data-testid={titleTestId}>{title}</span> : title
      }
      subtitle={
        status || subtitle ? (
          <div className="mt-2 space-y-2" data-testid={headerTestId}>
            {status ? (
              <div className="flex flex-wrap items-center gap-2">{status}</div>
            ) : null}
            {subtitle}
          </div>
        ) : undefined
      }
      actions={
        <>
          {back}
          <DetailHeaderActions
            edit={edit}
            primaryAction={primaryAction}
            overflow={overflow}
          />
        </>
      }
      tabs={tabs}
    >
      {children}
    </PageShell>
  );
}

export type DetailHeaderActionsProps = Pick<
  DetailHeaderProps,
  "edit" | "primaryAction" | "overflow"
>;

/** The header's action cluster on its own, for a page whose title row
 *  is rendered by a shared page frame. */
export function DetailHeaderActions({
  edit,
  primaryAction,
  overflow = [],
}: DetailHeaderActionsProps) {
  const [state, setState] = useState<ConfirmState>(IDLE);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  // Read by the menu's finalFocus when it closes, which can run before
  // the confirm has rendered; state would still read idle there.
  const confirmOpening = useRef(false);
  const pending = confirmingItem(state, overflow);

  function dispatch(event: ConfirmEvent) {
    const { state: next, run } = reduceConfirm(state, event, overflow);
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

  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex items-center gap-2">
        {primaryAction ? <PrimaryAction action={primaryAction} /> : null}
        {edit ? (
          <Link
            href={edit.href}
            className={buttonVariants()}
            data-testid={edit["data-testid"]}
          >
            {edit.label ?? "Edit"}
          </Link>
        ) : null}
        {overflow.length > 0 ? (
          <Menu.Root>
            <Menu.Trigger
              ref={triggerRef}
              aria-label="More actions"
              className={buttonVariants({ variant: "outline", size: "icon" })}
              data-testid="detail-header-overflow-trigger"
            >
              <span aria-hidden="true">⋯</span>
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Positioner align="end" sideOffset={4} className="z-50">
                <Menu.Popup
                  // A destructive pick moves focus to the inline confirm
                  // (the effect above), so don't pull it back to the trigger.
                  finalFocus={() => !confirmOpening.current}
                  className="min-w-40 rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none"
                  data-testid="detail-header-overflow-menu"
                >
                  {overflow.map((item) => (
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
        ) : null}
      </div>
      {pending ? (
        <InlineConfirm
          item={pending}
          cancelRef={cancelRef}
          onCancel={() => close({ type: "cancel" })}
          onConfirm={() => close({ type: "confirm" })}
        />
      ) : null}
    </div>
  );
}

function PrimaryAction({ action }: { action: DetailHeaderAction }) {
  const className = buttonVariants({ variant: "secondary" });
  if (action.href && !action.disabled) {
    return (
      <Link
        href={action.href}
        className={className}
        data-testid={action["data-testid"]}
      >
        {action.label}
      </Link>
    );
  }
  return (
    <Button
      type="button"
      variant="secondary"
      disabled={action.disabled}
      onClick={action.onClick}
      data-testid={action["data-testid"]}
    >
      {action.label}
    </Button>
  );
}

export function InlineConfirm({
  item,
  cancelRef,
  onCancel,
  onConfirm,
}: {
  item: OverflowItem;
  cancelRef?: React.Ref<HTMLButtonElement>;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="group"
      aria-label={`Confirm: ${item.label}`}
      className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm"
      data-testid="detail-header-confirm"
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
        data-testid="detail-header-confirm-cancel"
      >
        Cancel
      </Button>
      <Button
        type="button"
        variant="destructive"
        size="sm"
        onClick={onConfirm}
        data-testid="detail-header-confirm-ok"
      >
        {item.confirmLabel ?? item.label}
      </Button>
    </div>
  );
}

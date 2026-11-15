"use client";

// #2102 — the shared detail-page header (parent #2071, "finish the
// record lifecycles").
//
// Extracted from the vendor detail page, the pattern every record's
// detail page follows: status, the record title, **Edit** (a link to
// the record's edit route), one primary **lifecycle action** for the
// current state, and an overflow menu whose destructive items confirm
// inline (`components/ui/overflow-menu.tsx`, shared with row actions).
//
// It renders through PageShell (#2080): the title in the H1, status and
// subtitle in the subtitle slot, and the back link, primary action, Edit
// and overflow in the actions slot. Every testid is caller-supplied so
// adopting pages keep the selectors their existing specs use.

import Link from "next/link";
import type { ReactNode } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Button, buttonVariants } from "@/components/ui/button";
import { OverflowMenu, type OverflowItem } from "@/components/ui/overflow-menu";

export type { OverflowItem } from "@/components/ui/overflow-menu";

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
  return (
    <div className="flex items-start gap-2">
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
      <OverflowMenu items={overflow} testIdPrefix="detail-header" />
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

"use client";

// #2135 — needs-attention rows and the For you / Everyone toggle, shared
// by the dashboard's first section and the full /needs-attention page.
//
// Each row shows the item's type chip, its band, its title (linking to
// the record), when it is due or since when it has waited, its owner,
// and the action to take as a button. The rows render what the platform
// returned, in its order: nothing is re-ranked or re-filtered here.

import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { Person } from "@/components/ui/person";
import { Timestamp } from "@/components/ui/timestamp";
import {
  needsAttentionBandLabel,
  needsAttentionTypeLabel,
  type NeedsAttentionItem,
  type NeedsAttentionView,
} from "@/lib/api/needs-attention";

type BadgeVariant = "critical" | "warning" | "info" | "secondary" | "outline";

const BAND_VARIANTS: Record<string, BadgeVariant> = {
  failing: "critical",
  approval: "info",
  overdue: "warning",
  due_soon: "secondary",
  ownership_gap: "outline",
};

export function NeedsAttentionList({
  items,
  testid = "needs-attention-list",
}: {
  items: NeedsAttentionItem[];
  testid?: string;
}) {
  return (
    <ul className="divide-y divide-foreground/5" data-testid={testid}>
      {items.map((it) => (
        <li
          key={`${it.band}/${it.type}/${it.id}`}
          data-testid="needs-attention-row"
          data-band={it.band}
          data-type={it.type}
          data-id={it.id}
          className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-sm"
        >
          <Badge variant="outline" data-testid="needs-attention-type">
            {needsAttentionTypeLabel(it.type)}
          </Badge>
          <Badge
            variant={BAND_VARIANTS[it.band] ?? "outline"}
            data-testid="needs-attention-band"
          >
            {needsAttentionBandLabel(it.band)}
          </Badge>
          <Link
            href={it.link}
            className="min-w-0 flex-1 truncate font-medium hover:underline"
            data-testid="needs-attention-title"
          >
            {it.title}
          </Link>
          <span className="text-xs text-muted-foreground">
            {it.due_at ? (
              <>
                Due <Timestamp value={it.due_at} style="date" />
              </>
            ) : it.since ? (
              <>
                Since <Timestamp value={it.since} style="date" />
              </>
            ) : null}
          </span>
          <span
            className="w-32 truncate text-xs text-muted-foreground"
            data-testid="needs-attention-owner"
          >
            {it.owner?.user_id ? (
              <Person id={it.owner.user_id} />
            ) : it.owner?.role ? (
              `Role: ${it.owner.role}`
            ) : (
              "No owner"
            )}
          </span>
          <Link
            href={it.link}
            className={buttonVariants({ variant: "outline", size: "sm" })}
            data-testid="needs-attention-action"
          >
            {it.action}
          </Link>
        </li>
      ))}
    </ul>
  );
}

// ViewToggle switches between For you and Everyone. It renders only when
// the caller may see everyone (`/v1/me` is_approver, the platform's
// authz.IsTenantApprover, which is what for=all checks).
export function ViewToggle({
  view,
  canSeeEveryone,
  onChange,
}: {
  view: NeedsAttentionView;
  canSeeEveryone: boolean;
  onChange: (v: NeedsAttentionView) => void;
}) {
  if (!canSeeEveryone) return null;
  const options: { value: NeedsAttentionView; label: string }[] = [
    { value: "me", label: "For you" },
    { value: "all", label: "Everyone" },
  ];
  return (
    <div
      role="group"
      aria-label="Whose items"
      className="inline-flex rounded-md border p-0.5"
      data-testid="needs-attention-view"
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={view === o.value}
          onClick={() => onChange(o.value)}
          data-testid={`needs-attention-view-${o.value}`}
          className={`rounded px-2.5 py-1 text-xs font-medium ${
            view === o.value
              ? "bg-primary text-primary-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// emptyText is the empty state for a view.
export function needsAttentionEmptyText(view: NeedsAttentionView): string {
  return view === "me"
    ? "Nothing needs you right now."
    : "Nothing needs attention right now.";
}

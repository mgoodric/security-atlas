// Slice 686 — pure formatting helpers for the read-only vendor detail
// view (`/vendors/[id]`). Extracted out of the page component so the
// branchy display logic (date trimming, owner mailto resolution, DPA
// status label) is covered by fast vitest unit tests; the page wiring
// itself is covered by the Playwright e2e spec.
//
// Total + pure (never throw). No React, no DOM — node-tier per
// vitest.config.ts `environment: "node"`.

import { isEmail } from "@/lib/email";

// formatDetailDate trims an ISO timestamp (or a bare date) to its
// `YYYY-MM-DD` portion for display, and renders an em-dash for an
// absent value. Mirrors the slice 681 risks/[id] `formatDate` shape.
export function formatDetailDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  return iso.slice(0, 10);
}

// ownerMailto resolves a `mailto:` href for the owner field when it is a
// valid email (AC-3), reusing the slice-679 `isEmail` predicate so the
// detail view and the edit form agree on what "an email" is. A role
// string ("Head of Security"), a blank, or a nullish owner resolves to
// null — the page renders such a value as plain text, not a link.
export function ownerMailto(owner: string | null | undefined): string | null {
  if (!owner) return null;
  const trimmed = owner.trim();
  if (!isEmail(trimmed)) return null;
  return `mailto:${trimmed}`;
}

// dpaStatusLabel renders the DPA state as a single human-readable string:
// a signed DPA surfaces its signing date when one is recorded, an
// unsigned DPA reads "Not signed" and ignores any stray date the row may
// still carry.
export function dpaStatusLabel(
  signed: boolean,
  signedAt: string | null | undefined,
): string {
  if (!signed) return "Not signed";
  const date = signedAt ? signedAt.slice(0, 10) : "";
  return date ? `Signed (${date})` : "Signed";
}

// reviewOutcomeLabel renders a vendor_reviews ledger outcome (slice 688)
// as a human-readable label for the history timeline. An unknown value
// falls back to the raw string so a future enum addition still renders
// honestly rather than blanking out.
export function reviewOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case "pass":
      return "Pass";
    case "pass_with_findings":
      return "Pass with findings";
    case "fail":
      return "Fail";
    case "waived":
      return "Waived";
    default:
      return outcome;
  }
}

// reviewOutcomeBadgeVariant maps a review outcome to a shadcn Badge
// variant so the timeline reads at a glance: a clean pass is muted, a
// pass-with-findings or waiver is a neutral outline, and a fail is
// destructive (the one outcome an operator must not miss).
export function reviewOutcomeBadgeVariant(
  outcome: string,
): "secondary" | "outline" | "destructive" {
  switch (outcome) {
    case "fail":
      return "destructive";
    case "pass_with_findings":
    case "waived":
      return "outline";
    default:
      return "secondary";
  }
}

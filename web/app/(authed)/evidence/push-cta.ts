// Slice 233 — pure-logic module for the /evidence page's "Push evidence"
// CTA. Extracted per the slice 219 (pack-header-meta.ts) precedent:
// web/vitest.config.ts is node-env / no-JSX, so the shape the JSX renders
// is asserted by unit-testing the constants the component reads.
//
// Pre-slice-233 the CTA was a permanently-disabled `<Button>` with no
// hover text, no link, no documentation pointer — a UI-honesty gap
// (slice 178 HONESTY-GAP class; surfaced as F-204-E-1 by the slice 204
// audit harness). Slice 233 Option A replaces the disabled button with
// a primary-styled `<a>` pointing at the canonical CLI push doc; the
// page subtitle gains a second sentence directing operators to the same
// destination.
//
// Destination choice (decision D1 in docs/audit-log/233-decisions.md):
// `/docs/primitives/evidence#pushing-evidence-from-your-own-tools` — the
// section heading "Pushing evidence from your own tools" in the evidence
// primitive doc carries the canonical `just atlas-cli evidence push`
// example. The spec offered `/admin/credentials` as an alternative; that
// route is itself unimplemented (a separate honesty gap), so anchoring
// to the CLI doc is the truthful pick.

export const PUSH_CTA_LABEL = "Push evidence →" as const;

export const PUSH_CTA_HREF =
  "/docs/primitives/evidence#pushing-evidence-from-your-own-tools" as const;

// The page subtitle gets a second sentence. The literal text below is the
// non-linked prefix; the JSX in page.tsx wraps the trailing "Push evidence
// →" in an `<a>` pointing at PUSH_CTA_HREF. Splitting into prefix + link
// label lets the unit test pin both halves without a JSX renderer.
export const PUSH_CTA_SUBTITLE_PREFIX = "Push via CLI or SDK — see " as const;

// Convenience: full subtitle text (prefix + label) for accessibility-tree
// snapshots or screen-reader output. Not used by the JSX render path —
// the JSX composes the prefix span + `<a>` directly — but it pins the
// concatenated string a screen reader actually announces.
export function pushCtaSubtitleSuffix(): string {
  return `${PUSH_CTA_SUBTITLE_PREFIX}${PUSH_CTA_LABEL}`;
}

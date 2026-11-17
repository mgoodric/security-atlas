# 2312 — Audit-period page follow-ups: decisions log

Issue: mgoodric/security-atlas#2312, follow-ups from the review of #2305
(#2117). Four items: a UUID guard in the audit BFFs, the control list's
title, `no-store` on the GET BFFs, and an error code for the OSCAL
period-not-found 404.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The `..` path-normalisation gap
  and the message-text match were found in the #2305 review.
- **detection_tier_target:** unit. The new ratchet test catches the first
  for any future route; the code-based match is pinned by a Go and a vitest
  test.

## Decisions

### D1 — reuse `rejectNonUUID`, guard the audit BFFs, ratchet the rest

`rejectNonUUID` already exists in `web/lib/api/bff.ts` and several BFFs use
it. No second helper. It now guards all four `/api/audits/[id]` routes: the
three the ticket names plus `oscal-export`, which interpolates the same id.

A sweep found 86 other `route.ts` files under a dynamic segment that do not
call it. Guarding all 86 here would widen this PR far past its ticket, and
several take a param that is not a UUID (`[key]`, `[kind]`, `[ref]`,
`[transition]`, `[action]`, possibly evidence and question ids), which need
their own allowlist check, not a UUID regex. Instead
`web/app/api/bff-uuid-guard.test.ts` is a ratchet:

- a dynamic route that neither calls `rejectNonUUID(` nor appears in
  `KNOWN_UNGUARDED` fails;
- a listed route that is now guarded, or no longer exists, fails as stale,
  so the list only shrinks.

The 86 routes are the `KNOWN_UNGUARDED` set in that file. The exposure is
the one the ticket describes: it needs the caller's own bearer and
SameSite=lax blocks it cross-site, so there is no privilege gain.

### D2 — retitle and say what the list is

The card is now titled "Controls" and its description begins "Every control
in the tenant, not only those in this period's framework." Both, because
the old title promised a period scope that #2117 D1 says the platform
cannot supply.

### D3 — `noStore` on the GET BFFs only

`/api/audits/[id]` and `/api/audits/[id]/control-state` wrap their response
in `noStore`. The freeze POST is not cached by browsers, and the OSCAL
download is a one-off attachment. No other layer reintroduces staleness:
`forwardJSON` already fetches upstream with `cache: "no-store"`, the routes
read `cookies()` so Next renders them dynamically. The one remaining cache
is TanStack Query's in-tab one, under the app's global 60s `staleTime`; a
freeze in another tab shows here once that expires and the tab regains
focus. That cache is per tab and short, and changing the global default is
outside this ticket.

### D4 — a `code` field, scoped to the OSCAL export

The OSCAL export handler's period-not-found 404 now writes
`{"error": "audit period not found", "code": "audit_period_not_found"}`,
with the code as `oscalexport.CodePeriodNotFound`. The message is unchanged
and human. The shape follows the existing `code` precedent
(`treatment_validation_failed` on the risks handler, `httperr`'s 5xx code).

The other audit-period handlers return the same 404 message without a code.
They are left alone: nothing on the web matches on them, and the
disambiguation only matters on the export route, where a bare 404 means the
bridge route is not mounted.

The generic OpenAPI `Error` schema gains an optional `code` property, so the
field is documented for every operation that returns one.

On the web, `APIError` gains an optional `code`, `throwAPIError` in
`lib/api/audit-periods.ts` reads it from the body, and
`isOscalBridgeUnavailable` compares the code, not the message.

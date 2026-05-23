# 238 — Policies list: missing "Linked control" and "Ack status" filter pills

**Cluster:** policies (UI parity)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet — `/policies` page)

## Narrative

Surfaced by the slice 204 audit of `/policies` against
`Plans/mockups/policies.html` (see
`docs/audit-log/204-page-audit-policies.md`).

The mockup at `Plans/mockups/policies.html` (lines 125–166) renders
**four** filter pills above the table:

1. **Status** (All / published / draft / under_review / retired)
2. **Owner role** (All / security_lead / cto / people_ops / legal)
3. **Linked control** (All / SCF:IAC-06 · MFA / SCF:CHG-04 · change mgmt …)
4. **Ack status** (All / ≥ 95% / < 95% / < 50%)

The production `/policies` page at
`web/app/(authed)/policies/page.tsx` renders **only two** of the four
(`status` + `owner_role` — see the `FILTER_KEYS` constant on line 83
and the `pills` array on line 171). The two mockup pills that don't
exist on the live page are:

- **Linked control** — would filter rows by a SCF-anchor or control
  ID that the policy is linked to. The list-endpoint wire
  (`policyWire` from slice 022 + `?include=ack_rate` from slice 107)
  does not expose a `linked_controls` field, so no backing data
  exists today.
- **Ack status** — would bin rows by the joined `ack_rate.percent`
  cell (≥ 95%, < 95%, < 50%). The `ack_rate` cell DOES exist on the
  wire (slice 107); a client-side filter against it is small, but
  intentionally absent today.

Both pills are mockup-claims that have no production backing. This
slice ships **just the two missing client-side filters that have
backing data**, and explicitly defers the `linked_controls` filter
to a follow-on once a policy↔control linkage surface exists.

## Threat model

**Verdict.** **no-mitigations-needed.** This is a client-side filter
addition. The filter operates on rows already returned by the
list endpoint — no new data path, no new auth surface, no new
external IO. The `ack_rate` cell is already authorized via the
list endpoint's existing RLS context.

## Acceptance criteria

- **AC-1.** A new `ack_status` filter pill renders between
  `owner_role` and the right-aligned `meta` counter, matching the
  shadcn `FilterPills` shape used elsewhere on the page. Options:
  `All` (default) / `≥ 95% acknowledged` / `< 95% acknowledged` /
  `< 50% acknowledged`.
- **AC-2.** The filter applies client-side: rows whose joined
  `ack_rate.percent` falls in the selected band remain visible.
  Rows with `ack_rate: null` (non-published) or
  `ack_rate.percent: null` (no required-role users) are filtered
  OUT for the `≥ 95%` / `< 95%` / `< 50%` selections, IN for `All`.
- **AC-3.** The pill participates in the URL-driven filter state
  pattern (mirrors the `status` + `owner_role` pills' query-string
  serialization). `?ack_status=ge95` etc. is bookmarkable.
- **AC-4.** A `Linked control` pill is **deferred** in this slice
  — file a follow-on (suggested title: "Policies list: linked-
  control filter pill + backing wire field") that requires:
  (a) the list endpoint to surface a `linked_controls: string[]`
  field per row, (b) the pill renders a multi-select against the
  union of values across visible rows. The follow-on is OUT of
  scope here.
- **AC-5.** Unit test in `web/app/(authed)/policies/filters.test.ts`
  asserts the new `ack_status` band predicate matches the mockup
  bands (≥ 95 / < 95 / < 50) and that the null-`ack_rate` rows are
  excluded from the non-`All` bands.
- **AC-6.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 9 (manual evidence is first-class).** The fix touches
  no evidence flow.
- **AI-assist boundary.** No AI-generated content touched.
- **Anti-pattern rejected.** "Vanity trust centers" — the audit
  caught a forward-looking mockup claim (Ack status pill) with
  backing data already on the wire; this slice ships the real
  thing rather than letting the mockup-claim drift.

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — UI honesty anti-pattern
- `Plans/canvas/04-evidence-engine.md` §4.5 — policy + acknowledgment
  primitives
- `Plans/mockups/policies.html` lines 125–166 — filter pills

## Dependencies

- **#204** (audit parent) — `in-progress`.
- **#107** (policy ack-rate join) — merged. The `ack_rate` cell is
  already on the wire; this slice is purely UI.

## Anti-criteria (P0 — block merge)

- **P0-238-1.** Does NOT add the `Linked control` pill in this
  slice — that needs backing-wire work and is filed as a follow-on
  per AC-4.
- **P0-238-2.** Does NOT change the `/v1/policies` wire shape. The
  filter is purely client-side over `policiesQ.data?.policies`.
- **P0-238-3.** Does NOT introduce per-row fan-out for filter
  evaluation (P0-A2 of slice 101 still applies).
- **P0-238-4.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix

1. Next.js App Router + shadcn/ui — adding a pill to an existing
   `FilterPills` collection.
2. URL-driven filter state — `useSearchParams` round-trip pattern
   already used by status/owner_role.
3. Vitest unit testing — extending
   `web/app/(authed)/policies/filters.test.ts`.
4. TypeScript narrowing — handling `ack_rate?.percent ?? null`
   in the band predicate cleanly.

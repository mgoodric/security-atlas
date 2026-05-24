# Slice 238 — /policies Linked-control + Ack-status filter pills (decisions log)

**Type:** JUDGMENT
**Spec:** `docs/issues/238-policies-list-missing-filter-pills-linked-control-ack-status.md`
**Status at slice close:** functionally complete on the slice branch;
ack-status pill wired client-side over the slice-107 joined cell;
vitest extended (27 tests, +11 new); Playwright spec extended
(quarantined per the slice-098 pattern); Linked-control deferred to a
follow-on per AC-4.

This log records the subjective design calls made during build, so the
maintainer can iterate post-deployment without reverse-engineering the
PR diff.

---

## D1 — Linked-control pill deferred per spec AC-4

**Decision.** This slice ships ONLY the `ack_status` pill. The
`Linked control` pill from the mockup at `Plans/mockups/policies.html`
lines 145-152 is filed as a follow-on rather than implemented here.

**Rationale.** The list endpoint `/v1/policies?include=ack_rate` does
not surface a `linked_controls` field per row. `policyWire` does
carry `linked_control_ids: string[]` (UUIDs), but rendering a usable
dropdown requires joining against control titles via
`/v1/controls/{id}` for each unique ID — that is per-row fan-out,
which P0-A2 of slice 101 explicitly forbids. The spec's AC-4 names
the precondition: the list endpoint must surface a `linked_controls:
string[]` field (titles, not UUIDs) before the pill can ship
honestly. Exposing the pill against UUIDs would render
`SCF-anchor-uuid-abc123…` in the dropdown — unusable.

**Confidence:** high. The spec is explicit; the wire-shape gap is
real.

**Follow-on filed.** Suggested title: "Policies list: linked-control
filter pill + backing wire field". The follow-on requires (a) the
list endpoint to surface a `linked_controls: string[]` per row (or a
joined `{id, title}` object), (b) the pill renders a multi-select
against the union of values across visible rows, and (c) URL
serialization for `?linked_control=<id>` (likely `&linked_control=`
repeated for multi-select).

---

## D2 — Band thresholds match `ackRateBand` SOC 2 CC1.4 norm

**Decision.** The three non-`ALL` bands use these predicates:

- `ge95`: `pct >= 95`
- `lt95`: `pct < 95`
- `lt50`: `pct < 50`

The 95% threshold matches `ackRateBand` in
`web/app/(authed)/policies/ack-rate.ts` (SOC 2 CC1.4 norm captured
in slice 101). The 50% threshold matches the mockup at
`Plans/mockups/policies.html` lines 154-165 (the "< 50%" red-flag
tier).

**Rationale.** Reusing the slice-101 threshold keeps the band
semantics consistent — the operator who sees a green `ackRateBand`
pill on the row gets the same row when they filter `>= 95%`. Naming
two different thresholds for the same concept would surface as a
silent UX bug.

The mockup omits the in-between `<70%` boundary that `ackRateBand`
uses (it tiers into amber). Slice 238 deliberately follows the
mockup's three-tier scheme (`>= 95% / < 95% / < 50%`) rather than
inventing a fourth band, because the spec's AC-1 enumerates exactly
these three non-`ALL` values.

**Confidence:** high. Thresholds match a real audit norm + the
mockup.

---

## D3 — Null-rate rows excluded from non-ALL bands (AC-2)

**Decision.** Rows with `ack_rate: null` (non-published) or
`ack_rate.percent: null` (zero-denominator: no required-role users)
are excluded from the `ge95` / `lt95` / `lt50` selections. Only the
`ALL` band includes them.

**Rationale.** AC-2 is explicit; the deeper reason is that band
claims against unknown data are misleading. A draft policy with no
acknowledgments has no rate — it cannot truthfully be claimed as
"under 50%" (it has zero datapoints, not "low"). The em-dash
treatment in the table column (slice 101 D1) extends to the filter:
unknown is unknown, not zero. The operator who selects `< 50%
acknowledged` is asking "show me underperforming published policies"
— a draft has not been given the chance to underperform.

**Confidence:** high. The honesty discipline matches slice 101.

---

## D4 — URL-safe short band values

**Decision.** Band values serialize as `ge95` / `lt95` / `lt50` (not
`gte_95_percent` or the human label `>= 95% acknowledged`).

**Rationale.** Short URL-safe identifiers (a) survive copy-paste
without URL encoding noise, (b) keep `/policies?ack_status=ge95`
short enough to share in a Slack message without auto-truncation,
(c) decouple the URL surface from the human label so a translation
or label tweak doesn't break bookmarks.

The trade-off: the URL value is not self-describing. A user
inspecting `?ack_status=ge95` must consult the page to learn what
the band means. The mitigation is that the pill's selected value
re-hydrates from the URL on load — the user sees "≥ 95%
acknowledged" in the pill once the page renders.

**Confidence:** high. The same pattern is used by every other URL-
driven pill in `web/app/(authed)/`.

---

## D5 — Unknown band value falls back to `ALL`

**Decision.** `ackStatusMatches` returns `true` for any band string
not in the known set (`ALL`, `ge95`, `lt95`, `lt50`).

**Rationale.** Forward-compat with stale URLs. If a future slice
renames a band (e.g. `ge95` → `compliant`) and an operator has an
older bookmark, the page should stay usable (rendering all rows)
rather than blanking. This is the same defensive pattern used in
slice 100's risks filter.

**Confidence:** medium. The behavior is conservative; the downside
is that a typo in a hand-crafted URL silently filters nothing rather
than erroring. The win is forward-compat.

**Revisit once in use.** If we add an "unknown band" telemetry
event (slice 178 honesty harness territory), we can log such cases
without changing the rendering.

---

## D6 — Pill order matches the mockup

**Decision.** The pill order is `Status` → `Owner role` → `Ack
status` → right-aligned meta counter.

**Rationale.** The mockup at `Plans/mockups/policies.html` lines
125-166 renders the four pills in this order: Status / Owner role
/ Linked control / Ack status. Since `Linked control` is deferred
per D1, `Ack status` takes its position in the row immediately
before the meta counter. Preserving the visual order keeps the
operator's muscle memory consistent once the deferred pill lands.

**Confidence:** high. Pill ordering is a mockup-pinned decision.

---

## D7 — Filter-narrowed empty-state copy updated

**Decision.** The filter-narrowed empty-state body now reads "Try
widening the status, owner-role, or ack-status filters." (vs. the
slice-101 "Try widening the status or owner-role filters.")

**Rationale.** When the operator narrows by `ack_status` to a band
no row matches, the previous copy was misleading — it pointed at
filters they hadn't touched. The updated copy names all three pills
so the operator knows which to widen.

**Confidence:** high. Empty-state copy honesty matches the slice 242
empty-state work.

---

## D8 — Playwright spec extended but quarantined

**Decision.** Two new test blocks are added to
`web/e2e/policies-list.spec.ts` for the ack_status pill, but their
bodies are commented out — the same quarantine pattern as the rest
of the file.

**Rationale.** The Playwright runner is installed but the seed-data
harness (slice 082) is not yet ready to seed published policies with
known ack-rate cells. Writing assertions today would either fail
on every run or pass against no data. The commented body acts as a
reviewable contract — when slice 082 lands and the suite turns on,
the assertions are ready to go.

**Confidence:** high. Mirrors slice 098 / 100 / 101 / 102 precedent.

---

## P0 anti-criteria verification

- **P0-238-1 — does NOT add the Linked-control pill in this slice.**
  Verified: `filters.ts` does not introduce a `linked_control` field;
  `page.tsx` `pills` array does not include a Linked-control entry.
  D1 above documents the follow-on.
- **P0-238-2 — does NOT change the `/v1/policies` wire shape.** No
  Go / proto / migration changes. The filter operates client-side
  over `policiesQ.data?.policies`.
- **P0-238-3 — does NOT introduce per-row fan-out for filter
  evaluation.** The band predicate is O(rows) with one comparison
  per row, no network calls.
- **P0-238-4 — does NOT use vendor-prefixed test fixture tokens.**
  All test fixtures use neutral `p1`/`p2`/`p3` IDs and `test policy`
  titles. No `sk-` / `tok-` / vendor-prefixed strings.

---

## Wire-format & schema impact

None. Slice 238 is a pure UI slice that consumes the
`?include=ack_rate` cell that slice 107 already wired. No Go, no
proto, no migration, no sqlc regeneration, no policy reload.

---

## Spillover

- **Follow-on slice (per D1):** "Policies list: linked-control
  filter pill + backing wire field". File alongside the slice 238
  merge so the deferred pill remains visible in the backlog.

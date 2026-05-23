# 245 — Risks mockup `above appetite` subtitle drop · decisions log

**Slice:** `docs/issues/245-risks-mockup-stale-above-appetite-subtitle.md`
**Branch:** `mockup/245-risks-mockup-update`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: JUDGMENT`. The spec surfaces a mockup-stale finding
(`47 risks · 3 above appetite` in `Plans/mockups/risks.html` line 111
references a concept — risk appetite — that has no v1 backend) and asks
for a walk-back of the subtitle. The spec's original ACs (AC-2 v2
placeholder, AC-3 open-question append, AC-4 CHANGELOG) were narrowed
by the orchestrator directive at execution time to: mockup edit +
decisions log only. This log records the calls I made inside that
narrowed scope.

---

## Decisions made

### D1 — Drop the stale tally entirely (vs. replace with a v1-honest tally)

**Decision:** **Drop the entire `47 risks · 3 above appetite` span
contents and replace with a tenant-context subtitle** (no count
aggregate). The conservative path: the mockup no longer promises a
metric the backend cannot compute.

**Options considered:**

| Option                                                                                                 | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Drop the tally entirely; replace with tenant-context** (`Sentinel Labs · production`) — _chosen_ | The orchestrator directive named this the conservative default. The span still renders (preserves layout); the text it carries is v1-honest because the live `/risks` page already shows tenant context in the AppShell header. No fictional aggregate.                                                                                                                                                                                                                                        |
| (b) **Replace with a v1-honest tally** (e.g., `47 risks · 12 untreated · 5 accepted`)                  | The spec mentions this as a possible replacement (counts derivable from the `treatment` column shipped in slice 019). Rejected: every other peer-mockup tally is a real metric the live page already computes — emitting an `untreated/accepted` rollup as mockup chrome implies the live `/risks` page emits the same rollup, which it does not. Adding the rollup would be a UI-feature decision belonging to a new slice (live page wire-up + BFF aggregate), not a mockup-stale walk-back. |
| (c) **Delete the span entirely** (drop the markup, not just the text)                                  | Rejected as a P0 anti-criterion of the spec (`P0-245-3: does NOT delete the mockup line — replace it with a truthful subtitle in the same shape`). Preserving the span shape keeps the mockup's layout audit-comparable with the live page.                                                                                                                                                                                                                                                    |
| (d) **Keep the count, drop only the appetite phrase** (`47 risks` alone)                               | The spec's AC-1 names this as the floor. Rejected in favor of (a) because the orchestrator directive said "page title + tenant" — tenant context is the chosen replacement, not a single-metric count. The live `/risks` page also doesn't surface a `47 risks` count tally in this header position today (slice 014/019 ship the list view, not the rollup), so even `47 risks` alone would carry a faint mockup-vs-live drift.                                                               |

**Rationale.** Option (a) matches a peer-mockup precedent:
`Plans/mockups/dashboard.html` line 121 carries
`<span class="text-sm text-slate-500">Sentinel Labs · production</span>`
in the identical markup position (sibling of the H1, inside the
title-row flex container). Reusing that shape is the lowest-novelty,
highest-pattern-consistency choice and avoids inventing a new subtitle
idiom.

The full text replacement on `Plans/mockups/risks.html:111`:

```diff
-          <span class="text-sm text-slate-500">47 risks · 3 above appetite</span>
+          <span class="text-sm text-slate-500">Sentinel Labs · production</span>
```

**Confidence:** **high.** The orchestrator directive set the bar
explicitly; the peer-mockup precedent is identical-shape; the spec's
P0 anti-criteria are all honored.

### D2 — Subtitle replacement text uses the dashboard tenant pattern verbatim

**Decision:** **Use `Sentinel Labs · production`** — the literal string
from `Plans/mockups/dashboard.html` line 121. No tenant-name variation
across mockups.

**Options considered:**

| Option                                                          | Why rejected / why chosen                                                                                                                                                                                                                                                                           |
| --------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) `Sentinel Labs · production` (dashboard pattern) — _chosen_ | Mockups are shared illustration of a single hypothetical tenant. Cross-page consistency on the tenant name + env is the norm: `dashboard.html` introduces "Sentinel Labs · production"; the risks page reuses it so a reader scanning the mockup set doesn't perceive a different tenant for risks. |
| (b) `Acme Corp · production` (different sample tenant)          | Rejected — introduces a fictional second tenant into the mockup set with no precedent.                                                                                                                                                                                                              |
| (c) `Sentinel Labs` (tenant only, no env)                       | Rejected — the dashboard precedent is name + env; matching the full shape keeps the audit comparison tight.                                                                                                                                                                                         |
| (d) `Risk register · production` (page-name + env)              | Rejected — duplicates the H1 verbatim. The H1 already says "Risk register". The subtitle position serves a different purpose (context), not a restatement of the page name.                                                                                                                         |

**Confidence:** **high.** Pattern-matched directly to an existing peer
mockup.

### D3 — Decisions-log filename uses the orchestrator-specified path

**Decision:** **`docs/audit-log/245-decisions.md`** (orchestrator-named
path), not `docs/audit-log/245-risks-mockup-stale-above-appetite-subtitle-decisions.md`
(the slice-template default).

**Rationale.** The orchestrator directive named the path explicitly.
Peer logs from slices 204–208 do use a `<NNN>-<slug>-decisions.md`
shape; the deviation here is small and deliberate. If the maintainer
prefers the longer form on review, a follow-up rename is one git mv.

**Confidence:** **medium.** The filename is bikeshed-level; following
the directive verbatim is the right call for a 20-min slice, but the
peer pattern is the longer-form filename and a future grep for "245
decisions" would still surface this file via the H1.

---

## Revisit once in use

Specific items the maintainer should re-evaluate post-merge, in order
of expected priority:

1. **v2 risk-appetite module placeholder.** The spec's AC-2 calls for
   filing a `<NNN>-risk-appetite-module-v2-placeholder.md` slice in
   the v2 spillover range. The orchestrator narrowed scope to exclude
   that filing. The maintainer should decide whether to file the
   placeholder separately (likely yes — the appetite concept IS a
   common GRC primitive and the spec's narrative §a–c sketches the v2
   design tension already). Suggested action: file the placeholder
   as a separate slice in the next v2-planning pass.
2. **Canvas open-question append.** The spec's AC-3 calls for
   appending a line to `Plans/canvas/11-open-questions.md` noting
   "risk appetite as a first-class field (v2+ decision)". Same
   reasoning as item 1: the orchestrator narrowed scope. If the v2
   placeholder lands later, the open-question line should land
   alongside it.
3. **Other mockup-stale findings under the slice 204 fleet.** The
   spec cites itself as one of the category-iv findings surfaced by
   the slice 204 audit fleet. Other category-iv findings (if any
   exist) deserve the same walk-back treatment per the slice-178
   vocabulary. The maintainer should sweep `_STATUS.md` for sibling
   `mockup-stale` slices and batch them.
4. **Subtitle convention across mockups.** With this edit, three peer
   mockups now use the tenant-context subtitle pattern (`dashboard`,
   `risks` after this slice, and arguably `settings` which uses a
   prose paragraph in `<p>` instead of a span). The other peer
   mockups carry aggregate-count subtitles. The subtitle position is
   doing double-duty (tenant context vs. aggregate metric). The
   maintainer may want to standardize on one shape across the mockup
   set during the next mockup-iteration pass.

---

## Confidence summary

| Decision                                                  | Confidence |
| --------------------------------------------------------- | ---------- |
| D1 — drop the tally; replace with tenant context          | **high**   |
| D2 — `Sentinel Labs · production` verbatim from dashboard | **high**   |
| D3 — `245-decisions.md` filename (vs. long form)          | **medium** |

No `low`-confidence decisions in this slice. The `medium` on D3 is a
naming convention point, not a load-bearing design call.

---

## Anti-criteria honored

All four spec anti-criteria are honored:

- **P0-245-1.** No `appetite` field added to schema, wire type, or API
  — only the mockup file changed.
- **P0-245-2.** No change to the live `/risks` page (`web/app/risks/page.tsx`
  untouched).
- **P0-245-3.** The mockup `<span>` is preserved; only its text content
  changed.
- **P0-245-4.** No v2-placeholder slice filed (per the orchestrator
  scope narrowing; maintainer to file separately if desired — see
  "Revisit once in use" item 1).

The orchestrator's anti-criteria are also honored: no production code
changes; no `_STATUS.md` change; no `CHANGELOG.md` change; no
`Plans/canvas/*` change.

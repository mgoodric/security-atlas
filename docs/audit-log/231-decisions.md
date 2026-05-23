# Slice 231 — UI honesty: dashboard mockup-stale audit-cycle status pill (build-time decisions)

> Slice front-matter is `Type: AFK`. The mechanical edit (remove a `<div>` block, leave an in-place HTML comment) has no subjective surface, but the framing question — **mockup-stale vs. ship-gap** — is the load-bearing judgment call. The slice spec pre-resolved that question with the audit's recommendation (MOCKUP-STALE), and this engineer concurred without dissent. The decisions log is captured anyway for the post-deployment iteration trail per the per-slice-template convention (`Plans/prompts/04-per-slice-template.md` "Slice types").

---

## D1 — MOCKUP-STALE (remove the pill) chosen over SHIP-GAP (build a topbar pill)

**Decision:** Remove the amber `"SOC 2 Type II · Q2 2026 in progress"` pill `<div>` block from `Plans/mockups/dashboard.html` (lines 39–42 in the pre-edit file). Replace in-place with an HTML comment recording the deletion and the rationale, citing slice 231 and the slice 183 mockup-vs-production precedent. The production `web/components/shell/topbar.tsx` is NOT touched.

**Why:**

- **Audit-period state has a real home.** Slice 042 ships the audit-period card inside the `/audits` workspace as the authoritative surface for "is an audit currently running, and which." The topbar pill in the mockup duplicates that affordance in global chrome. Duplicating it introduces a state-sync responsibility (which audit-period is "current" when the topbar pill renders? which gets primacy if a tenant has two concurrent audits?) that the workspace card resolves cleanly via the audit-period detail view.
- **Multi-concurrent audit-periods are a v1 reality, not a v2 hypothetical.** Slice 030's audit-period design explicitly supports concurrent audits within a tenant (e.g., a SOC 2 Type II observation window running simultaneously with an ISO 27001 surveillance audit). A single-string topbar encoding (`"SOC 2 Type II · Q2 2026 in progress"`) cannot represent that state without becoming a stacked-pill UI experiment — and the design intent for a stacked-pill UI was never committed by anyone. The mockup encodes a design idea, not a built or planned surface.
- **Backing code path is absent.** The slice spec's grep result (zero matches for `"Q2 2026 in progress"`, `"audit-cycle"`, or any topbar-rendered AuditPeriod state in `web/components/shell/topbar.tsx`) is reproducible — no production code reads, renders, or fetches a "current audit-cycle" status for the topbar. Promoting the pill from mockup to ship would require: (a) a backing endpoint, (b) a TanStack Query hook, (c) a multi-concurrent-audit UX design pass, (d) a freshness/staleness boundary for the pill's "in progress" claim, and (e) re-validation against the canvas §1.6 anti-pattern "continuous-monitoring lies" (a static pill saying "in progress" without a freshness signal is exactly that anti-pattern). Cost is high; the destination workspace card already does the job; the user-need test is satisfied.
- **Slice 183 precedent.** Slice 183 removed two stale mockup-only entries (Vendors sidebar entry, trailing Admin sidebar entry) when the production sidebar diverged from the mockup. The slice 183 D3 decision explicitly bounded "mockup follows the production design, not the other way around" — applied recursively, the dashboard topbar pill follows the same rule. The production topbar shows logo + tenant switcher + sign-out only; the mockup follows.
- **Canvas alignment.** `Plans/canvas/01-vision.md` explicitly rejects "vanity trust centers" and the broader anti-pattern class of decorative status surfaces that imply live state they don't actually drive from data. A persistent topbar pill with a pulsing dot but no backing data path is the in-app analog of a vanity trust center. The MOCKUP-STALE resolution is consistent with the constitutional rejection of that surface class.

**Alternative considered:** SHIP-GAP — file a separate slice to build the topbar pill as a production affordance.

**Rejected because:** No user-need signal from the solo-security-leader persona supports a global-chrome audit-cycle indicator. The user's mental model for "where am I in this audit" is the `/audits` workspace (slice 042); the dashboard answers "how is the program doing." Conflating the two in the topbar burns a scarce chrome slot for a redundant surface. The audit's recommendation (MOCKUP-STALE) is correct on user-need grounds, not just on absence-of-implementation grounds.

**Confidence:** high. The decision is bounded by a constitutional anti-pattern (vanity status surfaces), an established mockup-vs-production divergence precedent (slice 183), and an authoritative surface that already exists for the underlying concept (slice 042's audit-period card). The maintainer reviewing this slice does not need to second-guess the framing call.

---

## D2 — In-place HTML comment retained (not a silent deletion)

**Decision:** Replace the deleted pill `<div>` block with the verbatim HTML comment dictated by AC-2 of the slice spec, in the exact same DOM position (immediately after the `<div class="ml-auto flex items-center gap-3">` opening tag, before the search-input `<div class="relative">`).

**Why:**

- **Spec literal compliance.** AC-2 is literal: the comment text is dictated word-for-word by the spec. Free-text variants would be drift.
- **Forensic discoverability.** Future contributors who grep for `"SOC 2 Type II · Q2 2026"` or `audit-cycle` in the mockup expecting to find the pill will instead find the comment and a citation chain to slice 231 + slice 042 + slice 030. That's the iteration-1 mockup file's documentation discipline (the mockups are design-doc reference, not production code — the comment captures the decision context).
- **Re-add cost is low.** If the maintainer later judges that a topbar pill should ship, the comment names the prerequisite ("Re-add behind a backing data path") and points to the relevant slices, so the future SHIP-GAP slice has a starting point that's better than "git log archeology."

**Alternative considered:** Silent deletion (no comment).

**Rejected because:** Iteration-1 mockup hygiene per the slice 183 precedent installs comments at the deletion site. The slice 183 PR also did this for the same forensic-discoverability reason. Departing from that pattern would be cheap inconsistency.

**Confidence:** high.

---

## Revisit triggers

- **If a topbar audit-cycle indicator becomes a real user-need signal** (e.g., the solo-security-leader user explicitly asks for "always show me which audit is running" during the v1 user-testing pass), file a fresh SHIP-GAP slice that scopes (a) the backing endpoint, (b) the multi-concurrent-audit UX, (c) the freshness boundary, and (d) the constitutional check against the "vanity status surface" anti-pattern. The mockup-comment in `dashboard.html` is the natural anchor for that slice.
- **If slice 042's audit-period card is removed or substantially refactored**, re-check whether the topbar-pill question reopens. The current resolution depends on the workspace card being the authoritative surface; if that surface goes away, the global-chrome question is back on the table.
- **If a future mockup-honesty audit pass (slice 204 successor) re-scans `Plans/mockups/dashboard.html`**, confirm no new audit-cycle pills have re-accreted. The comment is the canary.

---

## Files touched

- `Plans/mockups/dashboard.html` — removed pill `<div>` block (4 lines), inserted in-place HTML comment (1 line). Net `−4 / +1`.
- `docs/audit-log/204-page-audit-dashboard.md` — appended **Resolution** sub-bullet to the F-204D-4 finding entry citing this slice. Net `+1 line`.
- `docs/audit-log/231-decisions.md` — this file. New.

No production code touched. No `_STATUS.md` row flip in the same commit as the implementation (orchestrator owns the status flip per the per-slice-template workflow).

# 331 — Accessibility audit (WCAG 2.1 AA) decisions log

Slice 331 is `Type: JUDGMENT`. This log records the per-decision
reasoning for methodology choices, scope-bounding, severity tiering,
and spillover routing. The audit narrative lives at
`docs/audits/331-a11y-wcag-audit.md`.

Format: Diagnosis · Decision · Revisit-trigger · Confidence.

---

## D1 — Static-only review (no runtime browser scans in this slice)

**Diagnosis.** The slice doc bounds the work to "audit-only · spillover fan-out" and explicitly says the implementing agent should not modify code. A live-browser axe-core / WAVE / Lighthouse pass against the dev server (a) adds a dev-server bring-up step that's brittle in worktree CI, (b) produces output that's harder to ground per file:line than a static markup read, and (c) repeats most of the load-bearing findings the static read already surfaces (skip-link absence, contrast tokens, missing ARIA on combobox patterns are all visible without rendering).

**Decision.** STATIC review only. The slice produces a per-file finding table; live verification is captured as a spillover gate where a finding's confidence requires it (A11Y-13 explicitly tagged). Subsequent fix-PRs for any Critical or High finding MUST do a live axe-core scan before merge.

**Revisit-trigger.** First fix-PR for a Critical / High finding lands without a live-browser verification step in its decisions log.

**Confidence.** HIGH. The static surface for the eight WCAG 2.1 AA categories in the slice narrative is well-covered by a markup-and-Tailwind read against the shared primitives + the representative page sample. Runtime-only findings (motion behavior, screen-reader playback order) are correctly flagged as Medium-with-verification-gate rather than asserted as Critical without evidence.

---

## D2 — Bounded sample (~7 representative components, not all 30+ pages)

**Diagnosis.** The slice narrative enumerates ~30 routes; auditing each top-level page exhaustively would (a) produce a flat list of 200+ findings most of which are the same primitive-level bug repeated per consumer, (b) not respect the slice doc's "Spot-check rather than exhaustive" instruction (notes section), and (c) push the audit far past its 1.5d estimate.

**Decision.** Audit the shared SHELL (authed layout · topbar · sidebar · mobile-sidebar · global-search) + the SHARED PRIMITIVES (`web/components/ui/*` + `web/components/list/*`) + ONE representative of each template-pattern (login = auth-flow; dashboard = dashboard; controls list = list-with-filter; controls detail + risks/hierarchy = detail-with-panels; admin/super-admins + admin/tenants = form-heavy + dialog). Each primitive-level finding propagates to every consumer page automatically; per-page findings (e.g. heading hierarchy) are caught by template spot-check.

**Revisit-trigger.** If a stakeholder review surfaces a class of bug present only in unsampled pages (e.g. `/calendar`-specific keyboard trap, `/board-packs/[id]/page.tsx`-specific reading order bug), file an extension slice scoped to that template-pattern.

**Confidence.** HIGH. The shell + primitives strategy is exactly what `voltagent-qa-sec:accessibility-tester` recommends ("Semantic HTML priority · ARIA roles usage · Widget patterns · Label associations" — all primitive-level decisions). The page-pattern sample covers list / detail / dashboard / form / dialog / auth — the five template-patterns that account for all of `web/app/`. Per-page polish findings are explicitly downgraded to Medium so the strategy doesn't hide them.

---

## D3 — Severity tiering (Critical / High / Medium / Low)

**Diagnosis.** WCAG conformance levels (A / AA / AAA) tell you the SC level but not the user impact — a single AAA-level finding could be more user-painful than ten A-level findings depending on flow. The project's existing audit precedents (slices 327 / 329 / 333) use a 3-tier (High / Medium / Low) scheme; slice 331's narrative + acceptance criteria explicitly call out a 4-tier (Critical / High / Medium / Low) with different fan-out treatment per tier:

- **Critical** (AC-3): page is unusable for an SR or keyboard-only user → individual spillover slice
- **High** (AC-4): a specific action is unreachable but page navigable → individual spillover slice
- **Medium** (AC-5): cosmetic-but-noticeable → bundled OR per-component slices (engineer's call)
- **Low** (AC-5 carryover): minor friction → no spillover, audit report only

**Decision.** Use the 4-tier scheme from the slice doc verbatim. WCAG SC level is recorded per-finding but is NOT the primary severity axis — user-impact-on-flow is. A Level-A finding can be Medium if a workaround exists; an AAA advisory (e.g. 2.3.3 Animation from Interactions) can be elevated to Medium when the slice narrative explicitly calls it out as an in-scope acceptance criterion (A11Y-10).

**Revisit-trigger.** First spillover slice for a Critical / High finding lands and turns out to be a 5-line lift (over-severity-ed) OR a 3d implementation (under-severity-ed). Threshold = first delta of ≥2 estimate-buckets between predicted and actual.

**Confidence.** HIGH for Critical / High (the user-impact axis is mechanical when the finding blocks a keyboard or SR flow); MEDIUM for Medium vs Low (the line is at "workaround exists" which is judgmental — see decisions D4 + D5 below for individual cases).

---

## D4 — Skip-link absence as Critical (A11Y-1)

**Diagnosis.** WCAG 2.4.1 Bypass Blocks is Level A — the lowest conformance level — which would normally suggest a Medium or Low rating. But the project's authed shell is unusually chrome-heavy: topbar (logo + breadcrumb + global search + ⌘K hint + audit pill + tenant switcher + user avatar + sign-out = 7-8 affordances) + sidebar (13 nav items + 2 count badges = 15 affordances) = roughly 25 keyboard stops before the user reaches page content. Multiplied by every navigation. For a keyboard-only user (e.g. someone with an RSI flare, a motor impairment, or just using a screen reader on a laptop without a mouse), this transforms simple navigation into a multi-minute keyboard marathon EVERY TIME.

**Decision.** Promote A11Y-1 to **Critical** (above its Level-A WCAG floor). The slice doc's Critical definition is "page is unusable for a keyboard-only user OR a screen-reader user" — and a 25-tab marathon to reach content meets the user-impact threshold even though WCAG-level it's only A. This is the EXACT case the slice doc's "user-impact-on-flow is the primary severity axis" intent covers.

**Revisit-trigger.** If the fix-PR comes in at < 5 LOC (which it should — one `<a>` + one `id` + one Tailwind class group), confirm the over-severity rating was about USER IMPACT, not implementation cost. The two-axis nature of severity is the point.

**Confidence.** HIGH. The chrome stop-count is mechanical to verify; the user-impact framing is in the slice doc verbatim. A WCAG-purist could argue "it's only Level A, so Medium" — that's the wrong axis; the slice doc anticipates exactly this case.

---

## D5 — Medium findings bundled vs individual slices

**Diagnosis.** The slice doc gives engineer discretion on Medium routing: "bundled into a single 'a11y polish round 1' slice OR per-component slices — engineer's call" (AC-5). The 8 Medium findings (A11Y-6 through A11Y-13) span seven different files / surfaces (sidebar ARIA · heading hierarchy · filter-pill focus · disabled-button tooltip · prefers-reduced-motion · panel error live regions · table reflow · tenant-switcher live region). Each Medium is roughly a 2-10 LOC lift. The spillover cap is 5 and is already consumed by the Critical + 4 High individual slices.

**Decision.** **BUNDLE** the 8 Medium findings under a single "a11y polish round 1" follow-up slice — filed separately by the maintainer (or with maintainer approval to widen the cap on this audit), NOT in this slice. Reasoning:

1. **Cap respect.** Cap=5; individual Critical/High eat all 5 slots. The slice doc's "OR per-component slices" branch requires expanding the cap, which is a maintainer decision (not the audit's).
2. **Cost-to-merge ratio.** 8 small lifts in one PR ship faster than 8 PRs each needing review + CI + decisions log. The polish-round-1 idiom matches the project's other audit-bundle precedents.
3. **Independence.** All 8 Mediums are independent two-line lifts; no two have a dependency on each other, so the bundle PR is not "one bug holds up the others."
4. **Verification cost.** Live-browser verification (D1) needs to run once per fix-batch, not once per finding. The polish round amortizes the verification step.

**Revisit-trigger.** Maintainer reads this audit and decides to (a) widen the cap and let the implementing agent file 8 individual slices, OR (b) ratify the bundle and file it as a maintainer-owned follow-up. Either path is fine; the decision is on the maintainer's desk.

**Confidence.** HIGH. The cap math is mechanical (5 used); the engineer-discretion clause is the slice doc's own. The bundle approach is the project's documented precedent for audit polish rounds.

---

## D6 — Low findings: no spillover (audit report only)

**Diagnosis.** Slice doc AC-5 explicitly covers Medium routing but is silent on Low. The slice narrative's threshold is "minor friction" — a sharpening edit that improves clarity but does not block compliance. AC-9 ("No code modified") plus AC-5 (Medium routing only) plus the convention from sibling audit slices (333 / 334 / 335) suggests: Low = audit report only.

**Decision.** Low findings (A11Y-14 through A11Y-16) live in the audit report's findings table but produce NO spillover slice. They are documented for the next a11y maintenance pass to pick up if relevant.

**Revisit-trigger.** Next a11y audit (annual cadence per slice 002 of the audit-fleet conventions). If a Low finding has become more frequent / more user-painful, it gets re-tiered then.

**Confidence.** HIGH. The convention matches the sibling audit precedents; the slice doc does not contradict.

---

## D7 — Spillover slot numbering (start at 359)

**Diagnosis.** Per slice doc instruction:
`ls docs/issues/[0-9]*.md | sed -E 's|.*/([0-9]+).*|\1|' | sort -n | tail -1` returned 358. The next slot is 359.

**Decision.** Spillover slot numbers: **359 · 360 · 361 · 362 · 363**. Filed sequentially Critical → High in finding-ID order (A11Y-1 → A11Y-5). No gaps reserved.

**Revisit-trigger.** Concurrent slice-filing pass collides with these numbers (the maintainer reconciles via the standard `_STATUS.md` claim trail).

**Confidence.** HIGH. Mechanical computation; no judgment involved.

---

## D8 — Cross-reference to slice 178 (UI honesty audit harness)

**Diagnosis.** Slice doc AC-8 explicitly asks whether the audit's findings could extend the slice 178 harness with a11y assertions. Of the 16 findings, two are cheap mechanical wins for the harness:

- **A11Y-1 (skip-link)** — single Playwright `expect(page.locator('a[href="#main-content"]')).toBeVisible()` per route. ~5 LOC per spec.
- **A11Y-7 (heading hierarchy)** — axe-core's `heading-order` rule wraps the assertion BUT axe-core is not a current dep. Could be lifted as a hand-rolled DOM walk: `expect(headingsInOrder(page)).toMatchExpectedOrder()`.

The contrast findings (A11Y-2 + A11Y-4) and the combobox finding (A11Y-3) would benefit from axe-core integration — a new dependency. That's a meta-question for the maintainer.

**Decision.** File **slice 364** (Medium bundle's first item — IF the cap is widened OR the polish-round-1 bundle absorbs it) for the harness extension Phase 1: skip-link + heading-order assertions only. NO axe-core dep in this slice. Axe-core's integration is its own conversation; file a follow-up question when this audit lands. Slice 364 lives in the Medium bundle conceptually — it is NOT a spillover from this audit, it is a harness-extension follow-up acknowledged in the audit report's "Cross-references" section.

**Revisit-trigger.** Maintainer reads the bundle proposal and decides whether to lift the harness extension as a separate slice or absorb into polish-round-1.

**Confidence.** HIGH. The slice 178 harness is the right home; the two cheap mechanical wins do not need axe-core; the axe-core conversation is correctly deferred.

---

## D9 — No code modified (AC-9 verification)

**Diagnosis.** Slice doc AC-9: "No code modified. Diff = doc files only." The slice anti-criterion P0-331-3 is identical. AC-10 requires `pre-commit run --files` passes on the diff.

**Decision.** This slice adds exactly TWO new files:

- `docs/audits/331-a11y-wcag-audit.md` (audit narrative)
- `docs/audit-log/331-a11y-wcag-audit-decisions.md` (this file)

No `web/` file is touched; no migration is touched; no test is touched; no CI config is touched; no canvas / CLAUDE.md is touched (P0-331-7). The PR diff will be doc-files-only by mechanical construction.

**Revisit-trigger.** `git status` immediately before commit shows anything in `web/` / `internal/` / `migrations/` / `.github/` / `cmd/` / `proto/` / `policies/` / `schemas/` / `Plans/` / `CLAUDE.md` modified or added.

**Confidence.** HIGH. Trivially verifiable at commit time.

---

## Summary of findings + dispositions

| Finding | WCAG SC                          | Severity | Disposition                                                      |
| ------- | -------------------------------- | -------- | ---------------------------------------------------------------- |
| A11Y-1  | 2.4.1 Bypass Blocks (A)          | Critical | **Slice 359** (skip-link in authed layout)                       |
| A11Y-2  | 1.4.3 Contrast (Minimum) (AA)    | High     | **Slice 360** (light-mode `--muted-foreground` token)            |
| A11Y-3  | 4.1.2 Name, Role, Value (A)      | High     | **Slice 361** (global search combobox ARIA)                      |
| A11Y-4  | 1.4.3 Contrast (Minimum) (AA)    | High     | **Slice 362** (in-progress audit pill dark-mode contrast)        |
| A11Y-5  | 3.3.1 + 3.3.2 (A)                | High     | **Slice 363** (admin form raw input + error association)         |
| A11Y-6  | 1.3.1 (A)                        | Medium   | Medium bundle — multi-nav `aria-label`                           |
| A11Y-7  | 2.4.6 (AA)                       | Medium   | Medium bundle — heading hierarchy + harness extension candidate  |
| A11Y-8  | 2.1.1 (A) + 2.5.5 (AAA advisory) | Medium   | Medium bundle — filter-pill focus ring                           |
| A11Y-9  | 1.4.13 (AA)                      | Medium   | Medium bundle — replace `title` tooltip pattern                  |
| A11Y-10 | 2.3.3 (AAA) + project AC         | Medium   | Medium bundle — `prefers-reduced-motion` honor                   |
| A11Y-11 | 4.1.3 (AA)                       | Medium   | Medium bundle — dashboard panel live regions                     |
| A11Y-12 | 1.4.10 (AA)                      | Medium   | Medium bundle — table reflow / mobileMode opt-in                 |
| A11Y-13 | 3.2.2 (A)                        | Medium   | Medium bundle — tenant-switcher live region (needs verification) |
| A11Y-14 | 1.1.1 (A)                        | Low      | Audit report only · no spillover                                 |
| A11Y-15 | 2.4.7 (AA)                       | Low      | Audit report only · no spillover                                 |
| A11Y-16 | 1.3.1 (A)                        | Low      | Audit report only · no spillover                                 |

**Spillover count.** 5 individual slices (cap met). Medium bundle is a separate follow-up (D5).
**Verification gate.** A11Y-13 explicitly carries a "live-browser verification required" gate (D1).
**Harness extension candidate.** A11Y-1 + A11Y-7 named in D8 as cheap Phase 1 lifts for slice 178's harness; deferred as a follow-up question.

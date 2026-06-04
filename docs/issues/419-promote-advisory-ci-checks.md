# 419 — Promote long-stable advisory CI checks to required (or formally retire)

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready` (no unmerged deps — every candidate job is on main and has soaked)

## Narrative

**WHY.** Several CI jobs were added under the project's "promote after a few green runs"
convention but have sat **advisory** (not in `branch-protection.json`'s
`required_status_checks.contexts`) for 100+ slices past their soak window:

| Job (CI name)                  | Origin    | Current state | Evidence of stale deferral                                                                                                                           |
| ------------------------------ | --------- | ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Self-host bundle e2e           | slice 065 | advisory      | Not in contexts; bundle has been green since slice 065 (gh#115)                                                                                      |
| `Helm chart · lint + template` | slice 038 | advisory      | Not in contexts; green since gh#124                                                                                                                  |
| `Frontend · vitest`            | slice 069 | advisory      | `branch-protection.json` `$deviations_from_slice_069` literally says its promotion is "filed as a separate follow-on (no slice number assigned yet)" |
| prod-build Playwright          | slice 387 | advisory      | `Frontend · Playwright e2e (prod-build standalone)` not in contexts; merged gh#896                                                                   |

The "soak then promote" step long since came due; the deferral is now **drift**. An advisory
required-quality check is a gate that _looks_ like protection but isn't — a regression in any
of these surfaces can land on `main` green, because the check is not in the contexts list
branch protection actually enforces. This is precisely the failure slice 116 fixed for
Playwright (advisory → required after a verified soak) and the exact wording the
`branch-protection.json` `$deviations_from_slice_069` block flags as an open loose end for
vitest.

**WHAT.** Audit each advisory candidate's green-run history, then for each: **promote** the
proven-stable ones to required by adding their exact CI check name to
`branch-protection.json`'s `required_status_checks.contexts` and running
`scripts/apply-branch-protection.sh`; OR, for any that should stay advisory, document the
explicit reason in `branch-protection.json` (matching the existing `$deviations_*` /
`$additions_*` annotation convention) so "advisory" is a decision, not an accident.

**SCOPE DISCIPLINE.** This slice changes the _set_ of required checks — and ONLY that.
It does NOT change any job's behavior, does NOT add new jobs, does NOT touch the merge-queue
(415) or sharding (417). Promotion is gated on real green-run evidence per check (the slice
116 bar: a documented soak). A check whose soak is NOT clean is NOT promoted — it gets a
fix-first follow-on note, not a forced promotion. The promote-vs-retain-advisory call per
check is the JUDGMENT, recorded in the decisions log with the green-run evidence.

## Threat model

STRIDE pass. Changing the required-check set is a _governance_ control surface — it directly
determines what can reach `main`. Both directions carry a real threat: promoting a flaky
check wedges the merge button (availability); failing to promote a stable check leaves a
regression hole (integrity). This is the opposite-of-rubber-stamp case.

**S — Spoofing.** No endpoint/identity. The change is to a JSON policy file applied via the
existing maintainer token. No new auth surface. (`enforce_admins: true` already means the
maintainer cannot bypass required checks — promotion binds the maintainer too.)

**T — Tampering (integrity — the load-bearing threat).** **T-1 (a regression lands green on
an un-promoted stable check).** Today, a change that breaks the Helm chart, the self-host
bundle, the prod-build BFF cookie path, or the vitest BFF-route logic can merge because none
of those is a required context. For a product whose v1 thesis is "the customer diligences the
diligence tool," a broken self-host bundle or Helm chart silently reaching `main` is a
material integrity failure — the operator's first install breaks. Mitigation: promote the
checks with a clean soak (AC-2/AC-3); the promoted check then _blocks_ the regression. This
slice's deliverable IS the mitigation for T-1.

**I — Information disclosure (advisory check misrepresents protection).** **I-1.** An advisory
check posts a green/red status that _looks_ like a gate in the PR UI but does not block
merge. A contributor (or the maintainer at speed) can reasonably believe a green advisory
check means "protected," when it does not. Promoting (or explicitly documenting why it stays
advisory) removes the misrepresentation. Mitigation: AC-5 — every candidate ends EITHER in
the contexts list OR with a written `branch-protection.json` reason; no candidate is left in
silent-advisory limbo.

**D — Denial of service (availability — the counter-threat).** **D-1 (promoting a flaky check
wedges merges).** If a candidate flakes (the prod-build Playwright and vitest surfaces are the
likeliest), promoting it to required means every flake now blocks every PR — a self-inflicted
availability hit, the inverse of T-1. Mitigation: P0-1 — a check is promoted ONLY with a
documented clean soak (slice 116 bar: ≥5 clean runs or equivalent). A candidate that fails the
soak is NOT promoted (it gets a fix-first note). The decisions log records the soak evidence
per promoted check so the call is auditable, not optimistic.

**R / E.** No audit-trail or privilege-boundary surface beyond the above. The
branch-protection drift detector (slice 158, `branch-protection-drift-live`) continues to
catch file-vs-live divergence after the apply.

**Verdict: has-mitigations.** T-1 (integrity hole from under-promotion) and D-1 (availability
wedge from over-promotion) are the two opposing threats; the slice resolves both by gating
promotion strictly on documented clean soak, and by forcing every candidate to a _documented_
terminal state (required, or advisory-with-reason).

## Acceptance criteria

- [ ] **AC-1.** For each candidate (Self-host bundle e2e, `Helm chart · lint + template`,
      `Frontend · vitest`, prod-build Playwright), the decisions log records its recent
      green-run history (last N PR/main runs, pass/flake count) as the promotion evidence.
- [ ] **AC-2.** Every candidate with a documented CLEAN soak (slice 116 bar) is added to
      `branch-protection.json` `required_status_checks.contexts` under its EXACT CI check name.
- [ ] **AC-3.** Every candidate that is NOT promoted has an explicit reason recorded in
      `branch-protection.json` (a `$deviations_*` / `$retain_advisory_*` annotation, matching
      the file's existing convention) — no candidate is left in silent-advisory limbo.
- [ ] **AC-4.** The CI check names added to the contexts list match the literal `name:` values
      in `ci.yml` exactly (a typo'd context never resolves and wedges every PR — verified
      against the job `name:` strings).
- [ ] **AC-5.** Each promoted check has a working slice-061 stub-twin (so docs-only PRs still
      resolve the new required context green) — verified the stub exists for each promoted job
      name; if a candidate lacks a stub-twin, the slice adds one OR documents why the check is
      unconditional.
- [ ] **AC-6.** `bash scripts/apply-branch-protection.sh` succeeds against the updated file
      (or the maintainer-apply step is documented in the PR body if token scope is unavailable
      in-slice).
- [ ] **AC-7.** The `branch-protection-drift-validate` PR-time job (slice 158) passes against
      the edited `branch-protection.json` (file shape stays valid).
- [ ] **AC-8.** The `$deviations_from_slice_069` annotation for `Frontend · vitest` is updated
      to reflect its new state (promoted, with the soak evidence, OR retained-advisory with the
      specific reason) — the "no slice number assigned yet" loose end is closed by THIS slice
      number.
- [ ] **AC-9.** Decisions log at `docs/audit-log/419-promote-advisory-checks-decisions.md`
      records the promote-vs-retain call per candidate, the soak evidence, and a revisit list.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md "four enforced surfaces"): promoting `Frontend · vitest`
  and the self-host/Helm/prod-build checks tightens the gate around the four surfaces, in the
  direction the discipline already points (slice 116 set the precedent).
- v1 binary success test (CLAUDE.md): a green self-host bundle + Helm chart is load-bearing
  for "the operator runs their next audit out of security-atlas" — promoting those checks
  protects the install path the success test depends on.
- Branch-protection-as-code (slices 050/127/158): the change is a reviewable diff to
  `branch-protection.json` applied via the standard script, with the drift detector watching.

## Canvas references

- CLAUDE.md "Testing discipline (four enforced surfaces)" + the `branch-protection.json`
  `$deviations_from_slice_069` block (the documented loose end this slice closes).
- `Plans/canvas/10-roadmap.md` (self-host / Helm are roadmap-load-bearing surfaces; their
  CI gates should be required).

## Dependencies

- **#065** — `merged` (self-host bundle e2e job — candidate).
- **#038** — `merged` (Helm chart lint+template job — candidate).
- **#069** — `merged` (vitest surface — candidate; its promotion is the documented open loop).
- **#387** — `merged` (prod-build Playwright standalone job — candidate).
- **#116** — `merged` (the advisory→required promotion precedent + the soak bar).
- **#158** — `merged` (branch-protection drift detector; must stay green after the edit).

## Anti-criteria (P0 — block merge)

- **P0-1 (security — D-1).** A check is promoted to required ONLY with a documented clean soak
  (slice 116 bar). A candidate whose soak shows flakes is NOT promoted — it gets a fix-first
  follow-on note, never a forced promotion that wedges merges.
- **P0-2 (security — T-1 / I-1).** Every candidate ends in a DOCUMENTED terminal state
  (required, or advisory-with-written-reason). No candidate is left silently advisory.
- **P0-3.** Context names added MUST exactly match the `ci.yml` `name:` strings — a typo'd
  required context never resolves and bricks all merges. Verified by AC-4.
- **P0-4.** Does NOT add, remove, or change the BEHAVIOR of any CI job — this slice edits the
  required-set only. (Adding a missing stub-twin per AC-5 is the sole job-touching exception,
  and it is behavior-preserving for the docs-only fast-path.)
- **P0-5.** Does NOT touch merge-queue config (415) or the integration job structure (417).
- **P0-6.** Does NOT auto-merge; the maintainer reviews the required-set change before it
  goes live (it binds the maintainer too via `enforce_admins`).

## Skill mix (3-5)

`release-manager` · `ci-cd-pipeline-builder` · `dependency-auditor` (n/a) · `grill-with-docs`
· `Security` (governance-threat verification pass).

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ "Promote" = add the job's exact `name:` to
  `required_status_checks.contexts` + apply. "Advisory" = the job runs and posts a status but
  is NOT in contexts, so it does not block merge. Use the literal CI `name:` strings — the
  match is exact-string, and a near-miss is catastrophic (P0-3).
- _Scope._ Do NOT promote a check whose soak is dirty. The honest move for a flaky candidate
  (likely prod-build Playwright and/or vitest) is "file the flake fix first, promote after" —
  not "promote and accept the wedge." That mirrors slice 116's discipline (it promoted
  Playwright only after ≥5 clean runs).
- _Already-built check._ `rg -l "advisory.*required|promote.*advisory" docs/issues/` returns
  the precedent slices (116/069/089) but no slice that drains the current advisory backlog.
  This is it.

**Threat-model context (Phase 3).** The two threats pull opposite ways: under-promote and you
leave T-1 integrity holes (a broken self-host bundle merges green); over-promote and you hit
D-1 availability wedges (a flaky vitest blocks every PR). The resolution is strict: gate
promotion on documented clean soak, force every candidate to a documented terminal state.
The vitest and prod-build surfaces are the ones to scrutinize for flakiness before promoting;
the Helm-lint and self-host-bundle checks are deterministic build/template checks and are the
safest promotions.

**Implementation note.** Pull the green-run history via `gh run list --workflow ci.yml
--json conclusion,headSha,name` filtered per job name (the same data slice 352's
`flake-counter.sh` walks). The `branch-protection.json` file already has the annotation
convention to mirror (`$additions_from_slice_NNN` / `$deviations_from_slice_NNN`); add a
`$additions_from_slice_419` block summarizing the promotions + soak evidence.

**Provenance.** Filed 2026-06-03 in the CI-backlog batch (415-420). Closes the
`$deviations_from_slice_069` "no slice number assigned yet" loose end and drains the advisory
backlog accumulated since slices 038/065/069/387.

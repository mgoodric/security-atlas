# 204 — Comprehensive page-by-page UI parity audit (per-page agent fleet)

**Cluster:** Quality (UI parity / honesty)
**Estimate:** 2d (fleet wall-clock + spillover-filing overhead; ~11 mockup pages × ~15 min per page + aggregate)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** maintainer-surfaced 2026-05-22. Prior audits (slice 154 settings-only, slice 178 honesty-harness first-pass) produced spillovers (162/163/164/183/184/185/186 — all merged) but did not exhaustively cover every page or compare every comp claim against runtime behavior. Maintainer's frustration with the deployed v1.14.0 UI is the trigger: dark-mode wiring missing (filed as slice 203), light-mode logo invisible (slice 203 again), and per maintainer's 2026-05-22 message most components return 500 errors on the deployed v1.14.0 release.

## Narrative

The two prior UI audits (slice 154 settings page; slice 178 honesty-harness) shipped real value (8 + 11 spillovers filed across them, mostly merged) but each focused on ONE narrow surface — settings, or a harness with a thin first-pass. The maintainer's review of the v1.14.0 deployed release surfaces that the rest of the UI was not comparably scrutinized: there are mockup claims that have no backing implementation, broken interactions, missing dark-mode visual treatment (slice 203), and (per the maintainer's 2026-05-22 message) most components 500 on the deployed build.

This slice ships a **per-page audit fleet**. The implementation IS the audit: when the slice is picked up by the parallel-batch automation, the implementing engineer dispatches eleven concurrent audit-Agent subagents (one per mockup HTML file at `Plans/mockups/*.html`). Each agent:

1. Reads its assigned mockup HTML
2. Renders the corresponding live page from the running dev build OR the most recent main commit
3. Compares the two on four explicit axes: (i) layout / chrome parity (header, sidebar, footer match), (ii) broken interactions (buttons that no-op, links that 404, forms that don't submit, row-clicks that go to wrong destinations — the slice 178 honesty class), (iii) data-bound surfaces that lie (mockup shows "47 controls" but live shows nothing, or vice-versa — the slice-178 HONESTY-GAP class), (iv) text in the mockup that references features whose implementation does not exist (the slice-178 MOCKUP-STALE class)
4. For each finding, files ONE new slice via the `/idea-to-slice` skill (or its inline equivalent), with explicit citation of this slice (#204) as parent and the affected page + mockup file in the narrative
5. Writes its per-page report into `docs/audit-log/204-page-audit-<page-slug>.md`

The fleet's aggregate output is captured in `docs/audit-log/204-aggregate.md`: a single table listing every spillover slice filed, its page, its finding category (i/ii/iii/iv), and its preliminary priority guess (the operator triages priority post-merge).

**The slice itself is the audit dispatcher.** No production code change. No schema change. No backend touch. The slice's value materializes through the spillovers, which then enter the normal parallel-batch loop for implementation.

**Scope discipline (what is OUT):**

- **Fixing any finding inline** — every finding files as its own slice. The audit is read-only.
- **Investigating server-side 500-error class** — that's its own slice (parent 204 will note it; the actual fix is a separate spec the maintainer files after diagnosing root cause). The audit fleet may surface "this page 500s" as a finding category, but DOES NOT debug the cause.
- **Re-running slice 178's harness** — that harness ran already (8 findings, 6+2 split). This slice is the human-judgement parity pass, not a heuristic-rule pass.
- **Visual design critique** — color choices, typography refinement, spacing nits. Out of scope; that's a designer-led pass for a future slice.
- **Mobile / responsive audit** — out of scope; mockups are desktop-only.
- **Cross-tenant / multi-tenant UX edge cases** — out of scope unless a mockup explicitly shows them.

## Threat model

This is a read-only audit; the slice's deliverables are documentation files. STRIDE pass per `/idea-to-slice` discipline:

| STRIDE                | Threat                                                                                                                                                      | Mitigation                                                                                                                                                                                                                                                              |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Audit agent navigates to authenticated pages — could it surface session/cookie data in its report?                                                          | AC-7: per-agent reports must scrub `Set-Cookie` headers, JWT bearer strings, and `atlas_session` cookie values before commit. Concrete check: post-fleet `grep -r "Bearer\|atlas_session=" docs/audit-log/204-*.md` must return empty.                                  |
| **T** Tampering       | None — audit is read-only.                                                                                                                                  | n/a                                                                                                                                                                                                                                                                     |
| **R** Repudiation     | None — audit doesn't perform mutating operations.                                                                                                           | n/a                                                                                                                                                                                                                                                                     |
| **I** Info disclosure | Audit report might include screenshots or DOM dumps that contain real tenant data (control names, evidence captions, audit-period dates) from the dev seed. | AC-8: per-agent reports use the existing audit-harness's read-only `makeReadOnly(page)` pattern AND seed the dev build with the well-known `e2e-seed` fixture (no real tenant data). Reports are explicitly NOT permitted to include screenshots of production tenants. |
| **D** DoS             | Spawning 11 concurrent Playwright instances on the operator's machine could exhaust resources.                                                              | AC-9: cap fleet concurrency to 4 in-flight agents (3 page agents + 1 aggregate writer). If the local machine can't sustain 4 concurrent Playwright runs, fall back to serial execution and accept the wall-clock hit.                                                   |
| **E** EoP             | None — audit doesn't touch authz.                                                                                                                           | n/a                                                                                                                                                                                                                                                                     |

## Acceptance criteria

- [ ] **AC-1**: One audit-Agent subagent dispatched per mockup file in `Plans/mockups/*.html`. Eleven files at the time of writing: `audits.html`, `board-pack.html`, `control.html`, `controls.html`, `dashboard.html`, `evidence.html`, `index.html`, `policies.html`, `questionnaire.html`, `risks.html`, `settings.html`.
- [ ] **AC-2**: Each agent's prompt explicitly enumerates the four finding categories (layout-parity / broken-interaction / data-bound-honesty / mockup-stale) and instructs it to file ONE spillover slice per finding (not bundle multiple findings into one slice).
- [ ] **AC-3**: Each agent receives a read-only `makeReadOnly(page)` Playwright session per slice 178's pattern; no mutating operations allowed during the audit.
- [ ] **AC-4**: Each agent writes a per-page audit log at `docs/audit-log/204-page-audit-<page-slug>.md` containing: (a) page URL audited, (b) mockup HTML path, (c) screenshot reference (commit-relative path under `docs/audit-log/204-screenshots/`), (d) ordered list of findings with category, severity guess, spillover slice number, brief description.
- [ ] **AC-5**: Aggregate audit log at `docs/audit-log/204-aggregate.md`: a markdown table listing all spillover slices filed across the fleet, with columns `slice_number | page | category | severity-guess | spillover_title`. Sortable / scannable by the maintainer for batch-prioritization.
- [ ] **AC-6**: All spillover slices file as proper docs/issues/<NNN>-<slug>.md per `Plans/prompts/04-per-slice-template.md`. Each spillover cites #204 as parent in its Narrative.
- [ ] **AC-7**: Pre-commit scrub: `grep -rE "(Bearer [A-Za-z0-9._-]+|atlas_session=[A-Za-z0-9._-]+)" docs/audit-log/204-*.md` returns nothing. Auto-enforced by a one-line pre-commit local-hook addition (or grep run in the slice's CI delta — see AC-12).
- [ ] **AC-8**: Audit runs against the well-known dev-seed dataset (`bash deploy/docker/seed.sh` or whichever the audit harness uses). NO real-tenant screenshots or content in any report.
- [ ] **AC-9**: Fleet concurrency capped at 4 in-flight agents. Document the choice in the decisions log if the cap is altered.
- [ ] **AC-10**: Each agent's prompt MUST instruct it to NOT fix any finding inline. The slice's value materializes through the spillovers. Inline fix-attempts are an immediate slice-merge blocker.
- [ ] **AC-11**: When a finding's underlying bug is the v1.14.0 500-error class (i.e. the page can't even render), the agent's spillover slice citation is sufficient — do NOT debug the cause. The maintainer files the 500-class diagnostic slice separately.
- [ ] **AC-12**: Pre-commit hook update (or CI step): the scrub from AC-7 runs on every commit that touches `docs/audit-log/204-*.md`. Failure blocks the commit.
- [ ] **AC-13**: Pre-commit clean, DCO sign-off, Co-Authored-By trailer per CLAUDE.md.
- [ ] **AC-14**: CHANGELOG entry under `Changed` (not `Features` — the audit is a doc/process artifact). One bullet pointing at #204 + the aggregate log + the count of spillovers filed.
- [ ] **AC-15**: Decisions log at `docs/audit-log/204-fleet-orchestrator-decisions.md` capturing: (D1) fleet concurrency choice + rationale; (D2) which mockup pages got reduced scrutiny vs full audit; (D3) how findings were de-duplicated when multiple agents found the same bug; (D4) the slice 178-vs-204 boundary (heuristic harness vs human parity pass); (D5) CI-delta scan results.

## Constitutional invariants honored

This slice is read-only and produces only documentation + spillover slice files. It does NOT touch any constitutional invariant directly:

- **RLS / tenancy (#6)**: not touched. Audit runs against dev-seed only; no tenant context is mutated.
- **Audit-log integrity (#2)**: not touched. The fleet's "audit log" files live at `docs/audit-log/204-*.md`, distinct from the platform's append-only domain audit-log tables. The naming overlap is intentional (uniform `docs/audit-log/` convention for slice decisions) but the data layer is unaffected.
- **AI-assist boundary**: this slice IS the AI-assist boundary's allowed surface — using AI agents to draft findings + propose slices. Each spillover slice goes through normal review before merge per the boundary's "human approval per artifact" rule.

## Canvas references

- `Plans/canvas/01-vision.md` — "deliberately self-host-able, vendor-lock-in-free" positioning depends on the UI actually working
- `Plans/canvas/10-roadmap.md` — v1 binary success test is "does the user run their next SOC 2 audit out of security-atlas?" — that test cannot be evaluated honestly if the UI doesn't render

## Dependencies

- **#178** (UI honesty audit harness) — merged. This slice reuses its `makeReadOnly(page)` pattern + audit-harness Playwright project at `web/e2e-audit/`.
- **#154** (settings page audit) — merged. Reference for finding-classification + spillover-slice format.
- **#203** (dark-mode stylesheet wiring) — `ready` (filed simultaneously). The audit fleet MAY surface dark-mode-related findings; those are de-duped against slice 203's scope, not re-filed.

**Operational blocker (not a slice dep, but a hard runtime gate):**

Per maintainer's 2026-05-22 surface: "most components return 500 on the deployed v1.14.0 release". The audit fleet cannot meaningfully report on pages that 500 before rendering. The slice's implementing engineer MUST verify that the local dev build renders the audited pages without 500s before dispatching the fleet. If the local build also 500s, STOP and surface the diagnostic to the maintainer (the 500 class is its own slice; the audit is meaningless until it's resolved). This is operational, not a dep on a merged slice — the local dev build is expected to work.

**Likely root cause of the v1.14.0 500 class** (to inform the maintainer's diagnostic slice, NOT to fix in this slice): auth-substrate-v2 (slices 187–192 + spillovers 196/197/198/201) and the final bearer retirement (197) require the deployment to have: (a) OIDC IdP configured + reachable (`ATLAS_OIDC_*` env), (b) `ATLAS_KEYSTORE_PATH` writable by the atlas process, (c) `ATLAS_JWT_ISSUER` set, (d) first-install OIDC bootstrap run successfully (slice 198). If any of those four are misconfigured on the user's deployment, every authenticated endpoint returns 500. The maintainer should file a separate diagnostic slice (suggested title: "v1.14.0 deployment 500-error diagnosis: auth-substrate-v2 config gap").

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT fix any finding inline. Every finding is a spillover slice; the audit is read-only.
- **P0-A2**: DOES NOT touch any production code (`internal/*`, `cmd/*`, `web/app/*` except documentation).
- **P0-A3**: DOES NOT touch any schema, migration, or test fixture (the slice writes only to `docs/audit-log/204-*.md` and `docs/issues/<NNN>-*.md`).
- **P0-A4**: DOES NOT debug the v1.14.0 500-error class. That's a separate slice the maintainer files; this slice's per-agent finding category for "page 500s" surfaces the symptom and stops there.
- **P0-A5**: DOES NOT commit any artifact containing `Bearer <token>` substring, `atlas_session=<value>` cookie value, or real-tenant screenshot. AC-7's scrub is the enforcement.
- **P0-A6**: DOES NOT spawn more than 4 concurrent audit agents (AC-9). Per-machine resource ceiling.
- **P0-A7**: DOES NOT bundle multiple findings into one spillover slice. One finding → one slice (AC-2).
- **P0-A8**: DOES NOT use vendor-prefixed test fixture tokens; neutral `test-*` only.

## Skill mix

- `web/e2e-audit/` harness (slice 178)
- Playwright trace + screenshot capture
- `/idea-to-slice` skill (or its inline equivalent) per spillover
- markdown / table formatting for the aggregate log
- per-agent prompt engineering (the orchestrator-engineer writes the audit-Agent prompt)

## Notes for the implementing agent

This slice is unusual: the slice itself ships only docs (the aggregate audit log + decisions log + N spillover slice files). The "implementation" is dispatching the fleet, collecting + de-duping findings, filing spillovers, and writing the aggregate.

**The agent fleet prompt template** (each per-page audit-Agent gets this with `<PAGE>` and `<MOCKUP>` substituted):

```
You are auditing the security-atlas /<PAGE> page against its mockup at <MOCKUP>.

Workflow:
1. Read the mockup HTML at Plans/mockups/<MOCKUP>
2. Start the dev build (`cd web && npm run dev` if not already running)
3. Navigate to http://localhost:3000/<PAGE> with the well-known dev-seed credentials
4. Compare the live page to the mockup on four axes:
   (i) Layout / chrome parity — does the header / sidebar / footer match?
   (ii) Broken interactions — buttons, links, forms, row-clicks
   (iii) Data-bound surfaces that lie — does the live page show what the mockup claims?
   (iv) Mockup-stale text — does the mockup reference features that don't exist yet?
5. For each finding, file a NEW slice at docs/issues/<NEXT>-<slug>.md per Plans/prompts/04-per-slice-template.md format, citing #204 as parent
6. Write your per-page report to docs/audit-log/204-page-audit-<page-slug>.md
7. Do NOT fix anything inline — every finding is a spillover slice
8. Do NOT commit any artifact containing a Bearer token, atlas_session cookie value, or real-tenant screenshot
9. If the page itself returns 500 before rendering, file ONE finding noting that and stop auditing — do not debug the cause

Return: list of spillover slice numbers filed + path to your per-page report.
```

The orchestrator-engineer's job after fleet dispatch:

1. Collect each agent's spillover-slice list
2. De-duplicate (multiple agents may surface the same bug — pick the most specific spillover and close the others as duplicates BEFORE this PR is opened)
3. Write the aggregate at `docs/audit-log/204-aggregate.md` (markdown table)
4. Write the orchestrator decisions log at `docs/audit-log/204-fleet-orchestrator-decisions.md`
5. Open ONE PR containing: the aggregate, the decisions log, all per-page reports, and all spillover slice docs. The spillovers' canonical `_STATUS.md` rows are added in the same PR (each row starts at `ready` or `not-ready` per the dep analysis the spillover-filing did).

**Fleet concurrency**: use the parallel `Agent` tool invocation pattern. Up to 4 in-flight at a time. If a per-page audit Agent stalls or fails (Anthropic API overload, browser hang, etc.), the orchestrator-engineer respawns it once. If it fails twice, escalate per slice 05's recovery playbook.

**The 500-error class**: when an agent reports its page can't render because of a 500, the spillover slice is filed but its severity-guess is "DEFER until 500 class resolved". This is the audit's signal to the maintainer that the v1.14.0 deployment's underlying problem is bigger than UI polish.

Provenance: filed 2026-05-22 via `/idea-to-slice` after maintainer surfaced both (a) dark-mode wiring missing on deployed release (→ slice 203) and (b) prior audits not exhaustively covering the UI. The 500-error class surfaced mid-filing is logged here but its diagnostic is a separate slice.

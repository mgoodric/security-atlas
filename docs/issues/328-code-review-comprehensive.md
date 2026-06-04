# 328 — Comprehensive code review via voltagent-qa-sec:code-reviewer

**Cluster:** Quality
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Runs `voltagent-qa-sec:code-reviewer` against the full security-atlas
codebase to surface correctness bugs, simplification opportunities,
reuse gaps, and efficiency wins across all three language surfaces.
The v1 backlog is fully merged (69/69 v1 slices on `main`); the
code now has ~250 merged slices of accreted patterns. This audit
asks: where has the codebase drifted from its own conventions, where
are similar patterns duplicated, where can we reuse instead of
reinvent?

**Audit surface.** Full code-review pass across:

- **Go backend** — `internal/**` and `cmd/**`. Hot spots: `internal/api`
  (~30 packages of HTTP handlers), `internal/auth/**` (OAuth AS spine
  per slices 187-198), `internal/db`, `internal/evidence`, `internal/eval`,
  `internal/ucf`, `internal/scope`, `internal/risk`, `internal/policy`,
  `internal/audit`, `internal/board`. Each `cmd/*` binary entrypoint
  (`atlas`, `atlas-cli`, `atlas-oscal`, atlas-edge variants).
- **TypeScript frontend** — `web/app/**` (Next.js 16 App Router routes),
  `web/components/**` (shadcn/ui + custom), `web/lib/**` (BFF
  utilities including `lib/api.ts` and `lib/api/bff.ts`),
  `web/e2e/**` and `web/e2e-audit/**` Playwright harnesses.
- **Python OSCAL bridge** — `oscal-bridge/**` (compliance-trestle wrapper
  exposed via gRPC).

**Review focus** (per the `voltagent-qa-sec:code-reviewer` agent's
charter):

1. **OWASP top 10** — sanity-check across all three surfaces. Note: a
   security-specific deep audit runs separately at slice 327 (the
   `security-auditor` agent). This slice's OWASP coverage is the
   "code-reviewer perspective" — pattern smells visible from a
   developer's read.
2. **Reuse / simplification / efficiency** — duplicated helpers,
   utility-function drift, hand-rolled loops that have stdlib
   equivalents, repeated SQL where `sqlc` codegen could consolidate.
3. **Correctness bugs** — race conditions, missing error returns,
   nil-deref risk, off-by-one, time/timezone bugs, context-cancellation
   gaps, leaked goroutines.
4. **Convention drift** — packages that don't match the established
   pattern set (audit-log discipline, RLS-context setting, sqlc query
   placement, vitest test file layout, Playwright preconditions).

**Why now:** the codebase has crossed the threshold where pattern
drift is more expensive than catching it. Pre-v1-binary-test cleanup
buys headroom before SOC-2-by-self-host operators read the code.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 2/12.

**Disposition:** read-only review + follow-up-slice fan-out.

## Threat model

Code-review-only slice. STRIDE pass on the review activity itself:

- **S (Spoofing):** No new auth surface. CLEAN.
- **T (Tampering):** Read-only; AC-1 enforces no code changes in this
  slice's PR. Fixes are downstream slices.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/328-code-review-comprehensive-decisions.md`.
- **I (Information disclosure):** Code review output names file paths
  and snippets. None of these are confidential — the repo is destined
  for OSS publication. CLEAN with the standard "no production data in
  examples" caveat (demo seed only if the agent invokes the running
  binary at all).
- **D (Denial of service):** Run-once activity. CLEAN.
- **E (Elevation of privilege):** Reviewer operates with developer-level
  read access — no production credentials, no super-admin tokens. AC
  enforces.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:code-reviewer` agent runs against
      each of the three language surfaces (Go backend, TS frontend,
      Python OSCAL bridge) at the current `main` HEAD.
- [ ] **AC-2.** Each finding recorded in
      `docs/audit-log/328-code-review-comprehensive-decisions.md` with:
      short title · severity (Critical / High / Medium / Low /
      Informational) · category (OWASP | reuse | simplification |
      efficiency | correctness | convention-drift) · location
      (file/path:line range) · one-line disposition.
- [ ] **AC-3.** For each High and Critical finding, a follow-up slice
      is filed via `/idea-to-slice` in the same session. Slot numbers
      appended to the decisions log entry.
- [ ] **AC-4.** Medium findings: prefer **consolidated tracking
      slices grouped by category** (one slice for "reuse cleanups",
      one for "convention-drift cleanups", etc.) — the diff fan-out
      of individual medium-fix slices is unwieldy for this kind of
      review. Engineer's call documented in the decisions log.
- [ ] **AC-5.** Low / Informational findings: documented in the
      decisions log only.
- [ ] **AC-6.** No code modified in this slice's PR. Diff contains
      ONLY: `docs/issues/328-code-review-comprehensive.md`,
      `docs/issues/_STATUS.md`,
      `docs/audit-log/328-code-review-comprehensive-decisions.md`.
- [ ] **AC-7.** The decisions log includes a "Surface coverage" table:
      one row per top-level package (`internal/api/*`, `web/app/*`,
      etc.) with whether the agent visited it and the finding count.
- [ ] **AC-8.** Findings that cross with slice 327's security audit
      (any OWASP-category finding) are noted with a cross-reference
      so the maintainer can dedupe at follow-up-filing time.
- [ ] **AC-9.** `pre-commit run --files` passes for the three changed
      files at PR-time.

## Constitutional invariants honored

- **Simplicity gate (Article VII).** This audit's reuse + simplification
  findings directly serve the gate. The whole slice exists to find drift
  away from it.
- **Anti-abstraction gate (Article VIII).** Convention-drift findings
  flag unnecessary wrapper layers that should be inlined.
- **Survive third-party security review (canvas §6).** OWASP-category
  findings serve the same goal as slice 327; this slice catches the
  ones a code-reviewer notices that a security-specialist might miss.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — established conventions per
  language surface
- `Plans/canvas/01-vision.md` §6 — survive third-party review

## Dependencies

- **#069** (testing discipline) — `merged`. Provides the test
  surfaces the agent reasons against.
- **#178** (UI honesty audit harness) — `merged`. The frontend
  audit harness pattern is reused conceptually.

## Anti-criteria (P0 — block merge)

- **P0-328-1.** Does NOT bundle multiple High/Critical findings into
  one follow-up slice. One finding = one slice (tracer-bullet).
- **P0-328-2.** Does NOT auto-merge this slice's PR or any follow-up.
- **P0-328-3.** Does NOT modify code as part of this slice. AC-6
  enforces.
- **P0-328-4.** Does NOT operate on production tenant data — demo
  seed only if any runtime introspection is needed.
- **P0-328-5.** Does NOT cross into the territory of slice 327
  (security-specific deep dive) — when an OWASP-category finding
  surfaces, cross-reference it; don't expand this slice's scope to
  "security audit too".
- **P0-328-6.** Does NOT cross into the territory of slice 330 (QA
  strategy gap analysis) — convention-drift findings are in scope;
  "should the test pyramid look different" is not.
- **P0-328-7.** Does NOT touch CLAUDE.md, canvas, mockups, or any
  code surface. Doc-only PR.

## Skill mix

- `voltagent-qa-sec:code-reviewer` — the named audit agent
- `/idea-to-slice` — for filing follow-ups
- Standard read/grep tooling — surface enumeration before agent run

## Notes for the implementing agent

**Run-order suggestion** (parallelize within a surface, serialize
across surfaces):

1. **Go surface first.** Largest by far; convention set is most
   established. Per-package walk through `internal/api/*` first
   (HTTP handlers — likely the densest concentration of reuse
   opportunities), then `internal/auth/*` (most-recent code; pattern
   drift most likely), then the rest.
2. **TS surface second.** Smaller. Focus on `web/app/**` route
   handlers (BFF surface) and `web/lib/api/bff.ts` (the proxy
   pattern). Look for inconsistent error handling shapes — the
   BFF return-type contract has shifted over slices 187-211.
3. **Python surface third.** Smallest. `oscal-bridge/**` is mostly
   wrappers around compliance-trestle; check for redundant
   compatibility layers.

**Severity rubric (same as slice 327, restated):**

- **Critical** — Real bug with user-visible impact in production
  (data corruption, auth bypass, etc).
- **High** — Bug with limited user impact OR an OWASP top-10
  finding (cross-references slice 327).
- **Medium** — Convention drift causing maintainability burden,
  reusable helper not reused, simplification with clear payoff.
- **Low** — Style / cosmetic / nice-to-have.
- **Informational** — Observation without an obvious action.

**Bundling guidance for Medium findings.** Unlike slice 327 (where
one-finding-one-slice is dogma), this slice's Medium findings often
look like "the same drift across 8 packages" — bundling them into a
single "reuse cleanup: HTTP error response shape across `internal/api/*`"
slice is more useful than 8 separate slices. The engineer judges per
finding-cluster.

**Cross-reference with slice 327.** If the code reviewer surfaces
something that looks like an OWASP top-10 issue (e.g. "this handler
doesn't validate the tenant_id claim"), cross-reference it in the
decisions log with a note "candidate dedupe with slice 327 finding
#NN". The maintainer can decide which slice "owns" the follow-up at
review time.

**Audit log filename:**
`docs/audit-log/328-code-review-comprehensive-decisions.md`

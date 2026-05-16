# Decisions log — Slice 071 (Repo cleanup audit + in-place updates)

This is a JUDGMENT slice (per `Plans/prompts/04-per-slice-template.md` "Slice types" + the slice's `Type: JUDGMENT` frontmatter). The slice's deliverables — the audit report at [`docs/audits/2026-Q2-repo-cleanup.md`](../audits/2026-Q2-repo-cleanup.md), in-place doc fixes, and the follow-on deletion-candidates slice at [`docs/issues/096-repo-cleanup-deletions.md`](../issues/096-repo-cleanup-deletions.md) — required the engineer to make per-finding KEEP-vs-FIX-IN-PLACE-vs-DEFER calls + the AC-6 `_INDEX.md` policy call. The major judgment calls are recorded here.

## Decisions made

### D1 — `_INDEX.md` policy paragraph instead of row backfill (HIGH confidence)

**Decision:** add an `## Index policy` header paragraph to `docs/issues/_INDEX.md` documenting that the index is the **v1 spec snapshot** frozen at backlog-time (58 originally-scoped + 11 v1-time additions = the 58 rows currently listed under topological order); post-v1 slices (059 onward) deliberately do not appear in this index. The live merge tracker is `_STATUS.md`.

**Alternatives considered:**

- Backfill 37 rows for slices 059-095. Rejected: would conflate "what v1 was supposed to be" with "what has happened since v1 completed" — two distinct questions the maintainer asks separately. The historical-record purpose of the index would be eroded; the rolling-record purpose is already filled by `_STATUS.md`. Memory note "slices 059-064 are deliberately not in the index per the parallel-batch convention" reinforces this is the established convention.
- Leave the index untouched (rely on the memory note alone). Rejected: future contributors don't have access to the maintainer's MEMORY.md; the policy needs to be visible in the index itself.
- Backfill the 11 v1-time additions but skip 059+ post-v1. Rejected: the 11 v1-time additions ARE already in the index (slices 050-058 + 051 hotfix). The actual question is the 059+ row backfill.

**Confidence: HIGH.** Pattern-matched to the maintainer's existing convention (per the memory note). The policy paragraph is the lowest-touch + most defensible call; if future iteration disagrees, adding rows later is a one-time mechanical edit.

### D2 — `staticcheck` deferred to a future targeted cleanup slice (HIGH confidence)

**Decision:** the slice doc's category 13 names `staticcheck` (and the `unused` analyzer) as desirable signal alongside `go vet`. `staticcheck` is NOT in the repo's default linter mix (`.golangci.yml`'s enabled-linters do not include it; no `tools.go` blank-import; no `bin/staticcheck` in the repo). Installing it as a one-shot is feasible but expands scope past "audit current state". Decided: run `go vet ./...` only (silent — clean) + manual spot-check of `internal/` package list. Record the deferral.

**Alternatives considered:**

- Install `staticcheck` as a dev dependency in `go.mod`'s `tools.go`. Rejected: a new tool affects every contributor's local setup AND every CI run; that's a separate slice with its own review (tooling additions touch the spine).
- Run `go install honnef.co/go/tools/cmd/staticcheck@latest` ephemerally in the slice. Rejected: the slice ships a report; the report would name findings that no other contributor can reproduce without the same ephemeral install — un-cite-able.
- Use the existing `golangci-lint run ./...` (which does include some `staticcheck` rules). Considered + ran: clean. The signal that matters (zero net-new lint findings introduced by recent slices) is already provided by the existing CI lint job.

**Confidence: HIGH.** Scope discipline call; the absence of `staticcheck` is not a finding — it's a deliberate non-adoption that future iteration can revisit on its own merits.

### D3 — `web/` dead-code scan deferred (HIGH confidence)

**Decision:** category 14 names `tsc --noEmit` + `eslint` + `knip` as desirable. Running them requires `npm install -w web` (large Sharp + Playwright transitive trees) in a clean worktree — a multi-minute network operation. Decided: defer the local scan; rely on CI's `Frontend · vitest` + `Frontend · lint` (slice 078) signal, both green on this branch since claim-stake.

**Alternatives considered:**

- Run `npm install -w web` + the full scan. Rejected: the install would dominate the slice's runtime and yield findings that may or may not represent real dead exports vs IDE-completion code paths. The signal-to-cost ratio is poor for a one-shot audit.
- Add `knip` as a `web/devDependency`. Rejected: the slice doc explicitly flags this as a judgment call ("if cleanly installable as a `web/devDependency` — judgment call, log it"). New devDep should be evaluated on its own merits in a targeted future cleanup slice.
- Skip the category entirely without comment. Rejected: documenting the deferral is the value — future cleanup slices can pick up exactly the deferred surface.

**Confidence: HIGH.** Matches D2's scope-discipline logic. The CI-green signal is sufficient for "audit current state"; deeper dead-code analysis is its own targeted slice if it ever surfaces real noise.

### D4 — `RESOLVED` blockquote format mirrors items 4/13/19/20 verbatim (HIGH confidence)

**Decision:** the 5 new `RESOLVED` blockquotes added to `Plans/canvas/11-open-questions.md` (items 1, 3, 8, 16, 18) follow the exact format established by the pre-existing resolved items: blockquote-start, `**RESOLVED YYYY-MM-DD** (at slice NNN / context)`, decision-summary paragraph, optional canvas-link tail.

**Alternatives considered:**

- Introduce a new format (e.g., a top-line "Status: resolved" header). Rejected: the existing convention is what the maintainer / future contributors already scan for; introducing a parallel format reduces signal.
- Mark items "RESOLVED" only when the resolving slice itself has cited the OQ item by number. Rejected: many resolved items (the Apache 2.0 license call, the AI-assist boundary codification) are evidenced in artifacts other than the slice doc itself (LICENSE file, CLAUDE.md "AI-assist boundary (hard)"). Demanding a per-slice citation would leave clearly-resolved items unmarked.

**Confidence: HIGH.** Verbatim convention match is the lowest-risk call.

### D5 — Item #16 is `PARTIALLY RESOLVED`, not `RESOLVED` (HIGH confidence)

**Decision:** OQ #16 (AI inference backend default) gets `**PARTIALLY RESOLVED**` rather than `**RESOLVED**` because the backend choice (Ollama default + cloud opt-in per-tenant) IS locked in CLAUDE.md, but the specific local model selection (e.g., `llama3.1:8b-instruct-q5` baseline named in the CLAUDE.md tech-stack table) is a tentative pre-implementation pin that the first v2 AI-assist feature slice will need to validate against real benchmarks.

**Alternatives considered:**

- Mark it `RESOLVED` and treat the model-selection caveat as future iteration. Rejected: there's a meaningful difference between "decision locked" and "decision locked + production-validated"; AC-5's mandate to mark items resolved during v1 should reflect that nuance accurately. The format `RESOLVED` carries an implicit "final" tone that the model-selection sub-decision does not yet warrant.
- Leave it unmarked and let the v2 AI slice resolve it. Rejected: the architectural decision (Ollama default + cloud opt-in) IS done; failing to mark it loses signal.

**Confidence: HIGH.** Precision matters here — the maintainer will be the one re-reading this when an AI-assist slice lands.

### D6 — ADR Status format: append `· Honored (verified ...)` to existing `Accepted` (HIGH confidence)

**Decision:** all 3 ADRs already had `**Status:** Accepted` headers (matching the typical ADR template). Slice 071's AC-9 asks for "Status: Honored / Superseded by slice NNN / Partial — see §X" headers. Resolved by appending — `**Status:** Accepted · Honored (verified 2026-05-15 by slice 071 audit — <specific verification evidence>)` — rather than replacing the original `Accepted` (which is an immutable historical state) or restructuring the header.

**Alternatives considered:**

- Replace `Accepted` with `Honored`. Rejected: `Accepted` is the ADR's original decision state; `Honored` is a post-implementation observation about whether the code matches the decision. They are separate facts; both should be recorded.
- Add a second line `**Implementation status:** Honored ...`. Rejected: more verbose; introduces a new field name that other ADRs don't follow.
- Add a footer "Audit trail" section. Rejected: AC-9 explicitly asks for a one-line Status header just below the title.

**Confidence: HIGH.** Lowest-touch interpretation of AC-9 that preserves the ADR's history.

### D7 — e2e preamble rewrite preserves test-body comments verbatim (HIGH confidence)

**Decision:** the 5 e2e preambles got fresh top-comment blocks (post-slice-069 reality) but the body of each test (the commented `await page.goto(...)` and `expect(...)` lines) is preserved verbatim. The trailing "Per the preamble above: assertions are deliberately commented pending the slice-082 seed-data harness." note replaces the redundant slice-069 inline note that used to live between the preamble and the test bodies.

**Alternatives considered:**

- Uncomment the assertions. Rejected: the assertions depend on seed data the test harness can't yet provide (slice 082 is `not-ready`); uncommenting would produce immediate test failures, expanding scope to "fix the quarantine".
- Delete the commented assertions entirely. Rejected: P0-A1 — this slice does not delete; the commented assertions ARE the reviewable contract the slice 040/041/042/056/060 authors wrote.
- Rewrite the commented assertions into a different syntax (e.g., `test.skip`). Rejected: that's a code change, P0-A2 forbids it (production code touch outside the preamble comment).

**Confidence: HIGH.** The preamble update is the literal scope of AC-8; the test bodies stay as the contract.

### D8 — Stale worktree count: actual 49, slice doc said "~45" (LOW judgment, HIGH confidence)

**Decision:** the slice doc's narrative said "~45 directories under `../security-atlas-NNN/`"; the actual `git worktree list` from this worktree shows 49 stale worktrees (entries 007-063 + 074 + 079-081). All 49 land in the follow-on slice 096; no slice-doc edit, because the "~45" was a maintainer estimate, not a load-bearing fact.

**Alternatives considered:**

- Edit the slice doc to correct the count from `~45` to `49`. Rejected: P0-A6 forbids modifying `Plans/prompts/*` and similar meta-spec surfaces; the slice doc itself is the meta-spec for this slice. Beyond that, the estimate was correctly hedged with `~` — it's not "wrong", it's an order-of-magnitude estimate that the actual count matches closely.
- Round the actual count to "~50" in the audit report. Rejected: the audit report is the precise artifact; precision wins over rounding when the actual count is known.

**Confidence: HIGH** (the action is mechanical: list the worktrees in slice 096). **LOW judgment** (the choice between "fix the doc" vs "respect the meta-spec untouched" is trivial).

### D9 — No spillover slices filed (HIGH confidence)

**Decision:** no out-of-scope finding surfaced during the audit that warranted a separate slice filing. The 17th surface considered (global TODO/FIXME scan) overlapped entirely with categories already covered (10 + 7).

**Alternatives considered:**

- File a "TODO/FIXME scan" spillover slice as cover-the-bases. Rejected: would be pure duplication of the work already covered by categories 7 + 10 + 11. The slice doc's "scope discipline guardrails" warns against unnecessary scope expansion.
- File a "tsc + eslint + knip web dead-code scan" spillover slice (covering D3's deferred surface). Rejected as premature: the CI-green signal is sufficient for current-state confidence; filing a slice now commits the maintainer to acting on it before knowing whether there's anything to act on. Better to file IF/WHEN real noise surfaces.

**Confidence: HIGH.** Discipline-driven non-action; "no spillover" is the correct outcome for a clean audit.

### D10 — Follow-on slice number: 096, not 091/092/093 (HIGH confidence)

**Decision:** the follow-on deletion-candidates slice gets number 096 (next available after the current max 095). Per slice doc AC-3 + "Spillover-as-slice directive".

**Alternatives considered:**

- Re-use an existing un-merged slice number. Rejected: per Conventional Commits + the per-slice template, slice numbers are immutable identifiers.
- Use 091, 092, 093, or 094 (which exist as new v2 backlog items, see commits `d7b1b62`, `826b929`, `26a33d6`, `61ee8df`). Rejected: those slices have their own scope; reusing the number would conflict with the actual filed work.

**Confidence: HIGH.** Mechanical numbering per the standing convention; the `ls docs/issues/[0-9]*.md | sed -E 's|.*/([0-9]+).*|\1|' | sort -n | tail -1` calculation matches.

## Revisit once in use

- **D1 (`_INDEX.md` policy):** if a future contributor reasons that the index SHOULD reflect post-v1 work (e.g., a "v2 backlog" parallel section), the policy paragraph is the seam to revise. Adding rows mechanically would be cheap; the load-bearing call is whether the maintainer wants the index to be a "v1 historical record" or a "v1+v2 rolling backlog". Deferred to the maintainer.
- **D2 (`staticcheck`):** when a future slice needs the deeper signal (e.g., a refactor that introduces unused exports), file a targeted "add staticcheck to golangci-lint" slice. Until then, `go vet` + golangci-lint defaults are sufficient.
- **D3 (`web/` dead-code scan):** same calculus as D2 — file a targeted `knip` adoption slice if/when a refactor or version bump introduces real dead-export noise. Pre-condition: actual observed noise, not speculation.
- **D8 (worktree count):** the count will continue to grow as new slices are batched; slice 096 captures the current snapshot, but future deletion sweeps will need to re-list. Pattern: each "stale worktrees" cleanup sweep gets its own slice and re-lists fresh.
- **D5 (PARTIALLY RESOLVED for OQ #16):** when the first v2 AI-assist feature slice ships with a benchmark + locked model selection, flip OQ #16 from `PARTIALLY RESOLVED` → `RESOLVED`.
- **D6 (ADR Status format):** as new ADRs are written, the append-style `Accepted · Honored (...)` format should propagate. Worth codifying in the slice template or an ADR-format ADR if a fourth ADR lands without it.

## Confidence summary

10 of 10 decisions HIGH confidence. The substantive judgment calls were:

1. **D1 — `_INDEX.md` policy vs row backfill** (the load-bearing AC-6 call). Pattern-matched to the established memory-note convention; the policy paragraph captures the intent at lower cost than a row backfill.
2. **D2 + D3 — staticcheck + knip deferrals.** Both grounded in scope discipline: the slice's binary success-test is "16-category audit + in-place fixes shipped"; not "every deferred linter installed". The deferrals are explicit and re-actionable.
3. **D5 — `PARTIALLY RESOLVED` precision on OQ #16.** Maintainer signal hygiene — partial-resolution items are distinct from full-resolution items.
4. **D7 — preamble-only edit, test bodies preserved.** Tightest interpretation of AC-8; preserves the slice 040/041/042/056/060 contract verbatim.

No decision was a constitutional conflict; no escalation surfaced. The audit + in-place fixes + follow-on slice ship as one PR.

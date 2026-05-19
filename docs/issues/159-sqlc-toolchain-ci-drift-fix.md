# 159 — Resolve sqlc-toolchain CI binary drift (slice 109 follow-on)

**Cluster:** Infra
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**WHY.** Slice 109 pinned the sqlc toolchain version (`SQLC_VERSION := "v1.31.1"` in `justfile`) so contributors regenerate identically. Its AC-5 added an informational CI job (`Go · sqlc generate diff`) marked `continue-on-error: true`, with anti-criterion P0-A3 explicitly deferring promotion to required-checks "until the install paths are proven byte-identical across both" (the brew-distributed binary the slice 109 author used to commit vs the `go install` path CI takes).

That deferral has now bitten. Slice 153's PR #330 (logo standalone fix, 2026-05-18) was the **first** PR whose path-filter triggered the real sqlc-drift job rather than the docs-only stub — and the job failed. CI's `sqlc generate` reverts slice 109's deliberate hand-narrow of two columns in `internal/db/dbx/policies.sql.go` (and a similar drift in `scf_anchors.sql.go`):

```diff
-	// AckDenominator + AckNumerator are hand-narrowed from sqlc's
-	// inferred `interface{}` back to `pgtype.Int8` (slice 109).
-	// Why: sqlc v1.31.1 cannot type a `CASE WHEN ... THEN (scalar
-	// subquery)::bigint END` expression — it falls back to
-	// `interface{}` and the slice-107 handler depends on the
-	// `.Valid` / `.Int64` API ...
-	AckDenominator pgtype.Int8 `json:"ack_denominator"`
-	AckNumerator   pgtype.Int8 `json:"ack_numerator"`
+	AckDenominator interface{} `json:"ack_denominator"`
+	AckNumerator   interface{} `json:"ack_numerator"`
```

This matters because slice 107's `ListPoliciesWithAckRate` handler (`internal/api/policies/list.go`) consumes `AckDenominator.Valid` and `.Int64` — the `pgtype.Int8` API. Reverting to `interface{}` would compile-fail on `main`, but doesn't, because the hand-narrow lives in the **committed** tree on main and only the **CI-regenerated** copy diverges. The hand-narrow is therefore in a metastable equilibrium: it stays correct as long as no human runs `sqlc generate` locally and commits the result.

The drift was masked for the entire batch-23-through-batch-58 window because every prior CI run hit the docs-only stub via `dorny/paths-filter@v4`. The first real run (slice 153) exposed it. The `continue-on-error: true` shield kept the merge unblocked, which is precisely the safety-net behavior slice 109 designed for — but the bug now needs to be resolved.

**WHAT.** Pick one of the five resolution paths below (this is the JUDGMENT decision), implement it, prove the slice 107 handler still works at runtime, **then** promote `Go · sqlc generate diff` to `required-checks` in `.github/branch-protection.json` (the goal slice 109 anti-criterion P0-A3 explicitly deferred). Drop `continue-on-error: true` from the workflow once stable.

**SCOPE DISCIPLINE — what's deliberately out:**

- No new sqlc queries. No new handlers. No new endpoints.
- No change to slice 107's API surface (`/v1/policies` response shape stays exactly `ack_denominator: number | null`, `ack_numerator: number | null`).
- The `scf_anchors.sql.go` drift is in scope (same root cause), but the slice ships even if only `policies.sql.go` lands clean — the second file is a same-root-cause repeat, not a separate fix. If the chosen approach handles both for free, ship both; if `scf_anchors` needs a separate query rewrite (Option C), that's a spillover slice (160-series, file via `/idea-to-slice`).
- The five resolution-option STRIDE re-evaluations are baked into the decisions log, not the canvas.

## Threat model

**Spoofing.** N/A — no new endpoints, no auth surface changes. The slice touches `.github/workflows/ci.yml`, `.github/branch-protection.json`, and one or two `internal/db/dbx/*.go` files (plus possibly `internal/db/queries/*.sql` if Option C is chosen).

**Tampering.** The drift itself is a class of tampering risk that this slice closes — today, if a contributor's local sqlc binary differs from the committed bytes and they run `sqlc generate` + commit, the codegen file mutates silently and slice 107's handler stops working at runtime (500 on every `GET /v1/policies?include=ack_rate` call). **Mitigation in this slice:** required-checks promotion of `Go · sqlc generate diff` makes this class of drift a CI-blocking PR comment instead of a silent regression. AC-9 verifies the gate actually fails a synthetic drift PR before the slice merges.

**Repudiation.** N/A — no audit-log writes.

**Information disclosure.** N/A — no tenant-scoped data exposure; sqlc codegen is deterministic on schema, not data.

**Denial of service.** Indirect: a runtime break of `ListPoliciesWithAckRate` would degrade `/v1/policies?include=ack_rate` and the dashboard's policy-rate widget. **Mitigation:** AC-5 (integration test using the real handler path, not a unit test on the codegen file) verifies the chosen Option keeps the handler functionally identical against a real Postgres. The handler's existing integration tests in `internal/api/policies/list_integration_test.go` re-run on the regenerated tree.

**Elevation of privilege.** N/A — no role-check changes. The slice does not cross the `atlas_app` / `atlas_migrate` / `atlas_service_account` boundary.

**Anti-criteria added from threat model:** see P0-A4 + P0-A5 below.

## Resolution options (JUDGMENT — engineer picks ONE in decisions log)

| ID  | Approach                                                                                                                                                                                                                                                                                    | Pros                                                                                                                      | Cons                                                                                                                                                          |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A   | **Tighten the install pin.** Replace `go install ...@${SQLC_VERSION}` with a checksummed download from the GH release page (or use the official `sqlc-dev/setup-sqlc-action@<sha>`). Hypothesis: CI's `go install` builds from source at a slightly different commit than the brew tarball. | Smallest diff. No code change. Surgical.                                                                                  | If the root cause isn't actually install-path divergence (it's "interface{} is what v1.31.1 produces for this query regardless of binary build"), wasted day. |
| B   | **Post-generate hook.** Add a `tools/sqlc-postgenerate.sh` that runs after `sqlc generate` (in both `justfile` and the CI workflow) and re-applies the hand-narrows via a deterministic `sed` script. Commit the hand-narrowed bytes as the "after" state.                                  | Decouples sqlc-version drift from the override. Future sqlc upgrades won't re-break.                                      | Requires a script to maintain. Magic — the codegen file is no longer a pure sqlc artifact. Hides what's actually going on from a future reader.               |
| C   | **Query rewrite.** Restructure `internal/db/queries/policies.sql` to use a form sqlc CAN type natively (e.g. wrap the scalar subquery in `COALESCE($1::bigint, 0::bigint)` or use a CTE). Same for `scf_anchors.sql` if same root cause.                                                    | The "honest" fix. sqlc emits `pgtype.Int8` natively; no override needed; the codegen file is what sqlc says it should be. | Requires SQL surgery + verifying the query plan is identical. May need slice-107 handler tweaks if the typed shape changes.                                   |
| D   | **sqlc.yaml `overrides:` block.** Add a per-column override in `sqlc.yaml` mapping the offending columns to `pgtype.Int8`. The proper sqlc-native solution for "I know better than the inferencer."                                                                                         | Lives in the sqlc config, not in handwritten Go. Future-sqlc-version-safe.                                                | Was tried in slice 109 and "broke other things" (per the comment in policies.sql.go) — re-investigate what specifically broke and whether it's fixable.       |
| E   | **Accept divergence; refactor handler.** Promote sqlc-drift to required-checks as-is. Update slice 107's handler to work with `interface{}` (lose the `.Valid` / `.Int64` ergonomics; replace with type-assertion + nil-check).                                                             | No CI changes. No codegen-side hacks.                                                                                     | Loses runtime type-safety at the handler boundary. The handler becomes uglier. Spreads the workaround across two layers (codegen + handler).                  |

**Recommended starting point: Option D** (sqlc.yaml overrides) — it's the sqlc-native solution; the "broke other things" note in slice 109 is from a year ago and the sqlc override system has matured. If D genuinely doesn't work, fall back to Option C (query rewrite) as the second-best honest fix. Options A, B, E are tactical workarounds — record the reasoning if any of those wins.

The decisions log captures which option was picked, the alternatives considered, and the rationale. (See [`docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md`](../audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md), to be written as AC-7.)

## Acceptance criteria

**Resolution + verification:**

- [ ] AC-1: The chosen Option is implemented. `just sqlc-generate` (using the pinned binary) produces byte-identical output to what's committed in `internal/db/dbx/policies.sql.go`. Verify locally with `git diff --exit-code -- internal/db/dbx/`.
- [ ] AC-2: Same verification for `internal/db/dbx/scf_anchors.sql.go`. If the chosen Option fixes both for free, ship both. If `scf_anchors` needs a separate query rewrite (Option C territory), file slice 160 as spillover and document in decisions log.
- [ ] AC-3: `Go · sqlc generate diff` CI job runs the real `sqlc generate` (i.e., path-filter triggers) on this PR and passes (SUCCESS, not SKIPPED).
- [ ] AC-4: `internal/api/policies/list.go` compiles unchanged (Options A/B/D — no handler changes) OR the handler is updated to match the new codegen shape (Option C/E only). The `ListPoliciesWithAckRate` function still answers the same JSON response shape: `{"ack_denominator": number|null, "ack_numerator": number|null}` (verified by AC-5 / AC-6).
- [ ] AC-5: `internal/api/policies/list_integration_test.go` passes against the regenerated tree without modification. Run `go test -tags=integration -p 1 ./internal/api/policies/...` locally and confirm green.
- [ ] AC-6: New integration test added that posts a policy, computes the ack-rate, and asserts the response JSON has the expected `ack_denominator` / `ack_numerator` fields with the right values. (Belt-and-suspenders for AC-5 — pins the runtime behavior in case existing tests don't cover the typed-vs-interface boundary.)

**Decisions log + docs:**

- [ ] AC-7: `docs/audit-log/159-sqlc-toolchain-ci-drift-fix-decisions.md` written. Sections: (1) chosen Option + alternatives considered + why; (2) revisit-once-in-use list (e.g. "if a future sqlc upgrade re-breaks this, the fix is X"); (3) confidence per decision (high/medium/low).
- [ ] AC-8: If Option B was chosen (post-generate hook), the hook script lives at `tools/sqlc-postgenerate.sh` and is invoked by both `justfile:sqlc-generate` and the `.github/workflows/ci.yml` sqlc-drift job. README updated in CONTRIBUTING.md "Local CI parity" section.

**Required-checks promotion:**

- [ ] AC-9: `Go · sqlc generate diff` promoted to required-checks in `.github/branch-protection.json` (`required_status_checks.contexts` list). `continue-on-error: true` removed from the workflow job. Slice 109 anti-criterion P0-A3 is the deferred goal this AC closes.
- [ ] AC-10: Verify the gate works. Open a throwaway test branch with a manual `interface{}` revert of one column in `internal/db/dbx/policies.sql.go`, push to a draft PR, and confirm sqlc-drift now FAILS the PR (red, not yellow). Close the test PR. Screenshot or copy the failure into AC-7 decisions log.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only — never add or remove components as a fix."** Each Option is the minimum scope to close the drift. The slice does not refactor the policies query architecture or the codegen pipeline.
- **CLAUDE.md "Never assert without verification."** AC-5 + AC-6 require the integration test against a real Postgres handler path, not a unit test on the codegen file.
- **CLAUDE.md "First principles over bolt-ons."** Options C and D are first-principles fixes (the query produces typed output OR the config tells sqlc what type to emit); Options A, B, E are bolt-ons and the decisions log must justify them if chosen.
- **Slice 109's "No behavioral change."** The runtime API of `ListPoliciesWithAckRate` does not change. The JSON response shape does not change.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — sqlc + Atlas tech-stack commitment
- Slice 107 (`107-policies-include-ack-rate.md`) — the handler that depends on `pgtype.Int8`
- Slice 109 (`109-sqlc-toolchain-pin.md`) — the original pin + the anti-criterion P0-A3 this slice closes
- `docs/audit-log/109-sqlc-toolchain-pin-decisions.md` — slice 109's decisions log (note the `Why: sqlc v1.31.1 cannot type a CASE WHEN ...` reasoning)

## Dependencies

- #109 — merged. Pin + decisions log already on main. This slice extends slice 109's deferred work.
- #107 — merged. Handler whose API surface this slice protects.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT change the JSON response shape of `GET /v1/policies?include=ack_rate`. The `ack_denominator` / `ack_numerator` fields keep their current name + type + null semantics.
- **P0-A2**: Does NOT introduce a new sqlc query. The slice resolves drift on the two existing files; it does not add coverage.
- **P0-A3**: Does NOT skip the integration test verification (AC-5 + AC-6). A unit test against the regenerated codegen file is insufficient — the failure mode is at the handler runtime boundary.
- **P0-A4** (from threat model — Tampering): Does NOT merge with `Go · sqlc generate diff` still in `continue-on-error: true` mode. The slice's whole point is to close the silent-regression class. AC-9 is mandatory.
- **P0-A5** (from threat model — DoS): Does NOT merge without AC-10 evidence (the synthetic-drift PR proving the gate fails red). A green check on this PR alone is insufficient — the gate must be verified to actually block.
- **P0-A6**: Does NOT bundle the `scf_anchors.sql.go` fix as a separate slice. If the chosen Option handles both files for free, ship both; if `scf_anchors` truly needs different surgery, file slice 160 via `/idea-to-slice` rather than expanding this slice's scope.
- **P0-A7**: Does NOT use vendor-prefixed test fixture tokens (carry-over convention from slice 05).

## Skill mix

- sqlc-specific knowledge (query type inference rules, `overrides:` block syntax, post-generate hook patterns)
- Go integration testing against Postgres (`internal/api/policies/list_integration_test.go` is the canonical pattern)
- GitHub Actions workflow editing (slice 069 is the reference for stub-sibling jobs + path filters)
- `.github/branch-protection.json` editing (slice 069 + slice 099 are the references)
- Decisions-log discipline (slice 109 + slice 121 + slice 152 are recent JUDGMENT-slice examples)

## Notes for the implementing agent

**Provenance:** Surfaced 2026-05-18 during the slice 153 (logo standalone fix) PR session. The drift had been latent for the entire batch-23-through-batch-58 window — every prior PR hit the docs-only `Go · sqlc generate diff` stub via `dorny/paths-filter@v4`. Slice 153's PR was the first to trigger the real job (its file changes — `deploy/docker/web.Dockerfile`, `web/package.json`, new Playwright spec — pushed the path filter into `code=true`).

**The trap not to fall into:** Do NOT run `just sqlc-generate` locally and commit the resulting diff to "fix" the drift. That commits the `interface{}` version which compile-breaks `internal/api/policies/list.go` at runtime via `.Valid` / `.Int64` calls. The committed bytes on `main` (with the hand-narrow) are the CORRECT bytes; CI is producing the WRONG bytes. The slice's job is to make CI produce the right bytes too.

**Two ways to validate locally before opening the PR:**

1. `just sqlc-version-check` confirms your local sqlc matches the pin (`v1.31.1`).
2. `just sqlc-generate && git diff --exit-code -- internal/db/dbx/` should be clean post-fix. If it's clean locally but CI still fails, the divergence is in CI's install path (push Option A harder).

**Suggested investigation order before committing to an Option:**

1. Read `docs/audit-log/109-sqlc-toolchain-pin-decisions.md` to understand what Option D ("overrides block") already tried and why it was rejected. The "broke other things" note may have a specific failure that's worth re-testing against the current sqlc version.
2. Read `internal/db/queries/policies.sql` to find the `ListPoliciesWithAckRate` query. Identify the exact `CASE WHEN ... THEN (scalar subquery)::bigint END` expression — Option C's query rewrite candidates flow from this.
3. Reproduce the drift locally: run `just sqlc-generate` against a clean checkout of `main` and compare bytes to what's committed. If your local sqlc binary produces the same drift as CI, Option A is wrong (the issue isn't install-path divergence). If your local sqlc binary produces clean bytes and CI produces drift, Option A is the right answer.

**On the second file (`scf_anchors.sql.go`):** the drift comment in that file calls out the same `interface{}` regression. Quickly check whether its query has the same `CASE WHEN ... THEN scalar-subquery` shape; if yes, one fix should cover both. If no (different sqlc inferencer limitation), file slice 160 spillover via `/idea-to-slice` and ship this slice with policies.sql.go alone.

**On AC-10 (synthetic-drift PR for the gate-verify):** the easiest way to do this without polluting the PR queue is to push a throwaway branch to your fork, NOT to the upstream. The gate is repo-level so the failure will surface on the throwaway PR; copy the failure URL into the decisions log and close the test PR before merging slice 159.

**Cross-link to slice 153 reconcile:** the slice 153 batch-59 reconcile PR (#331) called out this drift as a spillover candidate. Once slice 159 ships, the reconcile entry's spillover-candidate-1 line can be marked resolved.

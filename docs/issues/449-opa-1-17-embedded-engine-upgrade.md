# 449 — OPA 1.4 → 1.17 embedded-engine upgrade (13 minors on the authz + control engine)

**Cluster:** Security
**Estimate:** M (1-2d)
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

`go.mod` pins `github.com/open-policy-agent/opa v1.4.0`. Dependabot PR
**#953** proposes the jump to `1.17.0` — thirteen minor releases of
Rego/evaluation drift. OPA is not a peripheral dependency here: per CLAUDE.md
("Auth (AuthZ) — RBAC + ABAC via OPA … Same OPA engine evaluates control
queries and authorization decisions") it is the **single load-bearing engine
for BOTH authorization decisions AND control evaluation**. The embedded
library backs `internal/authz/decision.go` (every authz allow/deny),
`internal/eval/rego.go` + `internal/eval/regocache/` (every control-as-code
evaluation), and `internal/api/adminauthzbundle/` (the hot-reload bundle
surface, slice 378).

Thirteen minors on a security-critical path is exactly the kind of bump that
auto-merge is wrong for. A Rego semantics change, a default-behavior shift in
the `rego` package API, or a change to how partial evaluation / prepared
queries memoize can silently flip an authz outcome or a control verdict. This
slice **supersedes dependabot PR #953** and replaces the "bump + green CI"
auto-merge with a deliberate regression pass: bump, run the full authz +
control-eval integration suite, diff the Rego evaluation semantics for the
queries atlas actually issues, and confirm slice 377's prepared-query
perf-cache fast-path (`internal/eval/regocache`) still holds its
7× speedup and its correctness contract.

**Scope discipline.** This is a dependency upgrade with a regression gate, not
a Rego-policy refactor. No `.rego` policy logic changes unless the upgrade
_forces_ a syntax/semantics migration (in which case the migration is the
minimum diff to preserve current behavior, recorded in the decisions log). No
new authz rules, no new control kinds, no API surface changes.

## Threat model

STRIDE pass — the threat models are the point for this slice. The dominant
risk is a **silent semantics regression on a security-critical evaluation
path**, not a classic injection/disclosure bug.

**S — Spoofing**

- _Threat:_ A Rego-evaluation change in `1.5..1.17` alters how `input` is
  coerced or how an undefined reference resolves, such that an unauthenticated
  or wrong-tenant `input` document evaluates to a different decision than under
  1.4. The authz path consumes a JWT-derived `input` (`internal/authz/input.go`);
  a coercion change could make a malformed identity claim resolve permissively.
- _Mitigation:_ Run the full `internal/authz` integration suite
  (`decision_test.go`, `matrix_integration_test.go`, the `sliceNNN_test.go`
  regression corpus) post-bump; these encode the role→permission matrix as
  ground truth. Add an explicit assertion that an empty/absent `input.subject`
  still denies (fail-closed) under 1.17.
- _Anti-criterion:_ P0-449-3.

**T — Tampering**

- _Threat:_ The hot-reload authz-bundle path (slice 378,
  `internal/api/adminauthzbundle`) parses an operator-supplied Rego bundle. An
  OPA parser/loader behavior change across 13 minors could accept a bundle that
  1.4 rejected (or vice-versa), changing the integrity contract.
- _Mitigation:_ Re-run the slice 378 bundle reload/validation integration tests
  against 1.17; assert a malformed/oversized bundle is still rejected.
- _Anti-criterion:_ P0-449-4.

**R — Repudiation**

- _Threat:_ `internal/authz/audit.go` writes a decision audit record. An OPA
  result-shape change (e.g. result-set ordering, metadata fields) could alter
  what gets logged, weakening the audit trail.
- _Mitigation:_ Assert the decision audit-log row shape is unchanged post-bump
  (existing `audit_guc_integration_test.go` coverage).

**I — Information disclosure**

- _Threat:_ An OPA error/diagnostic format change could surface internal policy
  text or `input` contents in an error returned to a caller (cross-tenant leak
  risk in a multi-tenant deny path).
- _Mitigation:_ Confirm authz/eval error paths still return opaque,
  non-leaking errors (composes with slice 367 errleak discipline); do not
  surface raw OPA diagnostic strings to API responses.

**D — Denial of service**

- _Threat:_ An eval-performance regression across 13 minors (or a prepared-query
  cache invalidation behavior change) could blow up the per-tick
  `EvaluateAll` cost that slice 377 reduced by 86%.
- _Mitigation:_ Re-run the slice 377 `regocache` benchmark; assert the
  prepared-query fast-path is still hit and per-call ns/op has not regressed
  beyond a documented tolerance. Record the measured before/after in the
  decisions log.
- _Anti-criterion:_ P0-449-5.

**E — Elevation of privilege**

- _Threat:_ The core failure mode — a Rego logical-operator, default-value, or
  set-membership semantics change flips a `deny` to `allow` for some
  role/tenant tuple, granting a non-admin an admin-only operation.
- _Mitigation:_ The role→permission matrix integration corpus
  (`matrix_integration_test.go` + the `matrix_validator.go` guard) is the
  ground-truth gate. CI must pass it unchanged. Any _intended_ matrix change is
  out of scope for this slice (it would be a separate authz slice).
- _Anti-criterion:_ P0-449-1, P0-449-2.

## Acceptance criteria

- [ ] **AC-1.** `go.mod` bumps `github.com/open-policy-agent/opa` `v1.4.0` →
      `v1.17.0`; `go mod tidy` run; `go.sum` updated; `go build ./...` clean.
- [ ] **AC-2.** Go unit suite (`go test ./...`) green across all OPA-importing
      packages: `internal/authz`, `internal/eval`, `internal/eval/regocache`,
      `internal/api/adminauthzbundle`.
- [ ] **AC-3.** Go integration suite green:
      `go test -tags=integration -p 1 ./internal/authz/... ./internal/eval/...`
      — including the `matrix_integration_test.go` role→permission ground-truth
      corpus and the `sliceNNN_test.go` authz regression set, **unchanged**.
- [ ] **AC-4.** Slice 377 prepared-query perf-cache fast-path verified: the
      `internal/eval/regocache` benchmark re-run shows the fast-path is still
      hit and per-call ns/op is within a documented tolerance of the 1.4
      baseline (record both numbers in the decisions log).
- [ ] **AC-5.** Slice 378 hot-reload authz-bundle integration tests
      (`internal/api/adminauthzbundle`) pass against 1.17; a malformed bundle is
      still rejected.
- [ ] **AC-6.** Rego-semantics diff recorded: the decisions log enumerates any
      OPA `rego` package API or evaluation-semantics changes across 1.5..1.17
      that touch atlas's actual query shapes, and how each was verified
      behavior-preserving (or, if a syntax migration was forced, the minimal
      diff applied).
- [ ] **AC-7.** Fail-closed assertion added/confirmed: an authz decision with an
      empty/absent subject `input` still denies under 1.17.
- [ ] **AC-8.** `pre-commit run --all-files` passes; CI green. PR body notes
      "Supersedes #953".
- [ ] **AC-9.** JUDGMENT decisions log at
      `docs/audit-log/449-opa-1-17-embedded-engine-upgrade-decisions.md`
      records the perf before/after, the semantics-diff verdict, and any forced
      migration, with per-decision confidence + detection-tier fields.

## Constitutional invariants honored

- **Tenant isolation / fail-closed authz (invariant #6).** The role→permission
  matrix and fail-closed deny remain the gate; the bump must not weaken either.
- **Ingestion / evaluation separation (invariant #2).** Control evaluation
  (eval) is read-only over the evidence ledger; the OPA bump does not change
  that — it must not introduce a write path.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "Auth (AuthZ) — RBAC + ABAC via OPA";
  "OPA deployment — Embedded Go library (v1)".
- `Plans/canvas/04-evidence-engine.md` — control-as-code evaluation.

## Dependencies

- Supersedes **dependabot #953** (opa 1.4 → 1.17).
- **#377** (eval Rego prepared-query cache, merged) — the perf fast-path this
  slice must verify still holds.
- **#378** (hot-reload authz bundle, merged) — the bundle path this slice must
  re-validate.

## Anti-criteria (P0 — block merge)

- **P0-449-1.** Does NOT change the role→permission matrix or any authz
  decision outcome. If a Rego-semantics change _would_ alter an outcome, STOP —
  that is a behavior regression, not an upgrade; surface it.
- **P0-449-2.** Does NOT modify `.rego` policy logic except a forced
  syntax/semantics migration whose only goal is preserving current behavior
  (minimal diff, recorded in the decisions log).
- **P0-449-3.** Does NOT allow a malformed/absent authz `input` to resolve
  permissively — fail-closed deny is asserted under 1.17.
- **P0-449-4.** Does NOT relax the slice 378 bundle-validation contract.
- **P0-449-5.** Does NOT regress the slice 377 prepared-query perf fast-path
  beyond the documented tolerance without an explicit, recorded decision.
- **P0-449-6.** Does NOT auto-merge — security-critical engine bump; maintainer
  reviews the semantics-diff verdict.

## Skill mix (3-5)

- `tdd` — run the existing authz + eval integration corpus as the regression gate.
- `dependency-auditor` — read the OPA 1.5..1.17 changelogs for breaking
  semantics changes.
- `security-review` — the authz path is in scope; verify fail-closed + no leak.
- `performance-profiler` — re-run the slice 377 regocache benchmark.
- `simplify` — pre-PR pass (should be near-zero for a dep bump).

## Notes for the implementing agent

- The OPA usages to verify are: `internal/authz/decision.go` (authz),
  `internal/eval/rego.go` + `internal/eval/regocache/regocache.go` (control
  eval + slice 377 cache), `internal/api/adminauthzbundle/` (slice 378 reload).
- Read the OPA release notes per minor for `rego` package API changes and Rego
  language semantics changes; the project uses the embedded `rego.New(...)`
  - `PrepareForEval` style (slice 377). Pay special attention to any change in
    default `--v1-compatible` / strict-mode behavior across the 1.x line.
- This is a JUDGMENT slice because "is this semantics change
  behavior-preserving for _our_ queries?" is a judgment call the engineer makes
  and records — not a mechanical AFK green-CI flip.

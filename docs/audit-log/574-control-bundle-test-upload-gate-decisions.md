# 574 — Control-bundle upload test-gate: JUDGMENT decisions log

Slice type: JUDGMENT. The slice doc named the gate policy (hard-block vs warn;
per-tenant opt-in; no-tests handling; SQL-on-upload) as the implementing agent's
call. This file records those calls + their rationale. It does NOT block merge —
the maintainer iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: unit
- detection_tier_target: unit

No shipped-behavior defect escaped during the build. The one design-time
correction (the typed-nil `*control.Store` interface trap — see below) was caught
at the green step by the package's own unit tests before any handler path could
panic in production. `detection_tier_actual = unit`, `detection_tier_target =
unit` (a Go interface-nil subtlety; unit is the cheapest and correct tier).

## Build-time correction (detection-tier note)

The unit test harness constructs the handler as `New(nil, nil)` — a nil
`*control.Store`. My first wiring passed `h.store` straight into `runGate` as the
`txRunner` interface. A typed-nil pointer wrapped in an interface is NOT `== nil`,
so `runner != nil` was true and a SQL-fixture bundle would have called
`WithReadOnlyTenantTx` on a nil pointer → panic. The fix: the handler assigns
`gateRunner` only when `h.store != nil`, passing an explicit `nil` interface
otherwise. The non-SQL unit tests already passed (they never reach the tx path);
adding `TestRunGate_SQLWithNoRunnerBlocks` pinned the nil-runner degradation
(SQL fixture → per-case ERROR → block, never a panic or a false pass).

## Decisions made

### D-POLICY-1 — Gate policy: HARD-BLOCK on any failing/errored case (AC-2)

- **Options considered:** (a) hard-block (reject the upload on any red case);
  (b) advisory/warn-only (accept, attach a warning); (c) per-tenant
  `require_bundle_tests_pass` flag defaulting to one of the above.
- **Chosen:** (a) hard-block, as the global v0 default.
- **Rationale:** the slice's framing is literal — "tests must pass to upload".
  The whole value is that a control whose query is provably wrong against its
  own fixtures NEVER reaches the catalog (the canvas anti-pattern is a tool that
  "silently mis-states compliance"). An advisory gate that lets a red bundle
  through defeats the guarantee — a board pack or auditor sample could then draw
  on a control the author's own test says is wrong. Hard-block is the CI-gate
  analogue the slice doc names ("the control-as-code analogue of a CI test
  gate"). The rejection is loud and actionable: a `400` whose body carries the
  full per-case report (`gateRejectionResp.test_report`), so the uploader sees
  exactly which fixture failed (AC-5).
- **What "block" covers:** a case that FAILS (ran, actual != expected) AND a case
  that ERRORS (query could not run — Rego compile error, SQL with no DB,
  unsupported language). Both are red. `report.AllPassed()` (Failed==0 AND
  Errored==0) is the gate predicate — identical to the CLI's exit-code rule
  (slice 496 AC-5), so the upload gate and `atlas-cli controls test` agree on
  the same bundle (anti-criterion P0-496-1 holds transitively).

### D-POLICY-2 — No-tests handling: ALLOW with a warning (AC-3)

- **Options considered:** (a) reject a bundle with no `tests/` (mandatory tests);
  (b) allow it, surfacing the absence as a warning (tests optional, enforced only
  when present).
- **Chosen:** (b) allow-with-warning, as the global v0 default.
- **Rationale:** matches slice 496's CLI (no-tests = warning, not failure) so the
  two surfaces stay consistent. Three concrete reasons (b) over (a) for v0:
  1. **Every existing bundle ships no tests.** Mandatory-tests would reject every
     control already authored — a breaking change with no migration path.
  2. **The inline-JSON upload path structurally cannot carry fixtures** (a pasted
     `manifest_yaml` has no `tests/` tree). Mandatory-tests would make the JSON
     path unusable; allow-with-warning keeps it working while still nudging
     authors toward the tarball-with-tests path.
  3. **A manual-attested control may legitimately have nothing to query-test.**
     The success response carries `gate_warning` so the absence is visible, not
     silent. A tenant that WANTS mandatory tests gets it via D-POLICY-3's deferred
     flag.

### D-POLICY-3 — Per-tenant `require_bundle_tests_pass` flag: DEFERRED (spillover 608)

- **Chosen:** ship a single global hard-block-with-no-tests-allowed default for
  v0; defer the per-tenant policy flag to a follow-on slice.
- **Rationale:** a per-tenant flag needs a tenant-settings column (a migration)
  - a settings UI/API surface to set it — disproportionate for v0 when the
    global default (hard-block present tests; allow absent tests) is the right
    behaviour for the solo-leader persona. Shipping the flag now would also force a
    policy-resolution code path with only one reachable value. Filed as **slice
    608** (`docs/issues/608-per-tenant-bundle-test-gate-policy.md`), parent #574.
    NO migration lands in this slice.

### D-SQL-1 — SQL fixtures RUN on the upload path via a read-only tenant tx (AC-4)

- **Chosen:** the gate evaluates `sql`-language fixtures inside a READ-ONLY,
  tenant-scoped transaction (`control.Store.WithReadOnlyTenantTx`) that is ALWAYS
  rolled back; a SQL fixture failure maps to the same `400`.
- **Rationale:** the slice doc notes the server upload handler HAS a tenant tx
  (unlike the local CLI), so SQL fixtures — which the slice-495 sandbox
  materialises into a CTE and runs inside a read-only subtransaction — CAN run on
  the upload path where they could not locally (slice 496 `ErrFixtureSQLNeedsDB`).
  The gate opens the tx only when the bundle actually declares a `sql` query
  (`needsSQL`); Rego/JSON-path-only bundles evaluate fully in memory with no
  database (slice 496 AC-9). Constitutional invariant #2 is upheld two ways: the
  tx is opened `pgx.ReadOnly` AND it is always rolled back — the gate evaluates,
  it never writes evidence or any source-of-truth row. A SQL fixture with no
  runner (a unit server with no pool) degrades to a per-case ERROR, which
  D-POLICY-1 treats as a block — never a silent pass.

### D-WIRE-1 — Capture `tests/*.yaml` in `ParseTarball`; run them in memory

- **Options considered:** (a) write the uploaded archive's `tests/` to a temp dir
  and call the existing `bundletest.Run(dir)`; (b) capture the `tests/*.yaml`
  bytes into `control.Bundle.TestFiles` during `ParseTarball` and add an
  in-memory `bundletest.RunFromFiles` that loads cases from bytes.
- **Chosen:** (b).
- **Rationale:** `ParseTarball` already streams the whole archive once and
  discards everything except `control.yaml` + `description.md`; capturing the
  top-level `tests/*.yaml` bytes in that same pass adds no second read and no
  filesystem I/O on the hot path. (a) would write attacker-influenced bytes to
  disk on every upload (a needless surface) and then re-read them. The in-memory
  loader (`loadTestCasesFromBytes`) reuses slice 496's exact decode +
  validation + unique-name rules (`parseTestFileBytes`, `runCase`), so the gate's
  verdict is byte-for-byte what `atlas-cli controls test` reports on the same
  bundle. Only the top-level `tests/*.yaml` (not nested `tests/sub/`) is captured,
  matching the slice-496 directory loader exactly.
- **Bounds (threat model):** ParseTarball caps retained test files at 200
  (`maxTestFilesPerBundle`) and each at 4 MB (`maxTestFileBytes`), inside the
  existing 500-entry / 20 MB-uncompressed archive guards. The slice-495/496 query
  sandbox (capability-restricted Rego, read-only SQL subtxn, in-process
  JSON-path, per-query timeout) applies unchanged — the gate grants NO new
  capability; it moves the same evaluation from post-upload to pre-upload.

## Scope honored

- Hooks the EXISTING `POST /v1/controls:upload-bundle` handler — no new route.
- Reuses slice 496's `bundletest` runner + `eval.EvaluateFixture` — no second
  evaluator, no reimplemented eval engine.
- Read-only: invariant #2 upheld (read-only tx, always rolled back; the runner
  itself performs zero writes).
- No `_INDEX.md` / `_STATUS.md` edits.

## Revisit once in use

- **Per-tenant `require_bundle_tests_pass`** (slice 608) — advisory-mode opt-in
  for authoring, and mandatory-tests opt-in for strict tenants.
- **Mandatory-tests for `automated` controls.** The slice doc floated requiring
  tests specifically for `automated` controls (which always have a query to
  test). v0 ships the uniform allow-with-warning default; a per-implementation_type
  policy is a natural refinement once the per-tenant flag exists.
- **Inline-JSON fixtures.** The JSON upload path cannot carry `tests/`; if authors
  want gate coverage on that path, an inline `tests:` block in the JSON envelope
  is a possible add (low priority — the CLI prefers tarballs).

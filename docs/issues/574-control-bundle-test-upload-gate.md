# 574 — Control-bundle upload test-gate: "tests must pass to upload"

**Cluster:** control-as-code
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (gate policy: hard-block vs warn; per-tenant opt-in)
**Status:** `blocked` (depends on #496 — control-bundle test runner — merged first)

## Narrative

Slice 496 shipped the control-bundle test runner (`atlas-cli controls test
<bundle-dir>`): an author can declare fixture evidence + expected pass/fail and
verify their evidence query locally before uploading. Slice 496 deliberately
scoped OUT wiring the runner into the upload path as a hard gate (P0-496-5
"does NOT add a hard upload-time test gate"); it shipped the runner first.

This slice closes that loop: make a bundle's declared tests a **precondition of
upload**. When a bundle is pushed to `POST /v1/controls:upload-bundle` (or via
`atlas-cli controls upload`), the platform runs the bundle's `tests/` cases
through the SAME `bundletest.Run` path the CLI uses; if any case fails or errors,
the upload is rejected with a 400 + a per-case report, so a control whose query
is provably wrong against its own fixtures never reaches the catalog. This is the
control-as-code analogue of a CI test gate — it makes "the bundle ships only if
its tests are green" a platform guarantee, not an author convention.

## Design calls (the JUDGMENT surface)

- **Hard-block vs warn-only.** Reject on any failing case (strict CI gate) vs
  accept-with-warning (advisory). Likely strict for `automated` controls, but a
  per-tenant policy flag (`require_bundle_tests_pass`) may be the right shape so a
  team can opt into advisory-only during authoring.
- **No-tests handling.** A bundle with no `tests/` directory: reject (require
  tests) vs allow (tests optional, only enforced when present). Slice 496's CLI
  treats no-tests as a warning; the upload gate should likely match (allow, but
  surface) unless the tenant opts into mandatory tests.
- **SQL fixtures on the server path.** The server upload handler HAS a tenant-
  scoped tx available (unlike the local CLI), so SQL-language fixtures can run on
  the upload path where they could not locally. Decide whether the gate runs SQL
  fixtures (it has the tx) and how a SQL fixture failure maps to the 400.
- **Threat model.** Running author-supplied fixtures through the evaluator on the
  upload path executes author queries server-side — already true for live
  evaluation post-upload, but the gate moves it to pre-upload. The slice-496 /
  slice-495 sandbox posture (capability-restricted Rego, read-only SQL subtxn,
  in-process JSON-path, per-query timeout) applies; confirm no new capability is
  granted.

## Acceptance criteria

- [ ] **AC-1.** The upload handler runs the bundle's `tests/` cases through
      `bundletest.Run` (the slice-496 runner) against an in-memory fixture set.
- [ ] **AC-2.** A bundle with a failing/errored test case is rejected (400 +
      per-case report) under the strict policy.
- [ ] **AC-3.** The gate policy (strict vs advisory; mandatory-tests vs
      tests-optional) is configurable (per-tenant flag or documented default).
- [ ] **AC-4.** SQL-language fixtures run on the upload path (the handler has a
      tenant tx); a SQL fixture failure maps to the same 400.
- [ ] **AC-5.** The CLI `controls upload` surfaces the gate's per-case report on
      rejection so the author sees which fixture failed.
- [ ] **AC-6.** Integration test: a bundle with a wrong query + a fixture that
      catches it is rejected at upload; a bundle whose tests pass uploads.

## Dependencies

- **#496** (control-bundle test runner) — provides `bundletest.Run` +
  `eval.EvaluateFixture` the gate calls. Merge 496 first.
- **#009 / #014** — the upload handler + schema-registry gate this composes with.

## Notes

Parent slice: #496. This was explicitly deferred by slice 496's P0-496-5 +
recorded in `docs/audit-log/496-control-bundle-test-runner-decisions.md`
("Revisit once in use → Hard upload gate"). Do NOT edit `_INDEX.md` or
`_STATUS.md`.

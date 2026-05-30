# Slice 392 — contract-test-tier rollout — decisions log

JUDGMENT slice. The build-time subjective calls (which endpoints, golden
shapes, e2e-mock-loading evaluation) are recorded here per the
continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`),
slice 349 (`docs/issues/349-contract-test-tier-evaluation.md`), slice
392 spec (`docs/issues/392-contract-test-tier-rollout.md`).

---

## D1 — Which endpoints get the golden-file contract tier

The slice doc named four recommended targets: `/v1/admin/demo/status`,
`/v1/me`, `/v1/version`, `/v1/metrics`. ADR-0007's load-bearing
constraint is that the **provider recorder must run on the plain
`go test ./...` unit surface** (no DB) — that is what keeps the tier
zero-new-gate (it rides the existing Go-unit surface, not the Go
integration surface). I picked the three of the four that satisfy that
constraint and deferred the fourth.

| Endpoint                    | Decision                   | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| --------------------------- | -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/version`           | **COVERED**                | Pure unit surface. `VersionHandler` takes a `fieldsFn` callback — no DB, no auth, no tenancy. Deterministic 4-field struct (`version`/`commit`/`build_time`/`go_version`). BFF (`web/app/api/version/route.ts`) is verbatim passthrough. Cheapest win; slice 072 already documents the shape as a breaking-change boundary.                                                                                                                                                                                                                                                                                                                                                                              |
| `GET /v1/me`                | **COVERED**                | Recordable on the unit surface WITHOUT a DB: `buildProfile` only calls `users.Store.GetByID` when `cred.UserID` parses as a UUID; with a non-UUID credential id (the API-key / bootstrap-admin path, `key_…`) it returns the **synthetic profile** with zero store/pool access, and `resolveRoles` returns `[]` when the resolver is nil. So `NewProfile(nil, nil, nil)` + an injected credential + tenancy context records the real wire shape (10 fields incl. the always-present `roles: []` and nullable `time_zone`). High consumer coupling — this is the slice-210-class identity surface.                                                                                                        |
| `GET /v1/admin/demo/status` | **COVERED**                | `Status` handler's happy path needs only an admin credential in context and the injected `isEnabled` gate — no DB (only Seed/Teardown touch `authPool`). Trivial `{enabled: bool}` shape, both `true` and `false` variants. slice 349's named secondary target; fast win that proves the tier generalizes to an admin-gated endpoint.                                                                                                                                                                                                                                                                                                                                                                    |
| `GET /v1/metrics`           | **DEFERRED** (not covered) | The `ListCatalog` handler reads through `dbx.New(h.pool)` against a real Postgres pool — there is no interface seam to record it on the plain `go test ./...` unit surface. Recording it would require either (a) the Go **integration** surface (violates ADR-0007's "rides the Go-unit surface" intent and would couple the golden's regeneration to a DB bring-up) or (b) refactoring the metrics handler to read through an injectable query interface (a real refactor, out of scope for a test-tier rollout). Deferred with rationale; the catalog shape is also already pinned structurally by the slice-076 handler unit tests. Captured as a candidate for a future slice if the seam is added. |

**Outcome:** 3 endpoint pairs covered (AC-1 requires ≥3). Exceeds the
floor while respecting the zero-new-gate constraint that bounds the tier.

## D2 — Golden shape: variants, not single body

Followed the pilot exactly: each golden is `{_comment, endpoint,
variants{}}` where `variants` is a map of stable contract-identifier
keys → recorded bodies. The provider records every variant the BFF must
tolerate; the consumer asserts the BFF's load-bearing assumptions
against each. Variants chosen per endpoint:

- **version:** `release` (all four fields populated), `dev_build`
  (sparse build metadata — what an un-stamped `go build` emits). The
  four fields are always present (non-pointer strings) so the BFF
  passthrough is total.
- **me:** `synthetic_admin` (API-key admin, no users row → `roles: []`,
  `time_zone: null`, `is_admin: true`), `synthetic_non_admin`
  (non-admin credential). Both exercise the no-DB synthetic path. The
  load-bearing consumer assertions: `user_id`/`tenant_id`/`is_admin`
  always present; `roles` and `owner_roles` always arrays (never null);
  `time_zone` nullable.
- **demo/status:** `enabled` and `disabled` — the two boolean states.

## D3 — Shared recorder helper

The pilot's `recordInstallStateVariant` + golden read/compare logic is
near-identical per endpoint. To avoid copy-pasting the canonicalize /
read-golden / diff / -update plumbing three times, I extracted a small
package-internal helper `contractrecord.go` in `internal/api` (and a
sibling in `internal/api/me` + `internal/api/admindemo` packages, since
Go test files cannot cross package boundaries). Each helper is a thin
canonicalize-and-diff-against-golden utility; the pilot's
`install_state_contract_test.go` is intentionally LEFT UNTOUCHED (it
already works and is the reference; rewriting it to use the shared helper
would be churn outside this slice's scope). The new recorders share the
helper.

NOTE: the demo/status and me recorders live in their own packages
(`admindemo`, `me`) because the handlers do. Each package's contract
test reuses the SAME relative-path golden-under-`web/lib/contracts`
convention; the relative path differs by package depth (`../../../web/...`
for the deeper packages vs `../../web/...` for `internal/api`).

## D4 — `-update` flag composition

Reused the pilot's lazy `flag.Lookup("update")` pattern so multiple
contract test files in the same package (and across packages) compose
with one `go test … -update` invocation without a duplicate-flag panic.
For the `me` and `admindemo` packages, which had no prior `-update`
flag, the lazy lookup registers it on first use.

## D5 — AC-3: should the `/e2e/` `route.fulfill` mocks load from goldens?

**DEFER, with rationale.** Evaluated teaching the 57 `route.fulfill`
mocks (slice 334 P-1) to load from the recorded goldens.

- **Why it is attractive:** it would retire the mock-vs-reality drift at
  its root — the e2e mocks would serve the provider-recorded truth.
- **Why it is NOT cheap (the deferral):**
  1. The goldens cover 3 endpoints; the e2e mocks cover ~30+ distinct
     upstream routes across `web/e2e/*.spec.ts`. A golden-backed mock
     helper only de-risks the handful of routes that have goldens; the
     other ~27 still hand-write bodies. The fragility P-1 names is not
     materially reduced until the golden coverage approaches the mock
     coverage — which is many rollout slices away.
  2. The e2e mocks frequently need _per-test_ variations (error states,
     pagination, empty sets) that the happy-path goldens do not carry.
     A `fulfillFromGolden(variant)` helper would still need a
     hand-written-override escape hatch for those, so it does not
     eliminate hand-written bodies — it adds a second code path beside
     them.
  3. Wiring it now, against 3 goldens, is premature: it builds the
     helper before there is enough golden coverage to justify it, and
     risks a half-adopted pattern (some specs golden-backed, most not)
     that is harder to reason about than uniform hand-mocks.

**Decision:** defer the e2e-mock-loading helper to a dedicated follow-on
slice that lands once golden coverage spans the high-traffic dashboard
routes the e2e suite actually traverses. Filed as a spillover (see
slice doc + spillover note in the PR). This is the cheap-vs-defer call
AC-3 explicitly asks for, with the "implement if cheap" branch declined
on the coverage-threshold reasoning above.

## D6 — Drift sensitivity proof (AC-2)

Proved on the **version** endpoint (the cleanest to mutate): renamed the
golden's `build_time` key → `built_at`. Result: the provider recorder
failed (`variant "release" wire shape drifted from golden`) AND the
consumer vitest failed (the BFF-passthrough `toEqual` mismatch + the
"every variant carries build_time" assertion). Both halves red on a
single-field rename; golden restored; both green. This is the
slice-210-class catch, reproduced. (Documented in the PR per AC-2.)

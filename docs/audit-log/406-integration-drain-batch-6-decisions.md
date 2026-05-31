# Slice 406 — integration-enrolment drain batch 6 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 6 of the slice-390 drain. Enrols the four packages that carry a
`//go:build integration` tag but were absent from the `tests-integration`
job's package list in `.github/workflows/ci.yml` (catalogued by the slice-345
guard's `KNOWN_UNENROLLED` allowlist). Mirrors the slice-401/402/403/404/405
method (depended on 405 landing first so the batches stay sequential and CI
stays green between them).

## Scope

```
internal/auth
internal/auth/keystore/fsstore
internal/mcp
internal/observability/otel
```

## Enrolment-form decisions (load-bearing)

- **`internal/auth` enrolled as the BARE root `./internal/auth`**, NOT
  `./internal/auth/...`. The directory `internal/auth/` holds exactly one Go
  file — `integration_test.go` in `package auth_test` (the slice-034
  users/sessions/api_keys RLS round-trip suite). It has **no `package auth`
  production source**. Its many subpackages (`oidc`, `jwtmw`, `users`,
  `oauthclient`, `oauthcode`, `revocation`, `userprefs`, `keystore/fsstore`,
  …) are tracked SEPARATELY — some already enrolled in the ci.yml list, and
  `keystore/fsstore` is a separate entry in THIS very batch. Enrolling the
  recursive `./internal/auth/...` form would double-enrol those subpackages
  (running their suites twice and muddying the `-p 1` ordering). The bare form
  runs only the root `auth_test` suite. This matches `KNOWN_UNENROLLED`'s bare
  `internal/auth` listing, the slice-403 api-root bare-form precedent, and the
  pre-existing bare `./internal/audit` entry already in the same ci.yml list.
- **`internal/auth/keystore/fsstore`** → `./internal/auth/keystore/fsstore/...`,
  **`internal/mcp`** → `./internal/mcp/...`, **`internal/observability/otel`**
  → `./internal/observability/otel/...` (per-leaf recursive — each `KNOWN_UNENROLLED`
  entry is a leaf package). All four inserted into the "Run integration tests"
  `go test` invocation, before the bare `./internal/api` root (which stays
  last per the slice-403 form decision).

## ci.yml GREP-TRAP note (slice 403 precedent honoured)

The four new entries were inserted into the **"Run integration tests"** `go
test` invocation ONLY (the block ending `./internal/audit/notes/... \` +
`./internal/api`). The `errleak-lint` + `duphelper-lint` `just` steps elsewhere
in ci.yml also carry `./internal/...` tokens; those were not touched. `actionlint`
on the edited file is clean (the only shellcheck warnings it reports —
SC2034/SC2045 at lines 244/267/501 — are pre-existing in unrelated steps, not
in the integration `go test` block).

## Local harness

Stood up a CI-faithful harness: fresh `postgres:16-alpine` container
(`security-atlas-pg-406`, host port 55406), applied `migrations/bootstrap/01-roles.sql`,
set `atlas_app`/`atlas_migrate` passwords, then applied all 66 forward
migrations in order as `atlas_migrate` (plain `psql`, per the memory note that
the Atlas community build panics on apply). Only `internal/auth` needs the DB
(`DATABASE_URL_APP` = atlas_app role, `DATABASE_URL` = atlas_migrate/BYPASSRLS
role); `mcp` uses an in-process fake HTTP platform, `otel` uses an in-memory
span exporter (+ optional NATS, which its in-process trace-propagation test
does not require), and `fsstore` uses `t.TempDir()` for the writable keystore
dir. No MinIO/NATS needed for the per-package runs.

## Per-package outcomes

| Package                          | Broke?  | Own-suite (integration)  | Existing floor | Floor action          |
| -------------------------------- | ------- | ------------------------ | -------------- | --------------------- |
| `internal/auth` (bare)           | YES (1) | `[no statements]`        | none           | none (zero-statement) |
| `internal/auth/keystore/fsstore` | no      | 75.0%                    | 74             | unchanged             |
| `internal/mcp`                   | no      | 65.1% (integration-only) | 80             | unchanged             |
| `internal/observability/otel`    | no      | 94.2%                    | 92             | unchanged             |

## `internal/auth` — 1 test broke; STALE test (not a product bug)

`TestOIDC_CallbackStateMismatchRejected` failed:

```
integration_test.go:317: expected ErrStateMismatch, got oidc: nonce mismatch (ID-token replay guard)
```

**Root cause (stale TEST, product code correct).** Slice 365 (the OIDC
ID-token-replay defence-in-depth slice) added a `NonceCookie` _presence_ check
to `HandleCallback` (`internal/auth/oidc/oidc.go:200-206`) that runs BEFORE the
state-mismatch (CSRF) check (`oidc.go:213-214`). The validation order is:
State-cookie present → Verifier-cookie present → Idp-cookie present →
**Nonce-cookie present (slice 365)** → query state non-empty → state matches.
The pre-365 test in `internal/auth/integration_test.go` sets the State,
Verifier, and Idp cookies and a mismatched query `state`, but never sets the
new NonceCookie. So the flow short-circuits at the nonce-presence gate and
returns `ErrNonceMismatch` before it ever reaches the state-mismatch check the
test is named for.

**Why a test fix, not a product fix, and not a spillover.** The ordering is
correct, intentional, and _locked in_ by slice 365's P0-365-1 (the additive
invariant: the state guard still fires, just after the nonce-presence guard).
The canonical slice-365 `ErrStateMismatch` test in
`internal/auth/oidc/oidc_nonce_integration_test.go` (lines ~294-321) ALSO adds
the NonceCookie before asserting `ErrStateMismatch`, precisely so the flow
reaches the state check. The root-package test simply predates the nonce cookie
and was never re-run after slice 365 because the package was on
`KNOWN_UNENROLLED` — the exact latent-rot the slice-390 drain exists to surface.
The defect is wholly in the test's setup (a missing cookie), so the correct,
unambiguous fix is to complete the setup. This is the slice's "stale test → fix
the test" branch, not the "real product bug → spillover" branch.

**Fix.** Added one line to `TestOIDC_CallbackStateMismatchRejected`:
`r.AddCookie(&http.Cookie{Name: oidc.NonceCookie, Value: "fixed-nonce"})`, with
a comment explaining the slice-365 ordering. The sibling
`TestOIDC_CallbackMissingCookieRejected` (sets NO cookies) is unaffected — it
fails at the very first State-cookie check and correctly returns
`ErrStateMismatch`. Full `internal/auth` suite green after the fix under
`-race -p 1` (one test legitimately self-`Skip`s: the BeginLogin happy path,
which needs a live IdP — documented, not a regression).

## `internal/auth` — coverage: zero-statement package, no floor, no excludes

`go test -tags=integration -cover ./internal/auth` reports `coverage:
[no statements]`. The directory holds only `integration_test.go` in the
external `package auth_test`; there is no `package auth` production code at that
path (the auth primitives live in subpackages: `apikeystore`, `bearer`,
`sessions`, `users`, `oidc`, …). A per-package line-coverage floor is undefined
for a zero-statement package, and fabricating one would be dishonest. Unlike the
slice-405 `internal/api/emptyset` case (which has a `doc.go` and so sits on
`excludes` with a justification), this path has _no_ Go source at all, so it is
never measured by the coverage gate and needs neither a floor nor an `excludes`
entry — the gate only iterates declared-floor keys and the package is not one.
This is the honest disposition per slices 396/401-405.

## `keystore/fsstore`, `mcp`, `otel` — coverage: existing floors, no change

All three already carry hard floors (`fsstore 74`, `mcp 80`, `otel 92`) added
by prior slices (312/317) and enforced in CI against the slice-279 MERGED
unit+integration profile (`-coverpkg=./...` across the whole integration run).
Enrolling each package's OWN integration suite into that run can only _raise_
its merged coverage (it adds covered lines from the suite plus a self-pkg
contribution), so no floor can drop below its current value. No floor change is
warranted or made.

- `fsstore`: own integration suite measures 75.0% in isolation — already above
  its 74 floor on its own, before any merge contribution. (Uses `t.TempDir()`
  for the writable keystore dir, as the brief flagged; no leakage.)
- `otel`: own suite 94.2% ≥ floor 92.
- `mcp`: own integration suite alone is 65.1% and the unit-only suite is 64.5%;
  the 80 floor is met in CI by the MERGED `-coverpkg=./...` profile, which also
  captures transitive coverage of `internal/mcp` from sibling suites
  (`internal/api/mcpwriteproposals`, `internal/mcp/writeproposals`, the
  end-to-end MCP session). That floor pre-existed and was already passing in CI
  before this slice; adding `internal/mcp`'s own suite to the enrolled list only
  increases the merged number. No change.

## Combined serial run (reproduced CI conditions, not just isolation)

Per the slice-405 lesson (FK-wipe contamination is invisible in isolation), ran
the four packages together with the most likely contaminating neighbour
(`internal/demoseed`, which seeds rows under the shared canonical tenant):

```
go test -tags=integration -race -p 1 -count=1 \
  ./internal/demoseed/... \
  ./internal/auth \
  ./internal/auth/keystore/fsstore/... \
  ./internal/mcp/... \
  ./internal/observability/otel/...
```

All GREEN, zero FAIL. No FK-wipe contamination is possible here: the `auth`
suite scopes every test to a fresh random tenant UUID and cleans up its own
rows via the admin (`BYPASSRLS`) pool (`DELETE … WHERE tenant_id = $1`), never
doing a global un-scoped `DELETE FROM controls`-style wipe; `mcp`, `otel`, and
`fsstore` do not touch the shared database at all. This batch is the benign
case the drain confirms quickly, in contrast to slice 405's deep RESTRICT-FK
chain.

## Tests fixed

- `internal/auth/integration_test.go` — `TestOIDC_CallbackStateMismatchRejected`:
  added the slice-365 `NonceCookie` to the request so the flow reaches the
  state-mismatch check it asserts (stale-test fix; product behaviour and
  slice-365 ordering are correct and unchanged).

## Product fix

**None.** The single failure was a stale test (a missing cookie in setup), not
a product bug. The slice-365 nonce-before-state ordering is correct and locked
by P0-365-1.

## Spillover

**None.** The failure needed no separate design work — a one-line test-setup
completion, the exact class of latent rot the slice-390 enrolment drain exists
to surface.

## Detection-tier classification

- `detection_tier_actual`: `integration` (the failure surfaced the moment the
  never-run `internal/auth` integration suite was executed locally during this
  drain).
- `detection_tier_target`: `integration` (this is exactly where it should have
  been caught — and would have been, had the package been enrolled at slice 365.
  The gap was enrolment, not test tier; closing it is precisely slice 390's job).

## Coverage dispositions (summary)

- `internal/auth`: **no floor, no excludes** — zero-statement package
  (`[no statements]`); no honest floor exists and the gate never measures it.
- `internal/auth/keystore/fsstore`: floor **74** unchanged (own suite 75.0%).
- `internal/mcp`: floor **80** unchanged (merged-profile floor; enrolment only
  raises it).
- `internal/observability/otel`: floor **92** unchanged (own suite 94.2%).

## KNOWN_UNENROLLED shrink

13 → 9 (removed the four batch-6 packages: `internal/auth`,
`internal/auth/keystore/fsstore`, `internal/mcp`, `internal/observability/otel`).
Guard self-test 12/12 pass; `./scripts/audit-integration-enrolment.sh` reports
OK with 78 enrolled / 9 waived. `scripts/check-coverage-excludes.sh` reports OK
(45 excludes, all justified, no orphans) — unchanged, as no `excludes` edits
were needed this batch.

## Files touched

- `.github/workflows/ci.yml` — 4 package entries added to the integration job's
  "Run integration tests" step (`./internal/auth` bare; `fsstore`, `mcp`, `otel`
  per-leaf recursive; all inserted before the bare `./internal/api` root).
- `scripts/audit-integration-enrolment.sh` — 4 entries removed from
  `KNOWN_UNENROLLED` (13 → 9).
- `internal/auth/integration_test.go` — added the slice-365 NonceCookie to
  `TestOIDC_CallbackStateMismatchRejected` (stale-test fix).
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/406-integration-drain-batch-6-decisions.md` — this file.

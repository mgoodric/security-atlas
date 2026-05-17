# 082 — Playwright e2e seed-data harness + un-quarantine — decisions log

Slice 082 is `Type: AFK`. This log records the subjective build-time
judgment calls made while shipping the seed-data harness that
un-quarantined the `Frontend · Playwright e2e` CI job after slice 079's
pause. Format mirrors the JUDGMENT-slice convention (Decisions made
· Revisit once in use · Confidence).

## Decisions made

### 1. Harness shape — `psql` subprocess, not a Postgres TS client

**Options considered:**

- **(A)** Add `pg` (`node-postgres`) as a `web/` devDependency and
  open a real Pool inside `seed.ts`. Idiomatic JS.
- **(B)** Spawn `psql` as a subprocess via `node:child_process.execFileSync`
  and feed it the fixture SQL files. No new npm dep; relies on `psql`
  being on PATH.

**Chosen: (B).** Three reasons:

1. **Zero new dep.** The slice notes say "most of this slice is wiring,
   not authoring" — adding a new runtime dep (with its own version
   floors, peer-dep matrix, and Dependabot surface) for a test-only
   utility is gratuitous. `psql` is on PATH in every reasonable dev
   environment AND on the `ubuntu-latest` GitHub runner image; the CI
   job already uses `psql` for migrations.
2. **Reuses the fixture format.** The fixtures already exist as `.sql`
   files (`fixtures/walkthroughs/*.sql`); a TS client would either
   re-parse them (fragile) or duplicate the row contents in TS
   literals (drift bait). `psql -f <file>` is the path of least
   surprise.
3. **`ON_ERROR_STOP=1` parity.** The walkthrough fixtures already
   target `psql -v ON_ERROR_STOP=1`; using anything else means
   inheriting a different error model.

**Revisit once in use:** if the harness grows logic that does NOT live
naturally in SQL (e.g. computing HMACs, reaching out to MinIO/NATS for
non-SQL state), the API-key insertion is already the first such case
and it's still a one-liner `psql -c`. If a third or fourth such case
lands, reconsider the TS-client path.

**Confidence: high.**

### 2. Spec-body scoping — wire `beforeAll` but DO NOT un-comment assertions

**Options considered:**

- **(i)** Slice 082 ships the harness AND un-comments every assertion
  in all five specs. The Playwright job becomes the gate for all five
  un-shimmed feature surfaces in one PR.
- **(ii)** Slice 082 ships the harness + `beforeAll` wiring + the CI
  un-quarantine. Assertions remain commented. Per-spec un-comment
  slices follow (one per spec, sized to taste). The CI gate exercises
  the harness end-to-end but does not assert against the rendered UI.

**Chosen: (ii).** Three reinforcing reasons:

1. **Slice notes call it out.** "Run specs individually as
   `seedFromFixture()` calls land; do not batch debugging across all
   five at once." That guidance directly anticipates this scoping
   split.
2. **Failure surface is bounded.** With (i) any unrelated UI
   regression in any of the five views poisons the slice's PR;
   maintainer attention is spent debugging visual diffs instead of
   reviewing the harness. With (ii) the harness PR has one job: prove
   the seed runs end-to-end.
3. **Reversal is cheap.** Each per-spec un-comment slice is a small
   diff that uncomments one file. The harness wiring is unchanged.
   Cadence stabilization (decision 4 below) benefits from this same
   smallness.

**Per-spec un-comment slices to file as spillover:** five slices, one
per spec name (`082a-dashboard-spec-uncomment.md` ... `082e-...`). All
status `not-ready` until the harness PR merges and CI cadence has been
observed for ~5 PRs.

**Revisit once in use:** if the un-comment slices reveal a structural
gap in the harness (e.g., the fixture doesn't actually unblock a
spec's assertions because the API shape differs from what the spec
expected), that's a slice-082 follow-on — file a `082-harness-gap`
slice rather than retroactively expanding this one.

**Confidence: high.** Matches slice 069's pattern of shipping in
shimmed-then-un-comment phases.

### 3. AC-5 — branch-protection promotion — DEFER

**Options considered:**

- **(I)** Add `Frontend · Playwright e2e` to the
  `.github/branch-protection.json` required-checks list in THIS slice.
  Merge is blocked on a green Playwright job from day one.
- **(II)** Do NOT promote in this slice. Document the trajectory in
  this decisions log. Let cadence stabilize first (≥5 PRs of clean
  Playwright passes), then file a promotion slice.

**Chosen: (II).** Three reasons:

1. **No green track record yet.** The job has been quarantined since
   slice 079; the only un-quarantined runs are about to start.
   Promoting to required-on-first-day is a forward-looking commitment
   without evidence to back it.
2. **Convention.** Slices 065 (self-host bundle), 038 (Helm chart),
   069 (vitest), and 089 (vulnerability scanning) all ship
   non-required first and graduate after observed cadence. This slice
   inherits that convention.
3. **Cheap reversibility.** A promotion slice is ~5 lines of JSON +
   the corresponding PR description. There is no engineering cost to
   doing it as its own slice once the data is in.

**Trajectory:** file `082-playwright-required-check.md` (status
`not-ready`) as a spillover. Status flips to `ready` after the
Playwright job runs clean on ≥5 consecutive PRs post-merge. Promotion
PR is a one-line JSON edit + a CHANGELOG line.

**Confidence: high.** Matches the broader pattern.

### 4. BEARER_HASH_KEY in CI — deterministic test value, not a secret

**Options considered:**

- **(α)** Generate a fresh BEARER_HASH_KEY per job run (e.g.
  `openssl rand -hex 32` in a setup step). Maximally hostile to
  cross-job leakage.
- **(β)** Hard-code a 32+ byte test string in the workflow env. The
  harness uses the same string to HMAC the test bearer. The two
  values MUST match or the lookup fails.

**Chosen: (β).** Reasoning: the BEARER_HASH_KEY in this job is NOT a
secret. It only protects the disposable CI Postgres instance from
pre-image attacks against the token hash; that Postgres instance is
torn down at job end and contains exactly one token (`test-bearer-e2e`)
that is itself in the workflow env in plaintext. A random key per run
would force the harness and the atlas server to communicate the key
out-of-band (via the env, which is exactly what we're doing now). All
the random-key path buys is operational complexity.

The hard-coded value (`test-bearer-hash-key-32-bytes-ok!!`) is 33
ASCII bytes (≥32 minimum per
`internal/auth/bearer/bearer.go HashKeyMinBytes`) and contains no
vendor-prefix substring that would trip GitGuardian (P0-A3).

**Confidence: high.**

### 5. Fixture authoring depth — minimal-viable, not maximal-coverage

The dashboard fixture is the worked example: full inserts for the
risk + drift + freshness + exception preconditions the spec preamble
calls out. The other four fixtures (`control-detail`, `audit-workspace`,
`risk-hierarchy`, `admin-bootstrap`) are intentionally lighter:

- `control-detail.sql` is a BEGIN/COMMIT stub with no extra inserts.
  The seeded control (`33333333-3333-3333-3333-333333330001`) IS the
  control-detail target; AC-6/AC-7 (multi-framework + OOS) need scf
  anchor / fw_to_scf_edges / framework_scopes rows that involve the
  bundled SCF catalog and are deferred to the per-spec un-comment
  slice.
- `audit-workspace.sql` adds one frozen audit_period + one
  auditor_assignment.
- `risk-hierarchy.sql` adds parent/child org_units + one
  tenant-private theme + one aggregation_rule + one future-revisit
  decision + one overdue decision.
- `admin-bootstrap.sql` adds two feature_flags.

This minimum is enough for the `beforeAll` hook to exercise the
harness end-to-end (the goal of slice 082) without bloating the
fixture surface ahead of the assertions that use it. When each
per-spec un-comment slice (the spillovers from decision 2) lands, it
extends ITS fixture as needed.

**Confidence: high.**

### 6. Tenant scoping — one demo tenant only

Every fixture writes into `00000000-0000-0000-0000-00000000d3a0`
(`demo-tenant` from `fixtures/walkthroughs/00-seed.sql`). The
`alt-tenant` UUID used by `rls-isolation.sql` is NOT seeded by the e2e
harness — the e2e specs do not exercise cross-tenant isolation; that's
the RLS walkthrough's job.

Consequence: any future e2e spec that wants to assert cross-tenant
isolation will need a new fixture (`fixtures/e2e/rls-isolation.sql`?)
plus a second api_keys row. Until that spec exists, the harness stays
single-tenant.

**Confidence: high.**

## Post-merge follow-ups

1. **Per-spec un-comment slices** (decision 2): file five slices
   `082a-dashboard-spec-uncomment.md` through `082e-admin-bootstrap-
spec-uncomment.md`. All status `not-ready` until the harness PR
   merges and CI cadence has been observed for ~5 PRs.
2. **Branch-protection promotion slice** (decision 3): file
   `082-playwright-required-check.md` (status `not-ready`). Promotes
   `Frontend · Playwright e2e` to `.github/branch-protection.json`'s
   required-checks list after ≥5 clean post-merge runs.
3. **Watch the cadence.** If the harness's `seedFromFixture` call
   itself flakes (psql process exits non-zero on a network blip /
   role-bootstrap race), the warning will appear in the Playwright
   step output. File a `082-harness-resilience` follow-on if a
   non-seed-data flake mode emerges.

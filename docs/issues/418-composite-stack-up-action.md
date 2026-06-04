# 418 — Extract the 4× duplicated Playwright/Postgres stack bring-up into a composite action

**Cluster:** Infra
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready` (no unmerged deps — all four call-sites are on main)

## Narrative

**WHY.** The MinIO + NATS JetStream + Postgres + role-bootstrap + forward-migrations +
atlas-server-build/start + JWT-mint bring-up is **copy-pasted across four jobs** in
`.github/workflows/ci.yml`:

| Job                              | Approx. line                                                                | Bring-up it duplicates                             |
| -------------------------------- | --------------------------------------------------------------------------- | -------------------------------------------------- |
| `tests-integration`              | ~255 (`Start MinIO`) / ~278 (`Start NATS`) / ~291-306 (roles + migrations)  | MinIO + NATS + Postgres + roles + migrations       |
| `frontend-playwright`            | ~1206 (`Start MinIO`) / ~1222 (`Start NATS`) / ~1257 (`Start atlas server`) | MinIO + NATS + Postgres + migrations + atlas + JWT |
| `frontend-playwright-prod-build` | ~1438 / ~1454 / ~1496                                                       | same stack                                         |
| `frontend-ui-honesty`            | ~1651 / ~1667 / ~1699                                                       | same stack                                         |

That is roughly **400 duplicated lines**. The duplication is a latent correctness risk: a
fix to the bring-up — e.g. a readiness-loop timing bug, a `pg_isready -d security_atlas`
correction (exactly the slice-200 fix), a MinIO bucket-create race, or the
`ATLAS_KEYSTORE_PATH=/tmp/...` EACCES workaround (slice 201) — must be applied in **four
places**, and history shows these fixes routinely miss a copy (the local-vs-CI delta and
bring-up-ordering bugs in slices 200/201/202 each touched bring-up steps that exist in
multiple jobs). One canonical bring-up means one place to fix.

**WHAT.** Extract a **composite action** at `.github/actions/atlas-stack-up/action.yml` that
encapsulates the shared bring-up (start MinIO + create bucket, start NATS, bootstrap
Postgres roles, apply forward migrations, optionally build + start the atlas server with the
test JWT signer wired). Parameterize the per-job differences via `inputs:` (e.g.
`start-atlas: true|false` — the integration job runs tests in-process and may not need the
HTTP server; the Playwright jobs do; `mint-jwt`, `data-dir`, `keystore-path`, bucket name).
Replace the inlined steps in all four jobs with a `uses: ./.github/actions/atlas-stack-up`
call. **Pure refactor — behavior-preserving:** every job's resulting stack must be
byte-for-byte equivalent in effect to today's inlined version.

**SCOPE DISCIPLINE.** Does NOT change WHAT any job tests, the migrations applied, the role
model, or the JWT-mint mechanism — only WHERE the bring-up lives. Does NOT touch the
slice-061 stub-twin siblings (they have no bring-up). Does NOT consolidate steps that are
genuinely job-specific (web-workspace build, Playwright chromium install, the actual test
invocation) — only the shared infra bring-up. If a job's bring-up has a _deliberate_
divergence (e.g. the integration job's `Bootstrap roles` vs the Playwright jobs'
`Bootstrap Postgres roles + migrations` combined step), the composite action's inputs must
preserve that divergence, not paper over it.

## Threat model

STRIDE pass. A composite action is a supply-chain + correctness surface: it becomes the
single point through which every Playwright/integration job's stack is built, so a bug or a
behavior change in it ripples to four required-or-advisory checks at once. A "behavior-
preserving refactor" that silently _changes_ behavior (e.g. relaxes a readiness check) can
mask real failures — a genuine Integrity threat.

**S — Spoofing.** The composite action mints the test JWT via the existing
`/v1/test/issue-jwt` env-gated path (slice 201). No new identity surface; the
`ATLAS_TEST_MODE` gating MUST be preserved so the runtime mint path is never enabled outside
CI. P0-3 forbids leaking the test-mint path into a non-test job.

**T — Tampering (behavior-preserving integrity — the load-bearing threat).**

- **T-1 (the refactor silently changes bring-up behavior).** If the composite action's
  readiness loop, migration ordering, or role-password wiring differs from the inlined
  original — even subtly (e.g. dropping a `-v ON_ERROR_STOP=1`, changing a `pg_isready` DB
  name, shortening a retry budget) — a job could start passing tests against a half-built
  stack, masking a real failure, OR start flaking. This is the exact failure the slice-200
  `pg_isready -d security_atlas` fix and the slice-202 staged-bring-up sentinel were about.
  Mitigation: AC-5 requires a behavior-equivalence diff (the composite action's effective
  steps == the inlined steps it replaces, parameter-by-parameter) recorded in the decisions
  log; AC-6 requires all four jobs green on the slice's own PR (the four jobs ARE the
  regression test).
- **T-2 (an input default flips a job's stack shape).** A wrong `inputs:` default (e.g.
  `start-atlas` defaulting wrong) could give a job a stack it did not have. Mitigation:
  P0-2 — every call-site passes EXPLICIT inputs for any value where the four jobs differ; no
  reliance on a default that hides a per-job difference.

**R — Repudiation.** No audit-trail surface (CI infra). Per-job logs still show the
composite action's steps expanded. No regression.

**I — Information disclosure.** No tenant data. The CI-scoped role passwords + the test JWT
key are set the same way as today and stay job-env-scoped. The composite action MUST NOT
echo secrets to logs (AC-7). Anti-criterion P0-4 forbids printing the role password or JWT
key.

**D — Denial of service.** Consolidation can only reduce, not increase, bring-up surface.
No unbounded input. One residual: a bug in the single composite action now fails four jobs
instead of one — but that is the _intended_ "fix once" property; the regression test (AC-6,
four jobs green) bounds it.

**E — Elevation of privilege.** The `atlas_app` / `atlas_migrate` role bootstrap is moved,
not changed; the same BYPASSRLS/CREATEROLE grants apply. No privilege boundary moves. P0-5
asserts the role model is byte-equivalent.

**Verdict: has-mitigations.** The one real threat is T-1 (a "pure refactor" that isn't);
it is bounded by AC-5 (behavior-equivalence diff) + AC-6 (four jobs green = the regression
suite). Net surface shrinks ~400 lines.

## Acceptance criteria

- [ ] **AC-1.** A composite action exists at `.github/actions/atlas-stack-up/action.yml`
      encapsulating the shared bring-up: start MinIO + create bucket, start NATS JetStream,
      bootstrap Postgres roles, apply forward migrations, and (input-gated) build + start the
      atlas server with the test JWT signer wired.
- [ ] **AC-2.** The action exposes `inputs:` covering every per-job difference observed
      across the four call-sites (at minimum: `start-atlas`, `mint-jwt`/test-mode,
      `data-dir`, `keystore-path`, MinIO bucket name) with documented defaults.
- [ ] **AC-3.** `tests-integration` calls the composite action in place of its inlined
      `Start MinIO` / `Start NATS JetStream` / `Bootstrap roles` / `Apply forward migrations`
      steps.
- [ ] **AC-4.** `frontend-playwright`, `frontend-playwright-prod-build`, and
      `frontend-ui-honesty` each call the composite action in place of their inlined bring-up
      steps.
- [ ] **AC-5.** The decisions log records a step-by-step behavior-equivalence diff showing the
      composite action reproduces each replaced step's effect (same migration order, same
      readiness checks, same role-password wiring, same JWT-mint path) — no silent behavior
      change.
- [ ] **AC-6.** All four jobs (`Go · integration (Postgres RLS)`, `Frontend · Playwright e2e`,
      `Frontend · Playwright e2e (prod-build standalone)`, `Frontend · UI honesty (advisory)`)
      pass on the slice's own PR — the four jobs are the refactor's regression suite.
- [ ] **AC-7.** The composite action does NOT echo the CI role password or the test JWT
      signing key to the job log (no `echo "$ATLAS_APP_PASSWORD"`, no key material in logs).
- [ ] **AC-8.** Net `ci.yml` line count drops materially (target: ~300+ lines removed across
      the four jobs); the removed lines are the duplicated bring-up, not job-specific logic.
- [ ] **AC-9.** Job-specific steps NOT part of the shared bring-up (web-workspace build,
      Playwright chromium install, the test invocation, coverage upload) remain inline in
      their respective jobs — the action is bring-up-only.
- [ ] **AC-10.** Any `uses:` inside the composite action (and the call-sites' `uses: ./...`
      local reference) satisfy slice 128 `actions-pin-check` (third-party actions SHA-pinned;
      local action reference is path-based, which the pin-check allows for `./`-prefixed uses).
- [ ] **AC-11.** The slice-061 stub-twin jobs for the affected required checks still resolve
      green on docs-only PRs (the action only runs in the real jobs, never the stubs).

## Constitutional invariants honored

- Tech-stack lock (CLAUDE.md): the stack components (Postgres 16+, MinIO/S3-compatible, NATS
  JetStream) and the migration tooling are unchanged — only the bring-up's location moves.
- Invariant #6 (RLS): the `atlas_app` / `atlas_migrate` role bootstrap (the foundation RLS
  enforcement rests on) is preserved byte-for-byte (P0-5).
- Supply-chain SHA-pinning (slice 128): preserved across the action and its call-sites.

## Canvas references

- CLAUDE.md "Testing discipline (four enforced surfaces)" — the four jobs whose bring-up this
  consolidates.
- `Plans/canvas/09-tech-stack.md` (deployment + observability stack — the services the
  bring-up starts).

## Dependencies

- None unmerged. All four call-sites (slices 069/082/178/387 lineage) are on main.
- **#417** — soft pair (the integration shard wants this action so its 2-3 shards don't each
  re-duplicate the bring-up). Neither blocks the other; if 418 lands first, 417 calls the
  action; if 417 lands first, 418 also consolidates 417's new shard call-sites.

## Anti-criteria (P0 — block merge)

- **P0-1 (security — T-1).** Behavior-preserving ONLY. The composite action MUST reproduce
  each replaced step's effect exactly; AC-5's equivalence diff + AC-6's four-green-jobs are
  the proof. No silent change to readiness checks, migration order, or role wiring.
- **P0-2 (security — T-2).** Every per-job difference is passed as an EXPLICIT input at the
  call-site; no job relies on a default that hides a real divergence between the four.
- **P0-3 (security — S).** The `ATLAS_TEST_MODE` / `/v1/test/issue-jwt` runtime-mint path
  stays env-gated and CI-only; the action MUST NOT enable the test-mint path in any context
  that is not already a test job.
- **P0-4 (security — I).** The action MUST NOT print the CI role password or JWT signing key
  to logs.
- **P0-5 (security — E).** The `atlas_app` / `atlas_migrate` role bootstrap (grants, BYPASSRLS
  flags) is byte-equivalent to today's; the refactor does not alter the role model.
- **P0-6.** Does NOT consolidate job-specific steps (test invocation, chromium install,
  coverage upload) — bring-up only.
- **P0-7.** Does NOT auto-merge; maintainer reviews the equivalence diff.

## Skill mix (3-5)

`ci-cd-pipeline-builder` · `git-worktree-manager` (n/a) · `monorepo-navigator` ·
`simplify` · `grill-with-docs`.

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ GitHub "composite action" (a `.github/actions/<name>/action.yml` with
  `runs.using: composite`) — NOT a reusable _workflow_ (`workflow_call`). The composite action
  is the right tool: it inlines steps into the calling job (sharing the job's services, env,
  and runner), which the bring-up needs (it sets `$GITHUB_ENV` vars the later test steps
  read). A reusable workflow would run in a separate job and break that env-sharing.
- _Scope._ The four jobs' bring-ups are NOT identical — the integration job may not need the
  HTTP atlas server; the Playwright jobs do. The composite action must parameterize this, not
  force-merge the difference (P0-2). Audit all four before extracting so no real divergence is
  lost.
- _Already-built check._ `rg -l "composite action|atlas-stack" docs/issues/` returns nothing.
  First slice to consolidate the bring-up.

**Threat-model context (Phase 3).** The whole risk is "a refactor advertised as
behavior-preserving that quietly isn't." Slices 200/201/202 are the cautionary history — each
was a bring-up bug (pg_isready DB name, EACCES on keystore path, staged-bring-up race) that
existed in bring-up code now slated for consolidation. Build AC-5 (line-by-line equivalence)
honestly; the four-jobs-green check (AC-6) is necessary but not sufficient on its own (a green
run against a subtly-degraded stack is exactly T-1).

**Implementation note.** Diff the four bring-up blocks first (they have drifted — e.g. the
integration job's `Bootstrap roles` is a separate step from `Apply forward migrations`, while
`frontend-playwright` combines them into `Bootstrap Postgres roles + migrations`). The
composite action's job is to make those the same WHERE they should be the same and an input
WHERE they should differ. Record every drift you collapse in the decisions log so a reviewer
can confirm none was a deliberate divergence.

**Provenance.** Filed 2026-06-03 in the CI-backlog batch (415-420). Pairs with 417 (which
would otherwise re-duplicate the bring-up across its shards).

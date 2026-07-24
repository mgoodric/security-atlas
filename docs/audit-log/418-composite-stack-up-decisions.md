# Slice 418 — composite stack-up action decisions log

**Type:** JUDGMENT. The action's input surface, the two-call shape at the
Playwright call-sites, and the scope of the `actions-pin-check` extension are
subjective calls made here and recorded for post-deployment revisit. No human
sign-off gate.

- detection_tier_actual: none
- detection_tier_target: none

(No product bug surfaced. This is a pure CI refactor. Its own regression suite
is AC-6 — the four jobs the action now feeds. The equivalence diff below is the
static half of the same proof; the four green jobs are the dynamic half.)

---

## The call-site table is stale — read this first

The slice spec (`docs/issues/418-composite-stack-up-action.md`) names
`tests-integration` as the fourth call-site, at ~line 255. That was true when
418 was filed (2026-06-03). **Slice 417 landed in between** and moved the whole
Go-integration bring-up out of `tests-integration` and into the new
`tests-integration-shard` matrix job; `tests-integration` is now a pure fan-in
job (artifact download + `gocovmerge` + the coverage gate) with no bring-up at
all.

This is exactly the case the spec's Dependencies section anticipated: "if 417
lands first, 418 also consolidates 417's new shard call-sites." So the four
call-sites this slice actually rewires are:

| Job                              | Bring-up it inlined                                    | Lines removed |
| -------------------------------- | ------------------------------------------------------ | ------------- |
| `tests-integration-shard`        | MinIO + bucket, NATS, roles, role password, migrations | 48            |
| `frontend-playwright`            | the above (as 3 steps) + atlas server                  | 58            |
| `frontend-playwright-prod-build` | same                                                   | 54            |
| `frontend-ui-honesty`            | same                                                   | 58            |
|                                  | **total inlined bring-up removed**                     | **223**       |

Consolidating into `tests-integration-shard` is strictly better than the spec's
original target: the matrix runs six legs (`A`, `B1`, `B2`, `B3`, `B4`, `B5`),
so one edit to the action now reaches six runners' worth of bring-up through a
single call-site rather than one.

---

## Divergence audit (Do-step 2)

Diffing the four inlined bring-ups against each other yields exactly **two**
differences. Everything else is character-identical.

### Genuine divergence 1 — the atlas HTTP server

`tests-integration-shard` has no `Start atlas server` step; the three Playwright
jobs do. The integration tests drive the handlers in-process against Postgres;
the Playwright jobs need a real HTTP server for the browser and the BFF to talk
to. This is the deliberate divergence the spec predicted, and it is the
`start-atlas` input. Passed explicitly at all four call-sites (P0-2).

### Genuine divergence 2 — the test-mint gate

`frontend-playwright` and `frontend-playwright-prod-build` set
`ATLAS_TEST_MODE: "1"` and `ATLAS_ISSUER_URL: http://localhost:8080` at job
level, which mounts `POST /v1/test/issue-jwt` on the atlas server so the
Playwright `globalSetup` can mint a JWT at runtime (slice 201).
`frontend-ui-honesty` sets **neither** — it authenticates with the static
`TEST_BEARER: test-bearer-e2e` (slice 069/178 shape). This is the `atlas-test-mode`
/ `atlas-issuer-url` input pair, passed explicitly at every call-site including
the empty values at `frontend-ui-honesty` (P0-2/P0-3). See D3 for why the
job-level `env:` blocks were left in place rather than moved into the inputs.

### Cosmetic divergence — step grouping (collapsed, not a behavior change)

`tests-integration-shard` splits the Postgres work into three named steps
(`Bootstrap roles` / `Set role passwords (CI-scoped)` / `Apply forward
migrations`); the three Playwright jobs run the identical three command groups
in the identical order inside one step named `Bootstrap Postgres roles +
migrations`. The commands, their order, and the `$GITHUB_ENV` write are
character-identical between the two shapes. The action uses the three-step form
(the more granular one, better failure attribution). Nothing observable changes:
`$GITHUB_ENV` is materialized for _subsequent_ steps in both shapes, and nothing
between the three groups reads `DATABASE_URL_APP`.

**No divergence turned out to be a latent bug**, so the spec's `AGENT_BLOCKED`
branch was not taken. Notably the slice-200 `pg_isready -d security_atlas` fix
and the slice-201 `ATLAS_KEYSTORE_PATH=/tmp/...` EACCES workaround were already
present in _every_ copy — the drift the slice feared had not (yet) occurred.
Values verified identical across all four call-sites and now defaults on the
action: bucket `atlas-artifacts-test`, MinIO health
`http://localhost:9000/minio/health/live`, NATS health
`http://localhost:8222/healthz`, atlas health `http://localhost:8080/health`,
readiness budget 30 attempts × `sleep 2`, keystore `/tmp/atlas-ci/keys`, data dir
`/tmp/atlas-ci`, log path `/tmp/atlas.log`.

---

## Behavior-equivalence diff (AC-5 / P0-1)

Step by step, inlined original → composite action step. "Identical" means the
command text, argument order, readiness budget, and `$GITHUB_ENV` writes match
character-for-character.

### 1. `Start MinIO` → action step `Start MinIO`

| Aspect                | Inlined (all four)                                                                                                                 | Action                                                                         | Same? |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ----- |
| Container run         | `docker run -d --name minio -p 9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio server /data` | identical                                                                      | yes   |
| Readiness loop        | `for i in $(seq 1 30)` / `curl -sf http://localhost:9000/minio/health/live` / `sleep 2`                                            | identical                                                                      | yes   |
| Loop exit on timeout  | falls through without failing (no `exit 1`)                                                                                        | identical — deliberately NOT hardened                                          | yes   |
| Bucket create         | `minio/mc mb -p local/atlas-artifacts-test`                                                                                        | `mc mb -p "local/${ATLAS_MINIO_BUCKET}"`, input default `atlas-artifacts-test` | yes   |
| `MC_HOST_local` alias | `http://minioadmin:minioadmin@localhost:9000`                                                                                      | identical                                                                      | yes   |

The only textual change is the bucket name moving from a literal to an
input-fed env var. It is passed through `env:` rather than `${{ }}`-interpolated
into the script body, so an input value can never be spliced into the shell as
code. Resolved value is unchanged at every call-site.

**Explicitly NOT changed:** the readiness loop still does not fail the step when
all 30 attempts miss. Hardening it (`exit 1` after the loop) is a behavior change
and would have been a P0-1 violation. It is a reasonable follow-up slice but is
NOT this one.

### 2. `Start NATS JetStream` → action step `Start NATS JetStream`

| Aspect         | Inlined (all four)                                                                                    | Action    | Same? |
| -------------- | ----------------------------------------------------------------------------------------------------- | --------- | ----- |
| Container run  | `docker run -d --name nats -p 4222:4222 -p 8222:8222 nats:2.10-alpine -js -sd /data --http_port 8222` | identical | yes   |
| Readiness loop | `for i in $(seq 1 30)` / `curl -sf http://localhost:8222/healthz` / `sleep 2`                         | identical | yes   |

Zero textual change. Copied verbatim.

### 3. Role bootstrap → action step `Bootstrap roles`

| Aspect          | Inlined                                                                        | Action                                                            | Same? |
| --------------- | ------------------------------------------------------------------------------ | ----------------------------------------------------------------- | ----- |
| Command         | `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/bootstrap/01-roles.sql` | identical                                                         | yes   |
| Role DSN        | `$DATABASE_URL` (the superuser/`postgres` DSN), NOT `DATABASE_URL_APP`         | identical                                                         | yes   |
| `ON_ERROR_STOP` | `1`                                                                            | identical                                                         | yes   |
| Working dir     | job workspace (relative path)                                                  | `${{ github.workspace }}` — the same directory, stated explicitly | yes   |

`migrations/bootstrap/01-roles.sql` is **not touched by this slice** (P0-5). The
`atlas_app` / `atlas_migrate` definitions, their grants, and the `BYPASSRLS`
flags are byte-identical to `main`; `git diff` shows no change under
`migrations/`. Constitutional invariant 6 rests on that file and it did not move.

### 4. Role password → action step `Set role passwords (CI-scoped)`

| Aspect              | Inlined                                                                                                   | Action                                                                    | Same? |
| ------------------- | --------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ----- |
| Password source     | job-level `ATLAS_APP_PASSWORD: ${{ secrets.CI_ATLAS_APP_PASSWORD \|\| 'ci-ephemeral' }}`                  | unchanged — still job-level env, read by the action from the caller's env | yes   |
| `ALTER ROLE`        | `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "ALTER ROLE atlas_app PASSWORD '$ATLAS_APP_PASSWORD'"`        | identical                                                                 | yes   |
| Which role          | `atlas_app` only (`atlas_migrate` keeps its bootstrap-file password)                                      | identical                                                                 | yes   |
| `$GITHUB_ENV` write | `DATABASE_URL_APP=postgres://atlas_app:$ATLAS_APP_PASSWORD@localhost:5432/security_atlas?sslmode=disable` | identical                                                                 | yes   |
| Secret in log       | never echoed                                                                                              | never echoed                                                              | yes   |

The `$GITHUB_ENV` write from inside a composite action propagates to subsequent
steps of the **calling job** exactly as it did from an inline step — this is the
load-bearing reason the extraction is a composite action and not a
`workflow_call` reusable workflow (which would run in a separate job and break
the propagation). The consumers of `DATABASE_URL_APP` — the integration test
step and the atlas server — are unchanged.

### 5. Forward migrations → action step `Apply forward migrations`

| Aspect           | Inlined                                              | Action    | Same? |
| ---------------- | ---------------------------------------------------- | --------- | ----- |
| Glob             | `migrations/sql/*.sql`                               | identical | yes   |
| Order            | shell lexical glob order — **not** `sort`, not `ls`  | identical | yes   |
| Down-file skip   | `case "$f" in *.down.sql) ;; ...`                    | identical | yes   |
| Per-file command | `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"`    | identical | yes   |
| Progress output  | `echo "applying $f"` (file name only, no secrets)    | identical | yes   |
| Connecting role  | `$DATABASE_URL` (migrate/superuser), NOT the app DSN | identical | yes   |

Migration order is the single most load-bearing thing in this diff and it is
reproduced by copying the loop verbatim. No `sort`, no `find`, no reordering.

### 6. `Start atlas server` → action step `Start atlas server` (gated on `start-atlas`)

| Aspect                | Inlined (3 Playwright jobs)                                         | Action                                                                                            | Same?     |
| --------------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | --------- |
| `ATLAS_KEYSTORE_PATH` | step env `/tmp/atlas-ci/keys`                                       | step env from `keystore-path`, passed explicitly as `/tmp/atlas-ci/keys`                          | yes       |
| `ATLAS_DATA_DIR`      | step env `/tmp/atlas-ci`                                            | step env from `data-dir`, passed explicitly as `/tmp/atlas-ci`                                    | yes       |
| Directory create      | `mkdir -p /tmp/atlas-ci/keys`                                       | `mkdir -p "$ATLAS_DATA_DIR" "$ATLAS_KEYSTORE_PATH"` — with these inputs, the same two directories | yes       |
| Launch                | `atlas serve > /tmp/atlas.log 2>&1 &`                               | identical                                                                                         | yes       |
| PID export            | `echo "ATLAS_PID=$!" >> "$GITHUB_ENV"`                              | identical                                                                                         | yes       |
| Readiness loop        | 30 × `curl -sf http://localhost:8080/health` / `sleep 2`            | identical, URL from `atlas-health-url` (passed explicitly as the same URL)                        | yes       |
| Log path              | `/tmp/atlas.log` (read by each job's `Dump server logs on failure`) | identical, still a literal so the two ends cannot drift                                           | yes       |
| `ATLAS_TEST_MODE`     | inherited from job env (`"1"` / absent)                             | step env from `atlas-test-mode` input                                                             | see below |
| `ATLAS_ISSUER_URL`    | inherited from job env (URL / absent)                               | step env from `atlas-issuer-url` input                                                            | see below |

`mkdir -p /tmp/atlas-ci/keys` implicitly created `/tmp/atlas-ci` as well; naming
both paths creates exactly the same set for these inputs and stays correct if a
future call-site points the keystore outside the data dir.

**The two env vars are the one place a reviewer should look hardest.** For the
two jobs that set them at job level, the step-scoped values are identical
strings — a no-op override. For `frontend-ui-honesty`, which sets neither, the
step-scoped values are the empty string where previously the vars were _unset_.
That distinction is provably immaterial for both:

- `ATLAS_TEST_MODE` — `internal/api/testissuejwt.go` gates on
  `os.Getenv(testModeEnvVar) != "1"` (and `internal/api/register_platform.go`
  gates mounting on the same `== "1"`). Empty and unset both fail that test, so
  `/v1/test/issue-jwt` stays unmounted and 404s on `frontend-ui-honesty` exactly
  as it does today. This is the P0-3 property: the runtime-mint path does not
  reach a job that did not already have it.
- `ATLAS_ISSUER_URL` — `cmd/atlas/main.go:337` gates on
  `if issuer := os.Getenv("ATLAS_ISSUER_URL"); issuer != ""`. Empty and unset are
  the same branch.

The project already relies on this equivalence outside CI:
`deploy/docker/docker-compose.yml` ships `ATLAS_TEST_MODE: ${ATLAS_TEST_MODE:-}`
and `docker-compose.edge.yml` ships `ATLAS_TEST_MODE: ""` as their production-safe
_disabled_ posture.

### 7. Steps NOT moved (AC-9 / P0-6)

Left inline, unchanged, in their original positions:

- `tests-integration-shard`: `Install cosign`, the two slice-425 LocalStack KMS
  steps, `Audit RLS coverage (slice 033)`, `Run integration shard`, both slice
  417/461 guard steps, the Codecov + artifact uploads, the wall-clock watermark,
  and the migration round-trip.
- All three Playwright jobs: `Download prebuilt atlas binary`, `Install prebuilt
atlas binary`, `Install web workspace`, `Build web workspace`
  (`build:standalone` on the prod-build leg), `Install Playwright chromium`,
  `Start web server`, the test invocation, report upload, and
  `Dump server logs on failure`.
- `frontend-ui-honesty` additionally keeps `Install workspaces`, `Validate
mockup-spec manifest`, and the entire slice-699 baseline/sticky-comment chain.

### 8. Step ordering — unchanged in all four jobs

Verified mechanically by parsing the rewritten `ci.yml` and printing each job's
step sequence: every job's steps appear in the same order as on `main`, with the
inlined bring-up steps replaced in place. No step moved earlier or later. The
composite action inlines its steps at the call-site, so the expanded runtime
sequence is identical too.

---

## Decisions made

### D1 — Composite action, not a reusable workflow. Confidence: high.

The bring-up writes `DATABASE_URL_APP` and `ATLAS_PID` into `$GITHUB_ENV` for
later steps in the _same_ job, and it depends on the caller's `services.postgres`
container and job-level `env:`. A composite action inlines into the calling job
and inherits all three. A `workflow_call` reusable workflow runs as a separate
job with its own runner and its own env — it would have broken every one of
those couplings. Not a close call; recorded because the spec's grill output
raised it.

### D2 — Two calls per Playwright job, not one. Confidence: medium-high.

In the three Playwright jobs the bring-up is genuinely **interrupted** by
job-specific steps: MinIO/NATS/Postgres run early, then `npm ci` + the web build

- chromium install, and only then does the atlas server start. A composite
  action cannot interleave with the caller's own steps, so a single call would
  have forced the atlas server to boot before the web build.

Options considered:

1. **One call, atlas started early.** Rejected. The server would come up ~4
   minutes earlier and sit idle through the npm build. Probably harmless — but
   "probably harmless reordering" is precisely the T-1 threat this slice exists
   to avoid, and P0-1 says reproduce exactly.
2. **Two composite actions** (`atlas-services-up` + `atlas-server-up`). Rejected:
   AC-1 specifies one action at one path.
3. **Two calls to the one action**, phase-selected by `start-services` /
   `start-atlas`. **Chosen.** Preserves ordering byte-for-byte, keeps a single
   action file, and makes the phase split legible at the call-site.

The cost is that `start-services` is a phase toggle rather than a behavioral
knob — a slightly awkward input. **Revisit** if a future refactor moves the web
build after the atlas start, at which point the two calls collapse into one and
`start-services` can be dropped.

### D3 — `ATLAS_TEST_MODE` stays in the job `env:` block AND becomes an input. Confidence: high.

The tempting cleanup — delete `ATLAS_TEST_MODE` from the job-level `env:` and
let the action's input be its only source — is **wrong**, and finding out why is
the most useful thing this slice turned up.

`ATLAS_TEST_MODE` is read by _two_ processes, not one:

- the Go atlas server (`internal/api/testissuejwt.go`), which the action starts; and
- the **Next.js web server**, at
  `web/app/(authed)/dashboard/dashboard-prefetch.ts:92` —
  `return process.env.ATLAS_TEST_MODE === "1"` — which gates the dashboard
  server-prefetch bypass the e2e specs depend on (see the comments in
  `web/e2e/dashboard.spec.ts` and `metrics-correctness-consistency.spec.ts`).
  The web server is started by the job's own `Start web server` step, which
  inherits the job env, not the action's step env.

Scoping the var to the action's step would therefore have silently changed the
web server's prefetch behavior in the two Playwright jobs — a textbook T-1
"behavior-preserving refactor that isn't", and one the four-green-jobs check
might well not have caught. So: the job-level declarations stay untouched, and
the input is an _additional_ explicit assertion of the same value on the atlas
process. Yes, that means the value appears twice for those two jobs. The
duplication is the price of P0-1 correctness and is cheap next to the risk.

### D4 — `actions-pin-check` extended, not exempted. Confidence: high.

`scripts/check-action-pins.sh` required every `uses:` to carry a 40-char SHA,
which the new `uses: ./.github/actions/atlas-stack-up` lines fail — verified by
running it before the fix (7 findings). AC-10 asserts the check passes and that
third-party `uses:` inside the action are SHA-pinned. Two changes, both minimal:

1. **`./`-prefixed refs are exempt and counted separately.** A local action has
   no `@<ref>` to pin; its code is checked out with the repo at the same commit
   the workflow runs from, so it is immutable by construction and carries none
   of the tag-jacking exposure the guard exists for.
2. **The scan now also covers `.github/actions/*/action.yml`.** This is the
   other half of the trade: exempting a local action from pinning _itself_ would
   otherwise turn any composite action into an un-audited hole where a
   tag-pinned third-party action could hide. An absent or empty actions
   directory is not an error (unlike the workflows directory).

Three new self-test cases in `scripts/check-action-pins_test.sh` pin the
behavior: a `./` ref passes and is counted as local; a tag-pinned third-party
action inside an `action.yml` fails with the offending line named; the same
shape SHA-pinned passes. Suite: 21 assertions, 0 failures.

The action itself currently contains **no** `uses:` at all — every step is a
`run:` — so there is nothing in it to pin today. The scan extension is what
keeps that true.

### D5 — Line-count target: 223 removed, not the spec's ~400. Confidence: high.

AC-8 targets "~300+ lines removed". Actual: **223 lines of duplicated bring-up
removed, 82 added, net −141** (`ci.yml`: 3720 → 3579).

The shortfall is entirely a consequence of the stale premise documented at the
top of this log. The spec's ~400 assumed four _full_ inlined bring-ups. Slice
417 had already collapsed the Go-integration side into a single matrix job, so
only 223 lines of bring-up remained to consolidate. There is no bring-up left in
`ci.yml` that this slice declined to extract — the duplication is now zero, which
is the property AC-8 was a proxy for. Reported honestly rather than inflated by
padding the diff.

Of the 82 added lines, 34 are the seven `uses:` blocks and 48 are their
explanatory comments; the per-call-site comments were deliberately trimmed once
the full rationale landed here and in the action's own header.

---

## Anti-criteria check

| Anti-criterion                                            | Status                                                                                                                                                                                                                                                                          |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P0-1 behavior-preserving only                             | met — section "Behavior-equivalence diff"; no readiness check, migration order, or role wiring changed                                                                                                                                                                          |
| P0-2 every per-job difference an explicit call-site input | met — `start-atlas`, `atlas-test-mode`, `atlas-issuer-url` passed at all four call-sites, including the empty values at `frontend-ui-honesty`                                                                                                                                   |
| P0-3 test-mint path stays env-gated + CI-only             | met — `atlas-test-mode` defaults to empty (off); only the two jobs that already had it pass `'1'`; D3                                                                                                                                                                           |
| P0-4 no role password / JWT key in logs                   | met — the password is interpolated into a `psql -c` argument and a `$GITHUB_ENV` line, never echoed; the migration loop echoes file names only; no `set -x`; no signing key is handled by the action at all (the server generates its own keystore under `ATLAS_KEYSTORE_PATH`) |
| P0-5 role bootstrap byte-equivalent                       | met — `migrations/bootstrap/01-roles.sql` untouched; the invoking command is character-identical                                                                                                                                                                                |
| P0-6 no job-specific steps consolidated                   | met — equivalence diff §7                                                                                                                                                                                                                                                       |
| P0-7 no auto-merge                                        | maintainer reviews this diff before merge                                                                                                                                                                                                                                       |

## Local verification run before push

- `bash scripts/check-action-pins.sh` → `no tag-pinned actions detected (183 pinned, 7 local across 9 files)`, exit 0
- `bash scripts/check-action-pins_test.sh` → 21 passed, 0 failed
- `actionlint -shellcheck "" -no-color .github/workflows/ci.yml` (the pre-commit hook's invocation) → exit 0
- `prettier --check` on `ci.yml` + `action.yml` → clean
- YAML parse of both files + a mechanical step-order dump of all four jobs → order matches `main`

AC-6 (four green jobs) and AC-11 (stub twins still resolve on docs-only PRs) are
CI-side and resolve on the slice's own PR.

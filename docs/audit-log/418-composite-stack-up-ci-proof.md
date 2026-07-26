# Slice 418 — composite stack-up action CI proof (AC-6 / AC-7 / AC-10)

**Type:** EVIDENCE. This file records the _dynamic_ half of the slice 418
behavior-preservation proof. The static half — the step-by-step equivalence
diff and the divergence audit — is in
`docs/audit-log/418-composite-stack-up-decisions.md`.

- detection_tier_actual: none
- detection_tier_target: none

(No defect surfaced. Every stack-bring-up job that now routes through
`.github/actions/atlas-stack-up` passed on both the slice's own PR and on the
merged state.)

---

## Why this file exists separately

Slice 418's AC-6 states the four stack-bring-up jobs _are_ the refactor's
regression suite: they must go green to prove no readiness check was silently
relaxed and no bring-up step masked a real failure. Making the change and
waiting on that proof were split — the change landed as PR #1456 (merge commit
`e610a69c`), and this file is the recorded proof.

Two dependencies had to be satisfied before a clean green run was meaningful:

- **PR #1456 merged.** Merged 2026-07-25 15:46:23 -0700 as `e610a69c`.
- **`internal/board` `TestGenerator_Generate_EndToEnd` fixed on `main`.** Landed
  as `df3aaa5f` ("fix(board): anchor risk test seeds to each test's own clock",
  PR #1458). Without it `main` stayed red for a reason unrelated to the
  refactor.

Both are on `main`, so the runs below are attributable to the refactor.

---

## Run A — the slice's own PR (#1456), the literal wording of AC-6

Run `30175584924` · `pull_request` · 2026-07-25T21:24:49Z · conclusion
`success`.

| Job (display name)                                                                     | Result  | URL                                                                                 |
| -------------------------------------------------------------------------------------- | ------- | ----------------------------------------------------------------------------------- |
| `Go · integration (Postgres RLS)` (`tests-integration`)                                | success | https://github.com/mgoodric/security-atlas/actions/runs/30175584924/job/89727669579 |
| `Frontend · Playwright e2e` (`frontend-playwright`)                                    | success | https://github.com/mgoodric/security-atlas/actions/runs/30175584924/job/89726926686 |
| `Frontend · Playwright e2e (prod-build standalone)` (`frontend-playwright-prod-build`) | success | https://github.com/mgoodric/security-atlas/actions/runs/30175584924/job/89726926691 |
| `Frontend · UI honesty (advisory)` (`frontend-ui-honesty`)                             | success | https://github.com/mgoodric/security-atlas/actions/runs/30175584924/job/89726926695 |
| `actions-pin-check` (slice 128, AC-10)                                                 | success | https://github.com/mgoodric/security-atlas/actions/runs/30175584924/job/89723734773 |

## Run B — the merged state

Run `30189797112` · `pull_request` (PR #1505, base `main`) ·
2026-07-26T05:41:42Z · conclusion `success`. The PR head `6068efb3` has
`e610a69c` as an ancestor, so this run exercises the composite action as it
sits on `main`.

| Job (display name)                                                                     | Result  | URL                                                                                 |
| -------------------------------------------------------------------------------------- | ------- | ----------------------------------------------------------------------------------- |
| `Go · integration (Postgres RLS)` (`tests-integration`)                                | success | https://github.com/mgoodric/security-atlas/actions/runs/30189797112/job/89761753232 |
| `Frontend · Playwright e2e` (`frontend-playwright`)                                    | success | https://github.com/mgoodric/security-atlas/actions/runs/30189797112/job/89761107212 |
| `Frontend · Playwright e2e (prod-build standalone)` (`frontend-playwright-prod-build`) | success | https://github.com/mgoodric/security-atlas/actions/runs/30189797112/job/89761107221 |
| `Frontend · UI honesty (advisory)` (`frontend-ui-honesty`)                             | success | https://github.com/mgoodric/security-atlas/actions/runs/30189797112/job/89761107220 |
| `actions-pin-check` (slice 128, AC-10)                                                 | success | https://github.com/mgoodric/security-atlas/actions/runs/30189797112/job/89760889387 |

---

## The jobs are real, not slice-061 stub twins

A green check name is not by itself proof. Every affected required check has a
slice-061 stub twin carrying the _same_ `name:`, which resolves green on
docs-only pushes. On a docs-only run the green `Frontend · Playwright e2e` is a
six-step job whose only work is
`echo "Docs-only change — skipped per dorny/paths-filter@v4 (slice 061)."`, and
the real job is the one reported `skipped`. The most recent `main` run at the
time of writing (`30191389903`, commit `da85ecf2`) is exactly that shape — it
proves nothing about the refactor and is deliberately _not_ cited above.

The runs cited above were checked at the step level. Each carries the composite
action's own steps:

| Job                                                 | Composite steps observed                                                                                                   |
| --------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `Frontend · Playwright e2e`                         | `Bring up the atlas CI stack (MinIO + NATS + Postgres)` [success]; `Start the atlas server (test-mint path on)` [success]  |
| `Frontend · Playwright e2e (prod-build standalone)` | `Bring up the atlas CI stack (MinIO + NATS + Postgres)` [success]; `Start the atlas server (test-mint path on)` [success]  |
| `Frontend · UI honesty (advisory)`                  | `Bring up the atlas CI stack (MinIO + NATS + Postgres)` [success]; `Start the atlas server (test-mint path OFF)` [success] |

`frontend-ui-honesty` showing **test-mint path OFF** is the direct dynamic
confirmation of P0-3: the UI-honesty leg uses the static `TEST_BEARER` and did
not gain the `/v1/test/issue-jwt` runtime mint.

`Go · integration (Postgres RLS)` is an aggregating gate — slice 417 moved the
bring-up into the `tests-integration-shard` matrix, which the decisions log
already records as the stale-call-site-table correction. All six shards ran the
composite action and passed, in both runs:

- Run A: shards A, B1–B5 → jobs `89724257776`, `89724257798`, `89724257794`,
  `89724257786`, `89724257803`, `89724257782` — each `success`, each with
  `Bring up the atlas CI stack (MinIO + NATS + Postgres)` [success].
- Run B: shards A, B1–B5 → jobs `89760931737`, `89760931743`, `89760931749`,
  `89760931742`, `89760931755`, `89760931734` — same.

The gate job asserts `needs.tests-integration-shard.result == 'success'`, so a
green gate is not reachable with a skipped or red shard.

---

## Secret hygiene on these runs (AC-7 / P0-4)

Job logs for all eleven jobs across both runs were downloaded and scanned. No
JWT signing key material, no PEM private key, no serialized JWT, and no
expanded `DATABASE_URL_APP` DSN appears in any of them.

The composite action's `Set role passwords (CI-scoped)` step echoes the command
**unexpanded** — the log line is literally
`-c "ALTER ROLE atlas_app PASSWORD '$ATLAS_APP_PASSWORD'"`, followed only by
psql's `ALTER ROLE` acknowledgement. The action satisfies AC-7 / P0-4 as
written: it does not print the password or the key.

One honest caveat, recorded rather than glossed: the runner's own per-step
`env:` banner prints `ATLAS_APP_PASSWORD: ci-ephemeral`. This is **not** a
finding against slice 418:

- `ci-ephemeral` is the public fallback literal committed at `ci.yml:326` and
  four sibling job-level `env:` blocks. It is what the expression
  `${{ secrets.CI_ATLAS_APP_PASSWORD || 'ci-ephemeral' }}` evaluates to when the
  repository secret is unset. That it renders unmasked is itself the proof the
  secret is unset — a configured secret is masked, as the sibling
  `DATABASE_URL: ***localhost:5432/...` line in the same banner shows.
- The five job-level `env:` declarations are byte-identical at `e610a69c^` and
  `e610a69c`. The refactor neither introduced nor moved them; the banner
  behaves exactly as it did before slice 418.

So the refactor introduced no new log exposure. If the repository ever sets a
real `CI_ATLAS_APP_PASSWORD`, GitHub masks it in that banner automatically.

---

## Verdict

The composite-action refactor is proven behavior-preserving. All four
stack-bring-up call-sites still work through
`.github/actions/atlas-stack-up`, on the slice's own PR and on the merged
state; `actions-pin-check` passes on both; no credential or key material
reaches the logs.

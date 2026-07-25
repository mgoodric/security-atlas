# Slice 419 — promote long-stable advisory CI checks to required (or formally retire) — decisions log

**Slice:** 419 — Promote long-stable advisory CI checks to required (or formally retire)
**Date:** 2026-07-24
**Author:** matt-claude (Open Engine, OPENENGINE-368)
**Type:** JUDGMENT (the promote-vs-retain call per candidate is the judgment)

- detection_tier_actual: `manual_review`
- detection_tier_target: `manual_review`

_A latent governance defect surfaced during this slice and was caught in exactly the tier it should be: reading the live check-run data before editing the required-set. The `Self-host bundle · end-to-end` base check name is reported ONLY by its slice-061 stub — the real matrix job reports four expanded names — so the "obvious" promotion (add the base name to `contexts`) would have bricked every code PR. No test tier could have caught that; only inspecting what GitHub actually reports could. See D4._

## Decision summary

Four candidates, four documented terminal states. No candidate is left silently advisory (P0-419-2).

| Candidate                                           | Origin    | Terminal state           | Basis                                                                              |
| --------------------------------------------------- | --------- | ------------------------ | ---------------------------------------------------------------------------------- |
| `Helm chart · lint + template`                      | slice 038 | **PROMOTED to required** | 106/106 clean real runs, 0 reds ever in window; no registry exposure               |
| `Frontend · vitest`                                 | slice 069 | **PROMOTED to required** | 106/106 clean real runs, 0 reds ever in window; no registry exposure               |
| `Frontend · Playwright e2e (prod-build standalone)` | slice 387 | **PROMOTED to required** | 105/106 clean; sole red = Docker Hub incident; exposure already accepted on a peer |
| `Self-host bundle · end-to-end`                     | slice 065 | **RETAINED ADVISORY**    | Base check name never reported by the real job (D4) + dirty soak (D5)              |

Surgical diff (two files):

- `.github/branch-protection.json` — adds three contexts to `required_status_checks.contexts`; adds `$additions_from_slice_419` (promotions + soak evidence) and `$retain_advisory_from_slice_419` (the written reason for the non-promotion); updates `$deviations_from_slice_069` to its new terminal state.
- `docs/audit-log/419-promote-advisory-checks-decisions.md` — this file.

`.github/workflows/ci.yml` is **INTENTIONALLY UNCHANGED**. All three promoted checks already have a working slice-061 stub-twin (D3), so the AC-5 "add the stub if missing" branch never fires. This preserves P0-419-4 (no CI job added, removed, or behaviour-changed).

## Soak evidence — method and the CI-disabled gap

**Method.** Walked the GitHub Actions API — `repos/mgoodric/security-atlas/actions/workflows/ci.yml/runs` then `actions/runs/<id>/jobs` per run — the same data source `scripts/flake-counter.sh` uses. Real runs are distinguished from slice-061 stub runs by step count (a stub is 2 steps + 4 harness steps = 6; a real leg is 9–26), because both report under the identical check name.

**Window:** 139 distinct `ci.yml` runs, `2026-06-14T20:38Z` → `2026-07-24T16:15Z` — 103 `pull_request` + 36 `push`-on-`main`.

**THE CI-DISABLED GAP (called out explicitly, per AC-419-1).** CI was disabled repo-wide from **2026-06-29 to 2026-07-24** (the 2026-07 decommission was infrastructure-only; the repo and its backlog were revived 2026-07-24). There are **zero** runs inside that 25-day hole. Every count below is therefore the **sum of the two sides of the gap, never a continuous soak**:

- **Pre-gap:** 136 runs, `2026-06-14T20:38Z` → `2026-06-29T18:13Z` (last run before the shutdown).
- **Post-gap:** 3 runs, all `2026-07-24` (`codeql-int-overflow-fix`, `open-engine/OE-366-…`, `open-engine/OE-367-…`) — the first runs after the revival.

No promotion below is asserted on evidence from inside the gap, because none exists. The post-gap side is thin (3 runs) by construction — it is one day old. It is treated as a **confirmation** that the pre-gap soak survived the shutdown (all four candidates green on all 3 post-gap runs), not as the soak itself. Where a candidate needed the post-gap runs to clear the slice-116 bar, that is stated.

### Per-candidate run history

| Check (exact `ci.yml` `name:`)                      | Real pre-gap | Real post-gap | Total real | Reds | Stub runs | Consecutive clean since last red |
| --------------------------------------------------- | ------------ | ------------- | ---------- | ---- | --------- | -------------------------------- |
| `Helm chart · lint + template`                      | 103 ✓ / 0 ✗  | 3 ✓ / 0 ✗     | 106 ✓      | 0    | 33 ✓      | 106 (no red in window)           |
| `Frontend · vitest`                                 | 103 ✓ / 0 ✗  | 3 ✓ / 0 ✗     | 106 ✓      | 0    | 33 ✓      | 106 (no red in window)           |
| `Frontend · Playwright e2e (prod-build standalone)` | 102 ✓ / 1 ✗  | 3 ✓ / 0 ✗     | 105 ✓      | 1    | 33 ✓      | 17                               |
| `Self-host bundle · end-to-end (bundled)`           | 102 ✓ / 1 ✗  | 3 ✓ / 0 ✗     | 105 ✓      | 1    | —         | 78                               |
| `Self-host bundle · end-to-end (external)`          | 101 ✓ / 2 ✗  | 3 ✓ / 0 ✗     | 104 ✓      | 2    | —         | 14                               |
| `Self-host bundle · end-to-end (proxy)`             | 103 ✓ / 0 ✗  | 3 ✓ / 0 ✗     | 106 ✓      | 0    | —         | 106 (no red in window)           |
| `Self-host bundle · end-to-end (migrate)`           | 102 ✓ / 1 ✗  | 3 ✓ / 0 ✗     | 105 ✓      | 1    | —         | 16                               |
| `Self-host bundle · end-to-end` (base name)         | **0**        | **0**         | **0**      | —    | 33 ✓      | n/a — never reported by real job |

### Every red in the window, root-caused

Five red candidate legs total. Each was opened and read; none is an unexplained flake.

| Run           | Date       | Leg                                                 | Failing step                    | Root cause                                                                                         | Class             |
| ------------- | ---------- | --------------------------------------------------- | ------------------------------- | -------------------------------------------------------------------------------------------------- | ----------------- |
| `27524789748` | 2026-06-15 | `Self-host bundle · end-to-end (bundled)`           | Run self-host bundle smoke test | Real `next build` failure inside the bundle image build, on branch `feat/484-framework-versioning` | **True positive** |
| `27687999615` | 2026-06-17 | `Self-host bundle · end-to-end (external)`          | Run self-host bundle smoke test | `docker/dockerfile:1` manifest → Docker Hub `500 Internal Server Error`                            | Registry incident |
| `27951828338` | 2026-06-22 | `Self-host bundle · end-to-end (migrate)`           | Run self-host bundle smoke test | `library/postgres:16-alpine` manifest → `Error response from daemon: Head … unknown`               | Registry incident |
| `27951828338` | 2026-06-22 | `Frontend · Playwright e2e (prod-build standalone)` | Start NATS JetStream            | `library/nats:2.10-alpine` manifest → `Error response from daemon: Head … unknown` (exit 125)      | Registry incident |
| `27955604996` | 2026-06-22 | `Self-host bundle · end-to-end (external)`          | Run self-host bundle smoke test | `minio/minio:latest` manifest → `Error response from daemon: Head … unknown`                       | Registry incident |

Two distinct Docker Hub incidents (2026-06-17 ~12:12Z; 2026-06-22 12:14Z–13:18Z). The 2026-06-22 incident also took down `Frontend · UI honesty (advisory)` at its `Start MinIO` step in the same minute — corroborating that the cause is external and simultaneous, not per-job.

**Zero same-SHA rerun-cleared flakes** in the window, i.e. zero flakes under the narrow `docs/flake-budget.md` v1 definition. The distinction that decides this slice is therefore not flaky-vs-stable, it is **registry-exposed vs not**.

## D1 — `Helm chart · lint + template` PROMOTED

**Decision:** add the exact string `Helm chart · lint + template` to `required_status_checks.contexts`.

**Rationale.** 106 real runs, 106 green, **zero reds of any kind** across the whole window — the only candidate with a literally spotless record. That is not luck: the job is `helm lint` + `helm template` under `azure/setup-helm`, which pulls no container images and touches no external registry, so it has no exposure to the failure mode that produced four of the five reds above. It is a deterministic template-render check whose red means exactly one thing — the chart does not render. Clears the slice-116 bar (≥5 clean runs) by a factor of 20 on the pre-gap side alone; the 3 post-gap runs confirm it survived the shutdown.

**T-1 mitigation.** The Helm chart is roadmap-load-bearing (`Plans/canvas/10-roadmap.md`; the K8s install path). Before this change a PR that broke the chart merged green. It now blocks.

## D2 — `Frontend · vitest` PROMOTED (closes the slice-069 loose end)

**Decision:** add the exact string `Frontend · vitest` to `required_status_checks.contexts`; rewrite `$deviations_from_slice_069` to record the closure.

**Rationale.** Identical evidence shape to D1 — 106/106 real green, 0 reds, 33/33 stub green, npm/node only with no container pulls. The `$deviations_from_slice_069` annotation had said vitest's promotion was "filed as a separate follow-on (no slice number assigned yet)"; **slice 419 is that slice number**, and the annotation now says so. That loose end had been open since slice 069 and outlived its stated precondition ("once vitest coverage stabilizes") by 100+ slices — the deferral had become drift, which is the whole premise of this slice.

**T-1 mitigation.** vitest is the module-logic tier for the BFF route handlers, `lib/api.ts`, and `lib/api/bff.ts` (CLAUDE.md "four enforced surfaces"). Three of those four surfaces were already required checks; vitest was the hole. It is now closed, so the discipline table and the enforced set finally agree.

## D3 — `Frontend · Playwright e2e (prod-build standalone)` PROMOTED

**Decision:** add the exact string `Frontend · Playwright e2e (prod-build standalone)` to `required_status_checks.contexts`.

**This is the closest call of the four**, and the one the slice doc flagged for scrutiny. Recorded in full because a future maintainer reversing it deserves the reasoning.

**For promotion:**

1. 105/106 real runs green. **Zero** failures attributable to the job's own surface (the prod-build BFF-cookie and logo-render regression specs) across the entire window.
2. The sole red is root-caused to the 2026-06-22 Docker Hub incident, failing at `Start NATS JetStream` with `docker: Error response from daemon: Head https://registry-1.docker.io/v2/library/nats/manifests/2.10-alpine: unknown` before any spec executed. That is an environment failure, not a flaky spec.
3. 17 consecutive clean real runs since, spanning both sides of the CI gap.
4. **The decisive D-1 argument.** This job's external-dependency profile is _identical_ to the already-required `Frontend · Playwright e2e`: same `postgres:16-alpine` service container, same `docker run … minio`, same `docker run … nats:2.10-alpine`, verified line-for-line in `ci.yml`. Promoting it therefore adds **no new class** of registry exposure to the merge path — the repo already accepts precisely this exposure on a required check, and has since slice 116. The marginal availability cost is second-order (a registry outage narrow enough to hit one job's pull but not its twin's, ~1 minute apart on 2026-06-22), not a new category of risk.

**Against (and why it does not carry):** the 2026-06-22 incident _did_ red this job while every then-required check stayed green, so promotion is not literally free. But point 4 bounds that cost: any registry incident long enough to matter takes the already-required Playwright leg down with it. Registry resilience is a real gap — it is on the revisit list below as a shared fix for both Playwright legs and the bundle — but it is a pre-existing gap in the _required_ set, not one this promotion introduces.

**T-1 mitigation.** The prod-build standalone leg is the only surface covering two shipped production-build regressions: the standalone tracer dropping `web/public/` (slice 153 logo-render) and the `NODE_ENV`-coupled cookie attribute dropping the auth cookie on the BFF round-trip (slice 146). Both were real customer-visible breakages found in production builds, and the dev-build Playwright leg by construction cannot catch either. Leaving this advisory meant a recurrence merged green.

## D4 — `Self-host bundle · end-to-end` RETAINED ADVISORY, reason 1: the base check name is unpromotable

**Decision:** do NOT add `Self-host bundle · end-to-end` to `required_status_checks.contexts`. Record the reason in `$retain_advisory_from_slice_419`.

**This reason alone is decisive, independent of the soak.**

The real job `test-self-host-bundle` is a four-leg matrix (`mode: [bundled, external, proxy, migrate]`). GitHub reports it under the **matrix-expanded** names — `Self-host bundle · end-to-end (bundled)`, `(external)`, `(proxy)`, `(migrate)`. The slice-061 stub `test-self-host-bundle-stub` reports under the **base** name only.

Verified empirically over the window: **33 reports under the base name, all from the stub; 0 real reports under the base name, ever.**

Consequences:

- Adding the **base name** to `contexts` gives a required check that resolves on docs-only PRs and **never resolves on a code PR** — the exact never-resolves failure P0-419-3 forbids, and it would brick every code merge. This is the trap the naive reading of the slice title walks into.
- Adding the **four expanded names** instead requires four _new_ matrix-named stub-twins, or docs-only PRs hang forever on four contexts nothing reports. The `ci.yml` block at L1135 already anticipates exactly this: _"Promote it to a required check (and add the matrix-named stubs) in a follow-up once it has a few green runs."_

Adding four stub jobs is technically permitted by P0-419-4's stub-twin exception, but it cannot be **verified in both directions inside this slice**: a single PR is either docs-only or code, never both, so one of the two paths would ship unexercised. Shipping four required contexts where the docs-only path is unverified is how you brick the merge button on the next README-only PR. The honest move is to make that a properly-scoped change with a two-PR verification, not a rider on this one.

## D5 — `Self-host bundle · end-to-end` RETAINED ADVISORY, reason 2: the soak is not clean (D-1)

Corroborating, and it is what makes the deferral in D4 the right call rather than merely the cautious one.

**Four of the five reds in the entire window are this job.**

- One is a **true positive** (run `27524789748`, real `next build` failure). That is the check earning its keep, and it is the strongest argument for eventually promoting it — noted so the revisit is not read as "this check does not matter."
- Three are **Docker Hub registry incidents** across two separate outages.

The load-bearing observation: **in all three registry incidents, every check already in `contexts` was green.** Promoting these four legs would have converted three transient outages into three repo-wide merge freezes that did not otherwise occur — the D-1 availability wedge, in the concrete rather than the hypothetical.

And unlike D3, the shared-exposure argument does **not** rescue it. No currently-required check runs a full `docker compose` bring-up plus a multi-stage image build across four topologies. This job pulls and builds strictly more than anything on the merge path today, so promotion adds a genuinely new availability surface rather than reusing an accepted one. P0-419-1 is explicit: a candidate with a dirty soak gets a fix-first note, never a forced promotion.

Note also that the slice doc's prior — _"the Helm-lint and self-host-bundle checks are deterministic build/template checks and are the safest promotions"_ — is **half wrong**, and the data is why. Helm-lint is exactly as safe as predicted (D1). The self-host bundle is the _least_ safe candidate of the four, because "deterministic" describes its logic but not its dependency graph: four docker-compose topologies pulling from Docker Hub is the most environment-coupled thing in the workflow. Recorded so the next reader trusts the measurement over the prior.

## D6 — no live apply from the slice branch

**Decision:** do not run `bash scripts/apply-branch-protection.sh` against live GitHub from this branch. Run it in `DRY_RUN` mode to validate the payload, and hand the apply to the maintainer in the PR body (AC-419-6, second branch).

**Rationale.** The required-set change binds the maintainer too via `enforce_admins: true` (P0-419-6), so it must not go live before maintainer review — applying from the slice branch would enforce a required-set the maintainer has not yet approved. This also matches the established hand-off convention: the `$additions_from_slice_128` / `_140` / `_159` / `_116` annotations all read "After merge: run `bash scripts/apply-branch-protection.sh`."

There is a second, mechanical reason the apply belongs after merge. `apply-branch-protection.sh` exits 3 when GitHub silently drops a context it does not recognise, and GitHub only recognises a context once that check has reported on the protected branch. That risk was checked, not assumed: each of the three promoted names has **36 green reports on `push`-to-`main` runs** inside the window, so GitHub already knows all three context strings and the maintainer's apply is expected to converge cleanly. The merge commit's own `main` run is the belt-and-braces confirmation, and it exists only after merge.

**File-vs-live state at slice time.** `gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks` returns 15 contexts, exactly matching the file's pre-slice list — **no pre-existing drift** (the reconciliation the `$deviations_from_slice_050_AC11` NOTE of 2026-06-13 called a follow-up has since happened). The only file↔live delta this slice creates is the three intended additions, so the maintainer's apply is a clean three-context add and nothing else.

## Verification performed

| Check                                                                                          | Result                                                                                                                                                                                 |
| ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AC-419-4 — every added context matches a literal `ci.yml` `name:` byte-for-byte                | PASS — `grep -xF "    name: <ctx>"` matched for all three; the separator is U+00B7 (`c2 b7`), same as existing entries                                                                 |
| AC-419-5 — each promoted check has a working slice-061 stub-twin                               | PASS — exactly 2 job declarations per name with mutually-exclusive `code == 'true'` / `code != 'true'` guards, **and** live-verified: 33 stub reports per name across 33 docs-only PRs |
| AC-419-7 — `branch-protection-drift-validate` (`Infra · branch-protection (PR-time validate)`) | PASS — file is valid JSON with a non-empty `.required_status_checks.contexts` (18 entries); the job's shape check reproduced locally                                                   |
| AC-419-6 — apply payload                                                                       | `DRY_RUN=1 bash scripts/apply-branch-protection.sh` succeeded; `$`-prefixed annotation keys strip cleanly, including the two new ones. Live apply handed to the maintainer (D6).       |
| P0-419-4 — no CI job added, removed, or behaviour-changed                                      | PASS — `.github/workflows/ci.yml` untouched                                                                                                                                            |
| P0-419-5 — merge-queue / integration job structure untouched                                   | PASS — `CI · merge-gate` and the shard matrix untouched                                                                                                                                |

**Self-verifying PR.** This PR touches only `.github/branch-protection.json` and `docs/audit-log/`. Neither path is in the `changes.code` paths-filter, so the PR runs as **docs-only** — which means its own CI run exercises precisely the stub-twin path for all three newly-required contexts. If a stub were missing or misnamed, this PR's checks would show it before the required-set ever goes live.

## Revisit list

1. **`Self-host bundle · end-to-end` → required.** Ordered, and all five steps land together:
   1. Harden `deploy/docker/test-self-host-bundle.sh` against registry flakiness — retry-with-backoff on image pull and/or a GHCR pull-through mirror for the base images — so a Docker Hub blip stops reading as a bundle regression.
   2. Add the four matrix-named slice-061 stub-twins: `Self-host bundle · end-to-end (bundled|external|proxy|migrate)`.
   3. Verify **both** directions: one docs-only PR resolving the four stub contexts green, and one code PR resolving the four real contexts green.
   4. Re-soak ≥5 clean runs per leg post-hardening.
   5. Promote all four expanded contexts in one change — a partial promotion leaves the same integrity hole this slice exists to close.
2. **Registry resilience for the required Playwright legs.** Step 1 above is worth generalising: both `Frontend · Playwright e2e` (already required) and `Frontend · Playwright e2e (prod-build standalone)` (promoted here) `docker run` MinIO and NATS with no pull retry. The 2026-06-22 incident is the existence proof. This is a pre-existing exposure on the merge path, not one this slice adds, but it is now the largest single availability risk in the required set.
3. **Re-check the post-gap soak once real traffic resumes.** The post-gap side is 3 runs on one day. If any promoted check reds in the first two weeks of resumed traffic for a reason that is _not_ an external incident, revisit that promotion rather than absorbing it — this slice's whole discipline is that a required check with a dirty soak is worse than an advisory one.
4. **The remaining advisory backlog is not empty.** `Frontend · lint` (slice 078, explicitly deferred per P0-A4) and `Frontend · UI honesty (advisory)` (slice 178, deferred pending 5+ stable PRs) were **not** in this slice's four-candidate scope and remain advisory by their own slices' decisions. They are named here so the "advisory backlog drained" claim is read precisely: the four candidates named in slice 419 are resolved; these two carry their own prior deferrals and would need their own promotion slice.

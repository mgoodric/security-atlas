# 123 — Investigate + fix 4 e2e specs unmasked by slice 119's port-3000 fix

**Cluster:** Frontend / Quality
**Estimate:** 1d (diagnose-heavy, 4 independent specs)
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 119, captured as follow-up per continuous-batch policy.

Slice 119 fixed the recurring "port 3000 already in use" race that had been killing every Playwright e2e run at startup. With Playwright now actually executing specs (104/110 passing post-fix), 4 distinct specs are failing — these are bugs that the port-3000 race had been silently masking for an unknown period:

| Spec                                                | Failure                                                                                  |
| --------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `web/e2e/auth-open-redirect.spec.ts`                | `expect(locator).toBeVisible()` timeout on the "dashboard not attacker URL" assertion    |
| `web/e2e/first-time-login.spec.ts`                  | `expect(locator).toBeVisible()` timeout on the "install state reports fresh" assertion   |
| `web/e2e/logo-render.spec.ts` (× 2 assertions)      | Failures on both the "Metadata API" path and the "static public assets" path             |
| `web/e2e/security-headers.spec.ts` (× 2 assertions) | Failures on the "all five hardening headers" assertion (likely the path + name variants) |

Reference: PR #259 (slice 119) CI run [25980065401](https://github.com/mgoodric/security-atlas/actions/runs/25980065401) — the first run where Playwright reached spec execution.

The 4 specs are likely independent failures — each needs its own diagnosis pass. Possible shared root causes worth ruling out:

- Seed-data collision from slice 122's `api_keys_token_hash_unique` issue cascading into auth state (could affect auth-open-redirect + first-time-login)
- Routing/middleware change that lands after one of the specs was authored (logo-render + security-headers)
- BFF cookie-forwarding change from slice 110 affecting the auth specs

## Gating condition

`not-ready` until slice 122 (api_keys idempotency) merges. If 122 fixes the seed collision AND the auth specs (auth-open-redirect + first-time-login) start passing, this slice's scope reduces to just the 2 visual/header specs. The maintainer flips to `ready` after 122 lands + re-running CI on a Dependabot PR confirms which specs remain broken.

## Acceptance criteria

- [ ] AC-1: Per-spec diagnosis. For each of the 4 specs, the PR body documents (a) the root cause (with evidence — git blame, code reference, runtime trace), (b) the minimal fix scope, (c) whether the bug is in the spec or in production code.
- [ ] AC-2: For each spec where the bug is in PRODUCTION CODE: apply the fix in this slice. For each spec where the bug is in the SPEC ITSELF (stale assertion, brittle selector, etc.): fix the spec.
- [ ] AC-3: All 4 specs pass on ≥3 consecutive CI runs of `Frontend · Playwright e2e` against this PR.
- [ ] AC-4: For each spec, the PR body identifies WHEN it was last passing (last commit where it was green) via `git log` + the slice that introduced the regression — surfaces whether the regression has been masked for 1 day vs 6 months.
- [ ] AC-5: Decisions log at `docs/audit-log/123-investigate-4-unmasked-e2e-spec-failures-decisions.md`. Required entry: the per-spec root cause + the fix scope decision (production-code-fix vs spec-fix) + whether any of the 4 had a shared root cause that became visible only after diagnosis.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — 4 independent diagnoses + 4 minimal fixes; don't refactor the spec suite
- **No vendor token prefixes in any new fixture** (carry-over)

## Canvas references

- `web/e2e/{auth-open-redirect,first-time-login,logo-render,security-headers}.spec.ts` (the 4 specs)
- Slice 119's PR #259 CI logs (the surfacing context)
- Slice 082's `seedFromFixture()` harness (potential interaction)
- Slice 110's BFF cookie-forwarding (potential interaction with auth-open-redirect)
- Slice 075's logo work (potential interaction with logo-render)
- Slice 037's security-header middleware (potential interaction with security-headers)

## Dependencies

- **122** (api_keys seed-harness idempotency) — must merge first; may resolve 1-2 of the 4 spec failures by itself

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT skip / `test.fixme` / `test.skip` any of the 4 specs as the "fix". The point is they should PASS. Quarantining them is the slice-079 anti-pattern that slice 082 + slice 119 spent effort reversing.
- **P0-A2**: Does NOT relax the assertions to make them pass. If `toBeVisible` times out, find why the element isn't visible; don't change the assertion to `toBeAttached` or similar.
- **P0-A3**: Does NOT bundle in scope-expansion to fix OTHER spec flakes that weren't in the slice-119-unmasked set. The 4 specs in AC-1 are the ENTIRE scope.
- **P0-A4**: Does NOT modify slice 119's port-3000 fix to "make it less aggressive" — slice 119 is correct; the specs are bugs.

## Notes for the implementing agent

- **Start with `git log -p web/e2e/auth-open-redirect.spec.ts` etc.** — see when each spec was last touched and what assertion is failing. Cross-reference against the failure trace in the slice 119 CI logs.
- **Sequence the 4 diagnoses to minimize re-work**: do the auth specs first (they may share root cause via slice 122's seed-data fix), then the logo + security-headers specs (likely independent).
- **For each spec, the diagnosis output is the load-bearing artifact** — the actual fix may be trivial once the cause is known, but the diagnosis trail is what prevents the next-slice regression.
- A previous similar pattern was slice 119 itself: the slice doc predicted one root cause; the actual root cause was different but the diagnosis pass made the fix obvious. Expect the same pattern here.

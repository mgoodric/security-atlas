# 161 — Diagnose + fix auth-open-redirect.spec.ts drift (slice 086 follow-on)

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**WHY.** Slice 086 (2026-Q2 security audit, open-redirect HIGH-severity remediation) added `web/e2e/auth-open-redirect.spec.ts` as the end-to-end live verification of `safeRedirectTarget` (the helper in `web/lib/safe-redirect.ts`). The spec drives the actual login form on `/login?from=https://evil.example.com/phish`, fills the bearer token, clicks "Sign in", and asserts the post-sign-in redirect lands on `/dashboard` rather than the attacker URL.

That spec is now failing on `main`. Surfaced 2026-05-18 on slice 153's PR #330 — the first PR whose path-filter triggered the real `Frontend · Playwright e2e` job rather than the docs-only stub. The failure output:

```
auth-open-redirect ▶ open-redirect defense on signIn ▶
  "sign-in with attacker ?from= lands on /dashboard, not attacker URL"
  Error: expect(received).toBe(expected) // Object.is equality
  [test failed, both attempts (initial + retry)]
```

The spec's two assertions are:

```ts
expect(final.host).not.toBe("evil.example.com"); // hostname check
expect(final.pathname.startsWith("/dashboard")).toBe(true); // path check
```

Without log triage we don't yet know which assertion fired. Three plausible drift sources:

1. **Behavior regressed** — some slice between 086 and HEAD changed the login post-action so the redirect no longer lands on `/dashboard`. Candidate slices: 100s-series auth-flow work, slice 141 (deferred multi-tenant rewrite, not yet shipped), slice 146 (BFF cookie production-build fix). If this is the cause, the **spec is right** and the platform is the bug.
2. **Spec drifted** — the post-sign-in destination intentionally changed (e.g., a new welcome / setup-flow step inserted before `/dashboard`), and the assertion's "starts with `/dashboard`" expectation is now wrong. If this is the cause, the **platform is right** and the spec needs updating.
3. **Harness drifted** — the `TEST_BEARER` env var or the `authedPage` fixture from `web/e2e/fixtures.ts` (slice 082) doesn't produce a usable signed-in session anymore, so the form-fill + click never reaches the redirect step. If this is the cause, the **fixture is the bug** and the spec is fine.

Important: this is **NOT a regression caused by slice 153**. Slice 153's only files were `deploy/docker/web.Dockerfile`, `web/package.json`, and a new Playwright spec; none of those touch auth flow. The spec was already broken on `main` and the docs-only path filter was hiding it.

**WHAT.** Diagnose which of the three drift sources is the actual cause (this is the JUDGMENT decision), then apply the matching fix. Run the spec locally against the docker-compose stack, read the failure trace + screenshot artifact, and record the chosen path in the decisions log.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT promote `Frontend · Playwright e2e` to required-checks. That's slice 116 (already filed `not-ready`, gated on 111-115 un-skip series + soak).
- Does NOT touch any other failing Playwright spec. The `control-detail-empty` failure is slice 160.
- Does NOT change `web/lib/safe-redirect.ts`. The unit test in `web/lib/safe-redirect.test.ts` is the always-required gate; if that's green on `main`, the helper itself isn't broken — the wiring around it might be (case 1) or the spec's expectations might be (case 2). Either way, this slice does not modify the helper.
- Does NOT bundle a "while we're here" pass on the other auth-flow specs (`root-redirect.spec.ts`, `first-time-login.spec.ts`). Each gets its own slice if it surfaces a real failure.

## Threat model

**Spoofing.** The spec exists specifically to defend against the spoofing risk that slice 086's audit caught — open-redirect → phishing pivot. **Mitigation in this slice:** AC-5 (synthetic-attacker repro) verifies that whichever fix path is chosen, the post-fix state still rejects the attacker URL and lands the user on a same-origin path. If case 1 (behavior regression), this slice **closes a live security regression** that's been on `main` since the breaking commit landed.

**Tampering.** N/A — no production code change beyond the diagnosed fix (which is constrained to either the spec, the fixture, or the wiring around `safeRedirectTarget`).

**Repudiation.** N/A — no audit-log surface change.

**Information disclosure.** N/A — the spec uses synthetic `evil.example.com` and the slice-082 harness bearer. No production data.

**Denial of service.** N/A — single Playwright test, bounded by Playwright's own timeouts.

**Elevation of privilege.** Closely related to spoofing: if the post-fix state ever lands on an authenticated `/dashboard` after consuming an **attacker-controlled URL**, that's privilege escalation via redirect-chain forgery. **Mitigation:** AC-6 (test-the-test) explicitly verifies the spec's negative assertion (`expect(final.host).not.toBe("evil.example.com")`) actually fires when the attacker URL is honored — i.e., before applying the fix in cases 1 or 3, the spec must red. If the spec is silently green when broken, the diagnosis is wrong.

**Anti-criteria added from threat model:** P0-A3 (synthetic-attacker repro mandatory), P0-A4 (test-the-test).

## Diagnosis options (JUDGMENT — engineer picks the case via decisions log)

| Case | Likely root cause                                                                                 | Fix shape                                                                                                                                                                                                                                                                                                                                          |
| ---- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | **Behavior regression.** Slice X between 086 and HEAD broke the post-sign-in redirect target.     | Trace through `web/app/login/actions.ts` and any middleware between login and `/dashboard`. Identify the breaking commit via `git log --oneline -- web/app/login/ web/middleware.ts` since slice 086's merge. Fix the wiring so `safeRedirectTarget` is honored on the post-sign-in redirect. **Spec unchanged.**                                  |
| 2    | **Intentional UX change.** Post-sign-in destination is now something like `/welcome` or `/setup`. | Update the spec's path assertion to match (`expect(final.pathname.startsWith("/welcome")).toBe(true)` or similar). Update the spec preamble to document the new expected destination. **Platform unchanged.** Cross-check with slice docs since 086 for the intentional change; if no slice owns the change, escalate — case 1 may be in disguise. |
| 3    | **Fixture drift.** `TEST_BEARER` / `authedPage` no longer produces a valid signed-in session.     | Fix the harness in `web/e2e/fixtures.ts` (slice 082 ownership). Likely depends on the slice-141 multi-tenant rewrite if that ever lands; for now, restore single-tenant fixture validity. **Spec + platform unchanged.**                                                                                                                           |

The decisions log captures which case won + the evidence trail. (See [`docs/audit-log/161-playwright-auth-open-redirect-spec-drift-decisions.md`](../audit-log/161-playwright-auth-open-redirect-spec-drift-decisions.md), AC-8.)

## Acceptance criteria

**Diagnosis:**

- [ ] AC-1: Reproduce the failure locally. `cd web && npx playwright test e2e/auth-open-redirect.spec.ts --headed=false` against the docker-compose stack fails the same way CI fails. Capture the trace + screenshot. Identify which assertion (host vs pathname) fires.
- [ ] AC-2: Determine which Case (1, 2, or 3) the failure is. Evidence: trace shows `final.host` and `final.pathname` values; `git log` since slice 086's merge surfaces the candidate slice (Case 1) OR a deliberate UX-change slice is identified (Case 2) OR the fixture's `authedPage` produces an unauthenticated session (Case 3).

**Resolution (one of three branches):**

- [ ] AC-3: **Case 1 only** — identify the breaking commit + fix the wiring. The fix is the smallest possible change that restores `safeRedirectTarget` enforcement on the post-sign-in redirect. Do NOT refactor adjacent code. Spec stays unchanged.
- [ ] AC-4: **Case 2 only** — update the spec's pathname assertion + the preamble to document the new expected destination. Cross-link the slice that introduced the UX change in the spec comment. No platform code change.
- [ ] AC-5: **Case 3 only** — fix the fixture in `web/e2e/fixtures.ts` so `authedPage` produces a valid signed-in session against the current platform. Spec stays unchanged. Re-run the spec post-fix.

**Verification (all cases):**

- [ ] AC-6: Spec passes locally (`npx playwright test e2e/auth-open-redirect.spec.ts`) — both initial run and a forced retry-via-`--retries=2`.
- [ ] AC-7: **Test-the-test** — temporarily revert the fix (Case 1) OR temporarily change the spec back to the broken assertion (Case 2) OR temporarily break the fixture (Case 3) to confirm the spec actually RED's in that broken state. Then revert the revert. This is the "the test actually tests what we think" gate.
- [ ] AC-8: `docs/audit-log/161-playwright-auth-open-redirect-spec-drift-decisions.md` written. Sections: (1) which Case + evidence; (2) chosen fix + alternatives rejected; (3) confidence per decision; (4) revisit-once-in-use list (e.g. "if slice 141 multi-tenant lands, re-verify TEST_BEARER provisioning matches the new session model").
- [ ] AC-9: `Frontend · Playwright e2e` CI job (real, not stub) passes on the PR. No `auth-open-redirect.spec.ts` failures in the summary.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only."** Each Case dictates the minimum scope. Case 1: spec unchanged. Case 2: platform unchanged. Case 3: spec + platform unchanged. The slice never bundles fixes across boundaries.
- **CLAUDE.md "Never assert without verification."** AC-1 (local repro) + AC-6 (local pass) + AC-7 (test-the-test) are all evidence-required. No "should work" merges.
- **CLAUDE.md "Fail-fast after two failed attempts."** If diagnosis stalls on Case identification, the engineer escalates rather than guessing — the wrong fix in this surface re-introduces a security regression.
- **Slice 086's open-redirect defense.** The post-fix state still rejects attacker URLs. AC-5 verifies via synthetic attacker repro (test-the-test direction).

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Playwright + e2e testing
- Slice 086 (`086-open-redirect-defense.md`) — the original audit fix this spec verifies
- Slice 082 (`082-playwright-seed-data-harness.md`) — the harness contract (`authedPage`, `TEST_BEARER`)
- Slice 102 — Bootstrap UI changes that COULD have moved the post-sign-in destination (cross-check during AC-2)
- Slice 146 (`146-bff-cookie-production-build.md`) — recent BFF cookie work, check if it changes session lifecycle
- `web/lib/safe-redirect.ts` + `web/lib/safe-redirect.test.ts` — the helper + its unit test (out of scope; here for diagnosis context only)
- `web/app/login/actions.ts` — the call site

## Dependencies

- #086 — merged. Slice this fixes is owned by 086.
- #082 — merged. Harness owner if Case 3 is the diagnosis.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT change `web/lib/safe-redirect.ts`. The unit test is the always-required gate on that helper; if it's green, the helper is fine.
- **P0-A2**: Does NOT promote `Frontend · Playwright e2e` to required-checks. Slice 116 owns that decision after the broader un-skip series completes.
- **P0-A3** (from threat model — Spoofing): Does NOT merge without AC-1 + AC-6 evidence. The whole point is to prove the attacker URL is rejected post-fix; a green CI run on a spec that doesn't actually test the right thing is worse than red.
- **P0-A4** (from threat model — Elevation of privilege): Does NOT merge without AC-7 test-the-test evidence. A spec that doesn't red on a broken state is a false sense of security.
- **P0-A5**: Does NOT bundle fixes for other failing specs (`control-detail-empty.spec.ts` is slice 160; any others are separate slices).
- **P0-A6**: Does NOT refactor `web/e2e/fixtures.ts` beyond the minimum needed for Case 3 (if Case 3 is the diagnosis). Slice 082 owns the harness shape.
- **P0-A7**: Does NOT use vendor-prefixed test fixture tokens (carry-over convention from slice 05 + slice 086).

## Skill mix

- Playwright trace + screenshot diagnosis (`npx playwright show-trace` is the canonical tool)
- `git log` / `git blame` archaeology across `web/app/login/` and `web/middleware.ts`
- Slice 086 + slice 082 contract reading (one-shot context for the auth flow + fixture)
- Decisions-log discipline (slice 109 + slice 121 + slice 152 + slice 159 are the recent JUDGMENT-slice examples)

## Notes for the implementing agent

**Provenance:** Surfaced 2026-05-18 during slice 153 (logo standalone fix) PR session. Spillover candidate #3 in batch-59 reconcile PR #331 notes.

**Why this is JUDGMENT, not AFK:** the fix shape depends entirely on the diagnosis. The slice doc cannot pre-pick a case — the engineer must trace the actual failure first. The decisions log captures the chosen case + evidence so a future reader understands why the fix took the shape it did.

**Diagnosis triage order:**

1. Run the spec locally first (AC-1). The trace screenshot tells you whether the redirect landed on the attacker URL (case 1, security regression) or on a different same-origin path like `/login` or `/welcome` (case 2 or case 3, behavior change or auth-fail).
2. If the screenshot shows `/login` after click → likely Case 3 (auth never succeeded; fixture is broken).
3. If the screenshot shows `evil.example.com` after click → **Case 1 critical**. This means slice 086's defense is broken in production. Escalate visibility; do not delay the fix. (The unit test in `safe-redirect.test.ts` being green only proves the helper works in isolation — it doesn't prove the wiring still calls it.)
4. If the screenshot shows `/welcome`, `/setup`, `/onboarding`, or any other same-origin non-`/dashboard` path → Case 2. Identify the slice that introduced it.

**Cross-check against the recent batch:** the v1.11.0 fix-slice campaign (146-153 + 156, 157) shipped a lot of UI churn. Slice 102 (admin bootstrap), slice 150 (admin bootstrap retry), slice 156 (OPA readable-resources expansion), and slice 157 (dashboard upcoming + top risks) all touched routes that COULD be post-sign-in destinations. None of them obviously changed `/dashboard` to something else, but the diagnosis must verify.

**On the multi-tenant slice 141 dependency:** slice 141 (`141-multi-tenant-login-and-switcher.md` — currently `not-ready`, deferred) will rewrite the login + session model. When that ships, the fix from this slice will likely need re-verification. AC-8's "revisit once in use" section should call this out explicitly.

**Cross-link to slice 153 reconcile:** the slice 153 batch-59 reconcile PR (#331) listed this as spillover candidate #3. Once slice 161 ships, that spillover line is resolved. All three batch-59 spillovers (159, 160, 161) close together.

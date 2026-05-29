# 351 — e2e critical multi-tenant flow gap audit — decisions log

Slice 351 is an AFK slice that carries real JUDGMENT on the quarantined
specs. This log records the build-time calls per the JUDGMENT-slice
discipline (the maintainer iterates post-merge rather than blocking on a
sign-off).

Closes slice 333 Q-9. Cross-refs slice 334 P-4.

---

## D-351-1 — Reconciliation: the slice doc's quarantine count was stale

The slice doc + slice 333 Q-9 said "8 `test.skip` quarantines" and named
`questionnaires` + `risks-create` among them. Verified on the branch
base by grep:

- `questionnaires.spec.ts` — **NOT skipped**. It is a LIVE mocked spec
  (slice 263). The earlier audit's line-29 grep hit was a comment, not a
  `test.skip()`.
- `control-detail-tabs.spec.ts` — **NOT skipped**. Un-quarantined by
  slice 276 (the mock-schema-conformance fix).
- The real count is **6 active `test.skip()` guards across 5 files**
  (`bff-cookie-production-build` has the guard expressed once at the
  describe level but two `authed(...)` tests inside it — the slice 333
  "8" likely counted assertions, not guards).

`audits-create.spec.ts` was NOT in the slice-doc spot-check but is the
same legacy-quarantine pattern as `risks-create`, so it was triaged here
too (the audit-pass is the load-bearing deliverable; leaving an
identical quarantine untriaged would be a gap).

**Confidence:** high (grep-verified on the branch base).

---

## D-351-2 — `auth-open-redirect.spec.ts` → (a) un-skip

**Decision:** removed the `test.skip(!HAS_BEARER, …)` guard.

**Evidence:** the guard reads `const HAS_BEARER =
!!process.env.TEST_BEARER` at module-load. Slice 201's `globalSetup`
(`web/e2e/global-setup.ts`) mints a JWT via the env-gated `POST
/v1/test/issue-jwt` and writes it into `process.env.TEST_BEARER` BEFORE
any worker imports a spec. So in CI `HAS_BEARER` is true and the guard
was a relic of the pre-201 era (when the harness provided no bearer). The
guard was silently skipping a spec that should have been RUNNING. The
spec BODY was already fixed by slice 161 (Case 2 — racy `waitForURL` →
settled-pathname wait; see
`docs/audit-log/161-playwright-auth-open-redirect-spec-drift-decisions.md`).
Removing the guard makes it an honest live security-regression gate.

**Why not (b)/(c):** not (b) — there is no blocking issue; the harness
provides the bearer. Not (c) — the spec is a live, valuable open-redirect
defense gate driving the real login form; deleting it would lose
coverage of a HIGH-severity security fix (slice 086).

**Risk:** if `TEST_BEARER` is somehow absent, the `authedPage` fixture
throws a clear error — correct fail-loud behaviour for a required gate,
not a silent skip.

**Confidence:** high.

---

## D-351-3 — `bff-cookie-production-build.spec.ts` +

## `logo-render-production-build.spec.ts` → (b) re-quarantine + spillover

**Decision:** kept the `test.skip(!ATLAS_PROD_BUILD, …)` guard;
rewrote the justification to cite the real gap + the spillover (slice
387). Did NOT force them green.

**Evidence:** both specs assert regressions that ONLY manifest under the
Next.js production-build standalone server (slice 146's `NODE_ENV`-cookie
bug; slice 153's `output: "standalone"` public-assets gap). CI's
Playwright `baseURL` points at the `npm start` dev server. `grep
ATLAS_PROD_BUILD .github/` is empty — there is no CI job that builds +
boots the standalone server. `web/package.json` has a `build:standalone`
script but nothing in CI invokes it.

**Why not (a):** forcing these to run against the dev server would make
them assert nothing about the standalone-only path — a false green. That
is precisely the green-washing the slice's UI-honesty value forbids.

**Why not (c):** the specs are correct and valuable; they guard real
shipped regressions. The blocker is a harness gap, not spec rot.

**Spillover:** slice 387 (`387-e2e-prod-build-standalone-ci-harness.md`)
— one standalone-server CI leg unblocks both specs. When it lands, the
`ATLAS_PROD_BUILD` guards are satisfied in CI and the specs gate every
PR.

**Confidence:** high.

---

## D-351-4 — `risks-create.spec.ts`,

## `risks-create-control-link.spec.ts`,

## `audits-create.spec.ts` → (a) un-skip (rewrite as mocked)

**Decision:** removed the `test.skip(!PLAYWRIGHT_RUN_QUARANTINED, …)`
guards and the commented-out bodies; rewrote each as a LIVE mocked spec
following the `questionnaires.spec.ts` `route.fulfill` convention
(anti-criterion P0-4 mandates the established mock pattern for new specs).

**Evidence:** the guard + commented bodies are the slice-082-era
placeholder pattern ("seed harness not landed yet — preserve bodies as a
reviewable contract"). The harness DID land (slice 082 + 201). The pages
(`/risks/new` risk-form.tsx, the ControlMultiSelect, `/audits/new`
audit-period-form.tsx) ship every asserted testid. There is **no
underlying product bug** — so P0-2 (don't fix the underlying bug) is not
engaged; this is pure spec-fill.

**Honesty correction (the project's UI-honesty value).** The old
commented bodies asserted behaviour that has since drifted:

- `risks-create`: the old "submit a risk" body relied on the default
  treatment (`mitigate`) NOT requiring a linked control, and the old
  "upstream 4xx" body expected an empty title to bounce off the SERVER.
  Both are now wrong: `mitigate` requires ≥1 linked control CLIENT-SIDE
  (`validateRiskForm`), and empty title is gated CLIENT-SIDE (renders
  `risks-create-title-error` inline, never contacts the server). The
  rewritten spec uses treatment `accept` for the happy path and asserts
  the ACTUAL client-side title-error behaviour — and explicitly asserts
  NO POST fires on the empty-title path. Writing the stale assertions
  verbatim would have green-washed a behaviour that no longer exists.

**Why not (b):** no blocking issue.

**Why not (c):** these are v1-flow specs (risk register + audit-period
create); deleting them would lose coverage the project wants.

**Confidence:** high on the un-skip decision; medium on the exact mock
shapes until the local Playwright run confirms (see D-351-6).

---

## D-351-5 — tenant-switch (AC-2) authored as a mocked spec + real-RLS

## variant spilled over

**Decision:** authored `web/e2e/tenant-switch.spec.ts` as a
`route.fulfill`-mocked spec covering (a) the >1-tenant switcher render,
(b) the switch changing tenant context (target id crosses the wire +
edge-visible current-tenant flip), (c) the single-tenant hide rule. Filed
the real-RLS cross-tenant-leak variant as slice 389.

**Evidence + judgment:** the slice doc's AC-2(c) ideal is "assert on a
known-tenant-A row not visible in tenant B view" — through real RLS.
That cannot run against the bring-up: `internal/api/testissuejwt.go`
hardcodes `AvailableTenants: []uuid.UUID{tenant}` (single-tenant), and
the RFC 8693 token-exchange requires the target tenant in
`available_tenants[]`. Per the seed-harness contract
(`web/e2e/README.md`): a spec needing preconditions the bootstrap can't
provide → file a spillover for the harness, don't relax the spec.
Anti-criterion P0-4 also mandates the `route.fulfill` pattern for new
specs in the merged suite. So the mocked spec ships now (honest about
what it proves: flow + hide rule), and the deeper real-RLS assertion is
slice 389 (which extends the test-JWT endpoint to mint a multi-tenant
claim set — a JUDGMENT call kept behind the existing `ATLAS_TEST_MODE`
gate).

**Confidence:** high on the flow + hide-rule mock; the real-RLS depth is
honestly deferred, not faked.

---

## D-351-6 — Local validation strategy

The merged-suite convention is `route.fulfill`-mocked specs; the new +
rewritten specs are all mocked and intercept their BFF endpoints before
the BFF→atlas path matters. They need the Next.js web server + a
`TEST_BEARER` cookie. Validated locally per AC-5; the
`globalSetup` JWT-mint requires a running atlas (`ATLAS_TEST_MODE=1`),
which the docker-compose bring-up provides in CI. The CI `Frontend ·
Playwright e2e` job is the authoritative gate (AC-6). Any local-vs-CI
delta (per the `feedback_local_vs_ci_delta.md` memory) is reconciled
against the CI run before claiming pass.

---

## Revisit-once-in-use list

- When **slice 387** lands, drop nothing — the prod-build specs simply
  start executing in the new CI leg. Re-verify they pass there.
- When **slice 389** lands, `tenant-switch.spec.ts` (mocked) stays as the
  fast flow gate; `tenant-switch-rls.spec.ts` adds the real-RLS depth.
- If the risk-create or audit-period **client-side validation** changes
  again, the rewritten specs assert the current behaviour and will red —
  which is the point (they are now honest gates, not commented contracts).

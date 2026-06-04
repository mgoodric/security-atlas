# 280 — Remove bearer-paste card from /login (no users yet; no backwards-compat)

**Cluster:** Frontend + Auth (cleanup)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The `/login` page currently renders three sign-in surfaces in order:

1. `<FirstInstallCard />` — the first-install bootstrap-token flow (slice 123)
2. `<Card>` for email/password local-auth — only renders when
   `bootstrapTenantID` is resolvable (slice 209)
3. `<Card>` for bearer-token paste — `signIn` action stores the pasted
   token in the `atlas_jwt` httpOnly cookie (legacy, pre-OIDC)

The bearer-paste card is the third one. It exists for two historical
reasons:

- **Dev-mode auth before slice 034 OIDC**: the operator pasted a token
  from `atlas-cli credentials issue` or the platform's stderr
  bootstrap output. Slice 197 (merged at `00a682c`) retired the
  slice-034 bearer middleware; slice 191 (merged at `8f0d265`)
  migrated the CLI to OAuth `client_credentials` + device-code. The
  bearer-issue path the card was designed against no longer exists.

- **Install-state-failure fallback**: when `/v1/install-state`
  doesn't resolve (network blip, wedged backend), the local-auth card
  is hidden — the bearer-paste card was the only remaining sign-in
  surface. After this slice, that fallback is replaced with an
  honest "service unavailable, retry shortly" banner.

The maintainer's call: there are **no users of the system yet**, so
no backwards-compatibility concern. The bearer-paste card is dead
chrome. Remove it.

### What ships in this slice

1. **Delete the bearer-paste `<Card>`** from
   `web/app/login/page.tsx` (currently lines ~196-237).
2. **Delete the `signIn` server action** from
   `web/app/login/actions.ts`. Keep `signInLocal` (slice 209) and
   `signOut`. Update the file's header comment to reflect the
   reduced surface.
3. **Replace the install-state-fails fallback** with a polite
   service-unavailable banner. When `bootstrapTenantID` is null
   AND `FirstInstallCard`'s own probe also reports null, render
   an `<Alert>` reading "Sign-in service unavailable. The
   platform may still be starting; retry in a few seconds. If
   the problem persists, contact your administrator." NO sign-in
   form. The operator either waits for the backend or fixes it.
4. **Update Playwright e2e**: `web/e2e/first-time-login.spec.ts`
   has three assertions that look up `getByLabel("Bearer token")`
   (lines 58, 76, 92). Replace with assertions on the local-auth
   email/password fields (the card slice 209 ships), OR mark
   those test branches as `.skip()` with a TODO pointing at the
   replacement-coverage slice if the migration is non-trivial.
   Prefer the replacement-coverage path; a skip should only ship
   if the replacement requires fixture work that's out-of-scope
   here.
5. **Update the page-level comment** at `web/app/login/page.tsx`
   line ~93 that references the "fall back to the bearer-paste
   card only" branch — that branch no longer exists.

### Scope discipline (deliberately OUT)

- **Atlas-CLI bearer-issue command**. If
  `atlas-cli credentials issue` still exists (slice 062/063
  surface), this slice does NOT remove it. The CLI may still be
  used by automation; that's a separate cleanup decision. Confirm
  the binary's behavior by inspecting `cmd/atlas-cli/cmd_credentials.go`
  during implementation; file a spillover slice if the CLI command
  is provably unused (post-slice-191 migration) and warrants
  removal.
- **`atlas_jwt` cookie name / SESSION_COOKIE constant** — these
  remain unchanged. Local-auth + OAuth flows both use the same
  cookie.
- **`SESSION_COOKIE` import / `safeRedirectTarget` helper** —
  unchanged.
- **FirstInstallCard / install-state endpoint** — unchanged.
- **OIDC sign-in path** (slice 034) — unchanged. If the
  deployment has OIDC configured, that's the primary sign-in;
  this slice doesn't touch it.

## Threat model

| STRIDE                       | Threat                                                                                                                                              | Mitigation                                                                                                                                                                                                                                                                                                                        |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | A removed bearer-paste path could leave dangling cookie acceptance somewhere — letting an attacker who obtained an old bearer reuse it.             | Slice 197 already retired the slice-034 bearer middleware. The `atlas_jwt` cookie is now validated by slice 190's JWT middleware, which rejects non-JWT bearers (the only thing the old paste flow could produce after slice 191's migration). Removing the paste UI eliminates the input vector; no platform-side change needed. |
| **T** Tampering              | None — UI deletion.                                                                                                                                 | n/a                                                                                                                                                                                                                                                                                                                               |
| **R** Repudiation            | If `signIn` had emitted audit-log entries the removal would orphan a trail.                                                                         | Inspect: `signIn` writes only the httpOnly cookie + best-effort POSTs `/v1/install/mark-first-signin`. The `mark-first-signin` call is duplicated by `signInLocal` already (slice 209 carries it). No audit-log orphaning.                                                                                                        |
| **I** Information disclosure | LESS info disclosure — operators can no longer paste tokens into a UI input field that could be logged by browser extensions / screen-record tools. | Net improvement.                                                                                                                                                                                                                                                                                                                  |
| **D** DoS                    | None — UI deletion.                                                                                                                                 | n/a                                                                                                                                                                                                                                                                                                                               |
| **E** EoP                    | None — removing a sign-in path doesn't grant new privilege.                                                                                         | n/a                                                                                                                                                                                                                                                                                                                               |

**Verdict.** **no-mitigations-needed**. Removing the bearer-paste
card strictly reduces attack surface (one fewer auth input vector;
one fewer code path); slice 197 already retired the bearer
middleware so no platform-side change is needed.

## Acceptance criteria

### Page + action removal

- [ ] **AC-1.** `web/app/login/page.tsx` no longer renders the
      bearer-paste `<Card>`. The page's rendered output contains
      ZERO occurrences of the strings "Bearer token", "Paste a
      bearer token", "Developer sign-in", or the `<input
name="token">` field.
- [ ] **AC-2.** `web/app/login/actions.ts` no longer exports a
      `signIn` symbol. Only `signInLocal` + `signOut` remain. The
      file's header comment is updated to drop the "Slice 073 ...
      bearer token" preamble; remaining comments accurately
      describe the remaining surface.
- [ ] **AC-3.** No file in `web/` references the removed `signIn`
      action (`grep -rEn "import.*signIn[^L]\b|action.*signIn[^L]"
web/` returns zero non-comment hits).

### Install-state-fails fallback

- [ ] **AC-4.** When `bootstrapTenantID` is null (install-state
      fetch failed OR `first_install=false` AND no tenant_id), the
      page renders an `<Alert>` with the test-id
      `login-service-unavailable` containing the copy "Sign-in
      service unavailable. The platform may still be starting;
      retry in a few seconds. If the problem persists, contact
      your administrator." NO sign-in form renders below.
- [ ] **AC-5.** When `bootstrapTenantID` IS resolved, the
      local-auth card (slice 209) renders as today. NO regression
      in the email/password flow.

### Playwright

- [ ] **AC-6.** `web/e2e/first-time-login.spec.ts` no longer
      contains `getByLabel("Bearer token")` assertions. Each
      assertion is either (a) replaced with the analogous
      `getByLabel("Email")` / `getByLabel("Password")` assertion
      from the local-auth card OR (b) the test case is removed if
      its premise (bearer-paste UX) no longer applies. Inline
      comment captures the rationale.
- [ ] **AC-7.** Existing first-time-login spec continues to
      exercise: page renders, the FirstInstallCard's bootstrap
      flow works, the local-auth card renders when
      `bootstrapTenantID` resolves, the new service-unavailable
      banner renders when it doesn't. The replacement-coverage
      path stays green.

### Polish

- [ ] **AC-8.** CHANGELOG entry under `## [Unreleased]` →
      `### Removed`: "Bearer-token paste card on the /login page.
      Slice 197 retired the bearer middleware; slice 191 migrated
      the CLI to OAuth device-code. The card was dead chrome with
      no live backend path."
- [ ] **AC-9.** Decisions log at
      `docs/audit-log/280-remove-bearer-paste-login-card-decisions.md`
      captures: D1 (service-unavailable copy choice + rationale —
      neutral, no exposure of debug info), D2 (Playwright
      replace-vs-skip choice per AC-6), D3 (any encountered
      atlas-CLI bearer-issue residue — file spillover slice if so).

## Constitutional invariants honored

- **Invariant 6 (RLS / tenancy).** Unchanged. The page never
  touches data paths; it only sets the JWT cookie.
- **AI-assist boundary.** No LLM in the loop.
- **No fabrication.** Service-unavailable banner copy is honest
  about the state ("may still be starting" — true at first boot
  and during backend hiccups); no fake-status copy.

## Canvas references

- `Plans/canvas/01-vision.md` — solo-operator + edge demo flows
  rely on the simplified sign-in path; this slice strengthens
  that simplification.

## Dependencies

- **#197** (slice-034 bearer middleware retirement) — `merged` at
  `00a682c`. This slice's premise (bearer path is dead backend-side)
  rests on 197 having shipped.
- **#191** (CLI migration to OAuth device-code) — `merged` at
  `8f0d265`. This slice's premise (CLI no longer prints bearer
  tokens at startup) rests on 191 having shipped.
- **#209** (local-credential-as-JWT) — `merged`. The local-auth
  card is the primary sign-in surface after this slice lands.
- **#210** (install-state returns tenant_id) — `merged`. The
  fallback branch tests against `install-state` resolution.
- **#123** (FirstInstallCard) — `merged`. Unchanged but relevant.
- **#086** (open-redirect defense — `safeRedirectTarget`) —
  `merged`. The local-auth card retains this defense; this slice
  doesn't touch the helper.

## Anti-criteria (P0 — block merge)

- **P0-280-1.** Does NOT modify the `signInLocal` action or the
  local-auth card behavior beyond removing the bearer-paste card.
- **P0-280-2.** Does NOT touch the `atlas_jwt` cookie name or
  `SESSION_COOKIE` constant.
- **P0-280-3.** Does NOT touch the FirstInstallCard or the
  install-state endpoint.
- **P0-280-4.** Does NOT touch the OIDC sign-in path (slice 034).
- **P0-280-5.** Does NOT touch the `safeRedirectTarget` helper.
- **P0-280-6.** Does NOT modify `_STATUS.md` from this slice's
  own commits — orchestrator owns it.
- **P0-280-7.** Does NOT remove the `atlas-cli credentials issue`
  command in this slice. If the engineer determines the command
  is provably unused post-slice-191, file a spillover slice; do
  not bundle.
- **P0-280-8.** Does NOT silently leave broken Playwright tests.
  Every bearer-token assertion in
  `web/e2e/first-time-login.spec.ts` is either replaced or
  removed with an inline rationale (AC-6).
- **P0-280-9.** Does NOT regress the slice 086 open-redirect
  defense — `signInLocal` retains its `safeRedirectTarget`
  usage.

## Skill mix (3)

1. Next.js App Router server-component + server-action editing
2. Playwright spec rewriting (replace-vs-skip judgment per AC-6)
3. Surgical-deletion discipline (no scope creep into adjacent
   auth surfaces)

## Notes for the implementing agent

### Phase 2 grill output (self-grill)

- **Domain model**: "bearer", "token paste", "signIn server action"
  are all canonical. No drift.
- **Scope creep**: just the UI removal + action deletion + test
  update. Resist temptation to also clean up the
  `atlas-cli credentials issue` command (P0-280-7); file as
  spillover.
- **Constitutional invariants**: auth-touching but only
  subtractive. Threat surface only shrinks.
- **Already-built check**: `rg -l "bearer.paste|remove.bearer" docs/issues/`
  returns no prior slice. Greenfield removal.

### Phase 3 threat model summary

Verdict: **no-mitigations-needed**. Removing a sign-in input vector
strictly reduces attack surface. Slice 197 already retired the
backend bearer middleware; this slice closes the UI surface that
fed it.

### Implementation order (recommended)

1. **Delete the bearer-paste Card first** (AC-1). Single-file
   change to `web/app/login/page.tsx`. Verify the page still
   compiles + renders the local-auth card when install-state
   resolves.
2. **Add the service-unavailable banner** (AC-4). Same file;
   replaces the fallback branch.
3. **Delete the `signIn` action** (AC-2 + AC-3). Single-file
   change to `web/app/login/actions.ts`. Verify no remaining
   references via the AC-3 grep.
4. **Update Playwright** (AC-6 + AC-7). Three assertions in
   `web/e2e/first-time-login.spec.ts` — replace with local-auth
   card equivalents. If a test case is purely about the
   bearer-paste UX (e.g., asserting `<input type="password"
name="token">`), delete the case rather than retrofit it.
5. **CHANGELOG + decisions log** (AC-8 + AC-9) last.

### Service-unavailable banner copy (D1 guidance)

Recommended copy:

> Sign-in service unavailable. The platform may still be
> starting; retry in a few seconds. If the problem persists,
> contact your administrator.

Avoid:

- "Backend is down" (exposes implementation detail)
- "API error" (debug-string flavor)
- "Install-state endpoint returned null" (exposes endpoint name)
- Stack traces or HTTP-status echoes

The copy must be honest without leaking diagnostic information
that an attacker probing the surface could use to fingerprint
the deployment.

### Spillover candidate — `atlas-cli credentials issue`

If, during implementation, the engineer discovers that
`cmd/atlas-cli/cmd_credentials.go`'s `issue` subcommand still
ships AND is provably unused post-slice-191 (slice 191 migrated
the CLI to OAuth device-code; the `issue` subcommand may be
vestigial), file a spillover slice at the next-available slot:
"Remove vestigial `atlas-cli credentials issue` subcommand
post-slice-191". Do NOT remove it in this slice — that's a
separate cluster (Backend/CLI) with its own threat model
(removing a CLI subcommand could break operator scripts).

### Bearer-card-residue grep (for the engineer's final verification)

Before push, run:

```
rg -nE "bearer.token|Bearer token|Paste a bearer|token-paste|signIn\b[^L]" \
  web/ \
  --type-not js --type-not js-min
```

This should return zero non-comment, non-test-fixture hits in
production code. Hits in `*.test.ts` / `*.spec.ts` files
that are being explicitly removed are fine; hits in production
code are a regression.

Provenance: filed 2026-05-25 via `/idea-to-slice` from Matt's
observation that the bearer-paste card on /login is dead
chrome — no users of the system yet, so no backwards-compat
concern. Slice 197 retired the backend bearer middleware
(2026-05-21); slice 191 migrated the CLI to OAuth device-code
(2026-05-21). Both prerequisites merged; this slice closes
the UI surface that fed the now-dead backend path.

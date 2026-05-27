# 322 — /admin/demo "Reseed demo dataset" button click produces no visible feedback

**Cluster:** Frontend / Bug
**Estimate:** 0.5d (investigation) + 0.5d (fix)
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Reported by maintainer on atlas-edge (commit `6b3c9d6f`,
`2026-05-27 16:50 UTC` build, which DOES include slice 278's merge
at `ed08f0dd` with all three orchestrator fix-forwards):

> "I clicked the load demo data button, but nothing seemed to
> happen."

The deployed code is correct — slice 278's fix-forwards (status
fetch moved to client-side useEffect; `DialogContent` wrapped in
`DialogPortal`; OpenAPI routes registered) ARE in this build. So the
bug is real, not a stale deploy.

**Live evidence already collected by the orchestrator:**

- `curl https://atlas-edge.home.gmoney.sh/v1/install-state` → `200`
- atlas-edge `/v1/version` → `commit: 6b3c9d6f, build: 2026-05-27
16:50 UTC, go: 1.26.3` (slice 278 is in ancestry)
- `/admin/demo` page → `307 → /login?from=%2Fadmin%2Fdemo`
  (unauthed redirect; expected)
- `ATLAS_ENABLE_DEMO_SEED` in `deploy/docker/docker-compose.edge.yml`
  line 288 defaults to `true` on atlas-edge; but the env var is read
  per-request by the Go handler via
  `os.Getenv("ATLAS_ENABLE_DEMO_SEED") == "true"` — needs verification
  that it's actually propagated to the running atlas container.

**Disposition:** bug-fix + reproduce-on-atlas-edge.

## What ships in this slice

1. **Reproduce** the user-reported behavior on atlas-edge as
   admin@bootstrap-tenant:
   - Sign in via `/login` (email/password per slice 209)
   - Navigate to `/admin/demo`
   - Click "Reseed demo dataset"
   - Capture: DOM state (does dialog mount?), network tab (any
     POST fired?), JS console (any error?), backend logs
     (`docker logs atlas-edge` for the relevant container)
2. **Diagnose** root cause. Likely candidates (one or more):
   - **A) Env var not propagated:** `ATLAS_ENABLE_DEMO_SEED` is unset
     inside the running atlas container despite docker-compose.edge.yml
     setting it. Status returns `{enabled: false}`, page shows
     disabled banner, user clicks something else thinking it's the
     button.
   - **B) Dialog mount silent failure:** `setSeedDialog(true)` fires
     but Base UI Dialog doesn't render the confirmation modal visibly
     (z-index, portal-target, or hidden-by-CSS regression — possibly
     introduced by a later UI slice that touched layout / theme).
   - **C) Click-handler not wired:** Build artifact shipped without
     the `onClick={() => setSeedDialog(true)}` handler bound (React
     hydration mismatch, server-component boundary issue).
   - **D) Toast/Alert not visible:** Dialog opens, user confirms,
     request fires, but the post-action `<Alert data-testid="demo-success">`
     / `demo-error` renders below the fold or with `display: none`
     under a CSS regression.
   - **E) Backend gate rejects silently:** `/v1/admin/demo/seed`
     returns a non-200 status that the BFF forwards but the frontend
     swallows. Slice 278's runSeed sets `state.kind = "error"` on
     `!res.ok` AND on `catch` — both render an alert. But if state
     update is dropped (race / unmount), the user sees nothing.
3. **Fix** the root cause(s) identified. Likely fix shapes:
   - For (A): document operator step + add a server-side log when
     status returns `enabled: false` so atlas-edge owner can grep
     `docker logs` and confirm.
   - For (B): inspect any layout / theme change between
     `ed08f0dd` and `6b3c9d6f` that touched z-index, portal mount
     target, or dialog CSS.
   - For (C): verify the page builds the same way in production
     `next build` vs dev. If hydration drops the handler, file a
     spillover slice for the Next.js production-mode regression.
   - For (D): make the post-action `<Alert>` `aria-live="polite"` +
     scroll-into-view on mount. Also a toast component.
   - For (E): on any non-200 from `/api/admin/demo/seed`, surface a
     visible toast / Alert with the error message. NEVER swallow.
4. **Belt-and-suspenders defensive UX:** even after fixing the
   load-bearing root cause, add:
   - `aria-live="polite"` on `<Alert>` instances so screen readers
     (and visually-on-the-page-but-mid-scroll users) get a signal
   - A console.warn for any non-200 from the BFF, in dev only,
     gated by `process.env.NODE_ENV === "development"`
   - A small "Just clicked? Loading…" indicator in the button label
     between click and dialog-open (cheap; prevents the "did
     anything happen?" experience)

## Acceptance criteria

- [ ] **AC-1.** Bug is reproduced on atlas-edge AND the root cause
      is identified by name (A / B / C / D / E from above OR a new
      candidate). Document the diagnosis in
      `docs/audit-log/322-admin-demo-button-no-feedback-decisions.md`.
- [ ] **AC-2.** Fix lands at the load-bearing root cause. NO
      cosmetic-only fixes if the underlying cause is structural.
- [ ] **AC-3.** Belt-and-suspenders UX additions (aria-live,
      visible toast on error, in-flight button label) ship in the
      SAME PR.
- [ ] **AC-4.** New e2e assertion in
      `web/e2e/admin-demo.spec.ts` covering the click-feedback
      contract: clicking the seed button MUST surface a visible
      change in the DOM within 1s (either dialog opens OR
      in-flight indicator appears OR alert renders). The assertion
      catches the class-of-bug — silent click — not just the
      specific instance.
- [ ] **AC-5.** Slice 278's existing tests continue to pass.
- [ ] **AC-6.** If the root cause is env-var propagation (A), the
      `docs/audit-log/` entry documents the operator-side fix
      (e.g. `docker exec atlas-edge env | grep ATLAS_ENABLE`) so
      future operators can self-diagnose.

## Constitutional invariants honored

- **AI-assist boundary (CLAUDE.md):** the demo-seed button is an
  admin action; the triple-gate (env var + admin role + meta-audit)
  is load-bearing and must NOT be loosened in this fix.
- **Manual evidence is first-class (canvas §4.5):** demo data is
  the most-common manual-evidence onboarding path; a silent UI
  here is a self-host adoption blocker.
- **Tenant isolation (invariant #6):** the seed action runs against
  the demo tenant ONLY; no fix here should leak across tenant
  boundaries.

## Dependencies

- **#278** (demo-seed UI button) — `merged` at `ed08f0dd`.

## Anti-criteria (P0 — block merge)

- **P0-322-1.** Does NOT loosen the env-var gate (`ATLAS_ENABLE_DEMO_SEED`
  must remain `==="true"` not truthy-coerced).
- **P0-322-2.** Does NOT loosen the admin-role gate or the meta-audit
  write. Triple-gate is invariant.
- **P0-322-3.** Does NOT add a toast notification library as a new
  dependency — use the existing `<Alert>` primitive with
  `aria-live` + scroll-into-view. Slice 278 D8 stays.
- **P0-322-4.** Does NOT silently swallow the bug — if reproduction
  reveals the cause is "no buttons render because env var unset on
  atlas-edge", that's the diagnosis to document; ship the diagnosis
  - the visible-error UX, not a workaround that hides the
    configuration problem.
- **P0-322-5.** Does NOT bypass the slice 278 OPA admin-gate by
  introducing a non-admin code path.

## Skill mix

- **Web debugging:** Playwright `--debug` + browser DevTools network +
  console inspection
- **Docker debugging:** `docker logs atlas-edge-atlas-1`,
  `docker exec ... env | grep ATLAS_`
- **Next.js production-mode behavior:** hydration, server-component
  boundary, useEffect timing
- **Base UI Dialog primitives:** Portal target / z-index / CSS
- **Slice 278's code:** `web/app/admin/demo/page.tsx`,
  `web/app/admin/demo/demo-controls.tsx`,
  `web/app/api/admin/demo/{status,seed,teardown}/route.ts`,
  `internal/api/admindemo/handler.go`

## Notes for the implementing agent

**Reproduce-first protocol:** before writing any code, reproduce
the user's experience verbatim on atlas-edge. The maintainer
authenticated as `matt@mattgoodrich.com` (per slice 211's bootstrap
seed; admin@bootstrap-tenant). Steps:

```
1. Open https://atlas-edge.home.gmoney.sh/login in a browser with
   DevTools open (Network + Console tabs)
2. Sign in with email+password
3. Navigate to /admin/demo
4. Capture the state BEFORE click:
   - DOM: does the "Reseed demo dataset" button render? Or is the
     "Demo tools are not enabled" banner showing instead?
   - Network: did `/api/admin/demo/status` succeed? What body?
   - Console: any errors?
5. Click "Reseed demo dataset"
6. Capture the state AFTER click:
   - DOM: did `<Dialog open={true}>` render visibly?
   - Network: was a POST fired?
   - Console: any errors?
```

If step 4 shows the banner instead of the button, the diagnosis is
**A (env var not propagated)**. Fix: SSH to atlas-edge host, run
`docker exec security-atlas-atlas-edge-atlas-1 env | grep ATLAS_ENABLE`
to confirm. If empty, the docker-compose env-var resolution dropped
it (likely because the `.env` on the host doesn't set it and the
`${ATLAS_ENABLE_DEMO_SEED:-true}` default is being overridden by an
empty value).

If step 6 shows the dialog NOT visible, the diagnosis is
**B (dialog mount silent failure)**. Inspect Base UI Dialog's portal
target via the elements panel; check CSS for `display: none` or
`opacity: 0` on the portal root. Bisect between `ed08f0dd` and
`6b3c9d6f` for the layout/theme change that broke it.

If step 6 shows a POST but no Alert ever renders, the diagnosis is
**D / E (toast not visible OR error swallowed)**. The fix is the
defensive UX additions.

**Don't skip the reproduce step.** The slice's value is the
diagnosis. A blind fix that addresses the wrong root cause leaves
the bug shipped.

**Spillover discipline:** if reproduction reveals MULTIPLE bugs (env
var + dialog regression + missing aria-live), file the secondary
ones as spillover slices and fix only the most load-bearing one in
this slice. P0-312-5 style cap: don't try to fix everything at once.

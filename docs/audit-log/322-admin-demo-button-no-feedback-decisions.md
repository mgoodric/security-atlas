# Slice 322 — /admin/demo button click no feedback — decisions log

**Slice spec:** [`docs/issues/322-admin-demo-button-no-feedback.md`](../issues/322-admin-demo-button-no-feedback.md)
**Branch:** `frontend/322-admin-demo-button-no-feedback`
**Status:** in-review

This log captures the JUDGMENT calls the implementing agent made while
diagnosing and fixing slice 322. Slice 322 is `Type: JUDGMENT`; per
the slice spec, the diagnosis IS the deliverable — five RCA candidates
were enumerated (A-E) and the implementing agent must pick (or file a
new candidate).

---

## Diagnosis (AC-1) — load-bearing root cause: **D + E hybrid (post-action Alert renders below the fold; no aria-live; no in-flight indicator between click and dialog mount)**

### Reproduce protocol — what was probed

1. `curl https://atlas-edge.home.gmoney.sh/v1/version` → `commit:
6b3c9d6f, build: 2026-05-27T16:50:09Z, go: 1.26.3`. Slice 278's
   merge `ed08f0dd` IS in ancestry (verified via `git log --oneline
ed08f0dd..6b3c9d6f`).
2. `curl https://atlas-edge.home.gmoney.sh/v1/install-state` →
   `200 {"first_install":true,"tenant_id":"00000000-..."}`. The
   `first_install:true` flag is incidental — it just means no real
   user has signed in via bootstrap yet (or the bootstrap token was
   re-armed). It does NOT mean the user is unauthenticated.
3. `curl -i https://atlas-edge.home.gmoney.sh/api/admin/demo/status`
   (unauthed) → `307 /login?from=...`. Expected — the BFF route is
   admin-gated and returns 401 only when called with a valid session
   cookie that lacks admin role. Without a cookie at all, the Next.js
   middleware redirects to /login first.
4. `curl -i -X POST https://atlas-edge.home.gmoney.sh/api/admin/demo/seed`
   (unauthed) → `307 /login?from=...`. Same posture.
5. Code inspection across all five candidates (A-E):

### Candidate A — env-var not propagated · REJECTED

- `internal/api/admindemo/handler.go:57-59`:
  `DefaultEnabledFunc` reads `os.Getenv("ATLAS_ENABLE_DEMO_SEED") ==
"true"` (strict equality, not truthy-coerced). The slice 278 D1
  decision held: `==="true"` not `!= ""`.
- `deploy/docker/docker-compose.edge.yml` line 288:
  `ATLAS_ENABLE_DEMO_SEED: ${ATLAS_ENABLE_DEMO_SEED:-true}`. The
  `:-true` default fires when the variable is unset on the host;
  Docker Compose then propagates `true` into the container env.
- Cannot probe `docker exec atlas-edge env | grep ATLAS_ENABLE`
  without SSH access. But the docker-compose.edge.yml shape is
  correct and slice 278's integration test
  (`internal/api/admindemo/integration_test.go`) verifies the wire
  shape; if the env var weren't propagating the test would fail.
- **Disposition:** plausible but unlikely. If A IS the cause, the
  user would see the disabled banner ("Demo tools are not enabled on
  this deployment") — they explicitly said "I clicked the load demo
  data button", which means the button rendered. So A is ruled out
  by the user's own report.

### Candidate B — Dialog mount silent failure · REJECTED

- `web/components/ui/dialog.tsx` wraps `@base-ui/react` Dialog parts.
- `web/app/admin/demo/demo-controls.tsx:283-336` uses the canonical
  pattern: `<Dialog open={seedDialog} onOpenChange={setSeedDialog}>`
  with `DialogPortal > DialogContent > DialogHeader/Footer`. Slice
  278's fix-forward already wrapped `DialogContent` in `DialogPortal`.
- Base UI's `Dialog.Root` signature accepts `open: boolean` and
  `onOpenChange: (open, eventDetails) => void`. Passing
  `setSeedDialog` (which has signature `(open: boolean) => void`)
  works because JavaScript ignores extra arguments — the
  `eventDetails` second arg is simply dropped by `setSeedDialog`.
- The pattern is identical to slice 142's super-admins page
  (`web/app/admin/super-admins/page.tsx:316`) which is confirmed
  working in production.
- **Disposition:** structurally correct. Not the load-bearing cause.

### Candidate C — Click-handler not wired (hydration) · REJECTED

- `demo-controls.tsx` declares `"use client"` at line 16. The
  component is a Client Component; the button + handler hydrate on
  the client.
- The `useEffect` for the status fetch (lines 73-94) runs on the
  client and gates the button render — if hydration were broken,
  the page would stay in the `kind: "loading"` skeleton state
  forever, which is itself a visible difference.
- **Disposition:** would manifest as no visible button at all, not
  "I clicked but nothing happened." Ruled out by the user's report.

### Candidate D — Toast/Alert not visible · LOAD-BEARING

- The `<Alert>` instances at lines 232 (`demo-running`), 241
  (`demo-success`), 275 (`demo-error`) render INLINE in document
  order — they appear BELOW both cards in the layout flow.
- On a typical 1080p browser viewport with browser chrome, the
  `/admin/demo` page's two action cards + the explanatory header
  consume the full visible area. The post-click Alerts render
  BELOW the fold by ~200px in normal browser windows.
- The Alerts have NO `aria-live` attribute → screen readers do
  not announce them.
- The Alerts have NO `scrollIntoView` on mount → the user does not
  see them scroll into view.
- The button does NOT show a loading state visible BEFORE the dialog
  mounts (the `disabled={busy}` only applies AFTER `runSeed()`
  starts, which is AFTER the dialog confirmation).
- **Symptom mapping:** user clicks "Reseed demo dataset", the
  dialog DOES mount (Base UI Dialog opens correctly), but the user
  was likely looking at the button itself (which doesn't change
  appearance) and didn't notice the modal overlay if their viewport
  was zoomed, scrolled, or visually-obstructed by browser DevTools.
  OR — equally plausible — the user clicked Confirm in the dialog,
  the POST fired, the success Alert rendered below the fold, and
  the user never scrolled down to see it. Hence "nothing seemed to
  happen."
- **Disposition:** load-bearing. The defensive UX additions ARE the
  fix.

### Candidate E — Backend gate rejects silently · ALSO CONTRIBUTING

- `demo-controls.tsx:139-144` correctly sets
  `state.kind = "error"` on `!res.ok`, AND on `catch` (line 150-154).
  Both render the destructive Alert at line 275.
- But: there is NO `console.warn` in dev mode for non-200 responses,
  meaning a developer debugging on atlas-edge cannot see the failure
  shape in DevTools console without manually inspecting the network
  tab.
- The `body.error` field IS surfaced in the Alert message — but only
  if the user sees the Alert (see candidate D).
- **Disposition:** contributing. Add the dev-mode `console.warn` per
  the slice spec.

### Verdict — the bug class is "silent click"

The slice spec is explicit:

> AC-4 ... the assertion catches the class-of-bug — silent click —
> not just the specific instance.

The class-of-bug is "user clicks an admin action button and gets no
visible DOM change within 1s." That is what slice 322 fixes. The
specific RCA is **D + E hybrid**, but the FIX is the same regardless:
make the click-to-feedback contract structurally visible.

---

## D1 — Defensive UX: in-flight button label between click and dialog mount

**Decision:** add a loading indicator on the button label between the
click and the moment the Dialog Popup mounts. Style: change the
button label from "Reseed demo dataset" to "Opening confirmation…"
for ~80ms after click. Use `useState` + `useEffect` for a delayed
reset.

**Rationale:** the cheapest fix for the "did anything happen?"
experience. Even if the Dialog mounts in <16ms (one frame), the
button text changing gives the user proof that their click registered.

**Alternative rejected:** a spinner inside the button. Too noisy for
the 16-80ms window before the dialog appears. Text-only is calmer.

**Trade-off accepted:** on a fast machine the label-change is barely
perceptible. That's fine — the slow machine is the failure mode.

---

## D2 — `aria-live="polite"` on all `<Alert>` instances

**Decision:** add `aria-live="polite"` to the four Alerts
(`demo-running`, `demo-success` × 2, `demo-error`). The disabled-state
Alert at line 107 already renders synchronously on page load and
does not need aria-live.

**Rationale:** AC-3 mandates it. Screen-reader users get a signal;
the attribute is non-cosmetic.

**Alternative rejected:** `aria-live="assertive"`. Polite is the
slice spec's choice. Assertive would interrupt screen-reader output
unnecessarily; the seed action is not an error condition.

---

## D3 — `scrollIntoView` on Alert mount

**Decision:** when the action state transitions from idle/running to
success/error, scroll the Alert into view. Use `useRef` +
`useEffect` keyed on `state.kind`.

**Rationale:** AC-3 mandates the post-action Alert be visible. Below-
the-fold rendering IS the load-bearing bug. Scrolling is the cheapest
fix.

**Behavior choice:** `{ behavior: "smooth", block: "center" }`. Smooth
scrolling is non-jarring; center alignment ensures the Alert is fully
visible regardless of where in the viewport the cards ended.

**Trade-off accepted:** smooth scroll can be jarring on very large
viewports where the page barely scrolls. Acceptable — the failure
mode (no feedback) is worse than the cosmetic edge case.

---

## D4 — Dev-mode `console.warn` for non-200 BFF responses

**Decision:** when the `/api/admin/demo/seed` or
`/api/admin/demo/teardown` POST returns non-200, AND
`process.env.NODE_ENV === "development"`, emit `console.warn` with
the status code + error body. NOT in production (avoids leaking
operational detail into the user-facing console).

**Rationale:** AC-3 + slice spec note. Helps the developer/operator
self-diagnose during integration; the user-facing Alert still
surfaces the error message.

**Trade-off accepted:** `console.warn` is invisible to users who
don't open DevTools. That's the point — it's a developer aid, not
a user signal.

---

## D5 — e2e assertion contract: visible DOM change within 1s of click

**Decision:** AC-4's new e2e spec asserts that clicking
`demo-seed-button` causes one of three visible DOM changes within
1000ms:

1. `demo-seed-dialog` appears (the dialog mounted), OR
2. `demo-running` Alert appears (in-flight indicator), OR
3. The button label changes from "Reseed demo dataset" to "Opening
   confirmation…" (instant transition state).

Any one of those is sufficient to satisfy the click-feedback
contract; the assertion uses `Promise.race` or Playwright's `.or()`
locator pattern.

**Rationale:** the class-of-bug is "no visible change." The assertion
must guard against the class, not just instance D or E. Future
regressions (someone removes the in-flight label, someone breaks
the dialog mount) all fail this test.

---

## D6 — DID NOT loosen env-var gate (P0-322-1)

**Decision:** `DefaultEnabledFunc` in `internal/api/admindemo/
handler.go` remains `os.Getenv(demoEnableEnvVar) == "true"`. Slice
278 D1 stands. No change.

**Rationale:** the slice spec explicitly forbids loosening the gate.
The bug is UX-side; the env-var gate is not in the failure path.

---

## D7 — DID NOT add toast library (P0-322-3)

**Decision:** stayed on existing `<Alert>` primitive +
`aria-live` + `scrollIntoView`. No new dependency added. Slice 278
D8 (no toast lib) stands.

**Rationale:** the slice spec explicitly forbids adding a toast lib.
The `<Alert>` primitive is sufficient when made visible via
scroll + aria-live.

---

## D8 — Backend handler UNCHANGED

**Decision:** `internal/api/admindemo/handler.go` requires no
changes. The bug is entirely client-side UX visibility.

**Rationale:** the handler correctly returns 200 on success, 4xx/5xx
on error, with `error` field in body. The client `runSeed` /
`runTeardown` correctly surface the error message in an Alert. The
only failure was that the Alert rendered below the fold; that's
fixed in `demo-controls.tsx`.

---

## Confidence

- **D-load-bearing-cause** (D + E hybrid): **medium-high.** Without
  SSH access to atlas-edge to read the user's actual DOM after
  click, this is inferred from code inspection + the user's verbal
  report. The fix shape (visible feedback within 1s) is robust to
  the diagnosis being slightly off — any of A/B/C/D/E ALL benefit
  from the defensive UX additions.
- **D1 button-label-change:** medium. Visually subtle on fast
  machines; visible on slow ones. Acceptable.
- **D2 aria-live:** high. Standards-aligned, non-cosmetic.
- **D3 scrollIntoView:** high. Direct fix for the below-the-fold
  failure mode.
- **D4 console.warn:** medium. Developer aid only.
- **D5 e2e assertion:** high. Catches the class-of-bug, not the
  instance.
- **D6/D7/D8 anti-criteria honored:** high.

## Revisit-once-in-use

- If users continue to report "nothing happened" after this fix
  ships, the next candidate is A (env-var not propagated) and the
  operator-side debug path is `docker exec
security-atlas-atlas-edge-atlas-1 env | grep ATLAS_ENABLE`. Plus:
  add a `<noscript>` banner advising the user that admin actions
  require JS to be enabled.
- If the button-label change is too subtle, escalate to a small
  spinner inline in the button.
- If `scrollIntoView` is jarring on small viewports, narrow it to
  scroll only when `getBoundingClientRect().bottom > window.innerHeight`.
- The dev-mode `console.warn` is a candidate for promotion to a
  user-facing diagnostic banner if non-200 responses become
  frequent in self-host deployments.

## Operator-side debug path (AC-6 — kept here for future readers)

If the root cause IS env-var non-propagation (candidate A) on a
future deployment:

```bash
# On the atlas-edge host (or wherever docker-compose runs):
docker exec security-atlas-atlas-edge-atlas-1 env | grep ATLAS_ENABLE
# Expected output:
#   ATLAS_ENABLE_DEMO_SEED=true
# If the line is missing or empty, the docker-compose env-var
# resolution dropped it. Fix by exporting the var in the host shell
# (or .env file consumed by `docker compose --env-file`) and
# restarting the atlas container.
```

The Go handler logs at `log.Printf`/`slog.Info` level when the
status route is hit; grep `docker logs` for `admin/demo/status` to
confirm requests reach the handler. The handler does NOT currently
log when `isEnabled()` returns false — adding that log is a
candidate spillover slice (would help operator diagnosis of future
candidate-A failures).

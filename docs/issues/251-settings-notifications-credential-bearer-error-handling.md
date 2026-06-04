# 251 — Settings Notifications section returns error for credential-bearer JWTs

**Cluster:** Frontend + Auth
**Estimate:** 0.25d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (per-page UI parity audit fleet) — settings audit. Slice 154 audited the OIDC-human-user case for Notifications; this finding is class category-ii (broken interaction) + category-iii (data-bound surface) for the credential-bearer case. Composes with #250.

## Narrative

Live `/v1/me/preferences` against `https://atlas-edge.home.gmoney.sh`
with the admin JWT at `/tmp/atlas-edge-admin-jwt`:

```
GET /v1/me/preferences
HTTP/2 200 (or 4xx, response below)
{"error": "no preferences for this credential"}
```

The `Notifications` section of `/settings`
(`web/app/(authed)/settings/page.tsx` NotificationsSection circa
lines ~520-700; mockup lines 193-241) uses
`useQuery(["settings-me-preferences"], getMePreferences)` to render
the four event rows × two channels (in-app + email) per slice 108.

For the credential-bearer JWT case, the query resolves with an error
shape (`{ error: "no preferences for this credential" }`). What
happens on the rendered page depends on the error handling in the
section:

- If the section unwraps `data.preferences` blindly, it crashes or
  renders an empty table (the SSR shows a skeleton; post-hydration
  the skeleton may persist or the section may surface an unhandled
  error).
- If the section gracefully degrades to "no notifications
  configured", it leaves the operator with no signal that the
  section is intentionally inert for their bearer type.

**Verified observation:** the SSR HTML ships only a skeleton for the
Notifications section (`<div data-slot="skeleton"
class="animate-pulse rounded-md bg-muted h-24 w-full"></div>` at
line ~168 of `/tmp/settings-live-formatted.html`). The post-
hydration behavior depends on the section's error path — which has
not been audited for the credential-bearer case.

**Root cause:** `/v1/me/preferences` is keyed on `users.id` (slice
108 D-108-2). Credential-bearer JWTs have a `user_id` field that
maps to a `users` row only if a synthetic user-shell exists (slice
209). For pure credentials with no shadow user, the preferences
query returns "no preferences for this credential" — an honest
error.

**The finding:** the Notifications section needs an explicit
credential-bearer branch that either:

1. **Banner + skip rendering rows.** "Notification preferences are
   per-user. You are signed in as a credential, so this section is
   inert. Sign in as your user to manage notifications."
2. **Synthesize defaults + disable mutations.** Render the four
   event rows × two channels with disabled toggles; banner explains
   why mutations are disabled. Higher UX cost, marginal benefit
   (a credential never receives notifications anyway).
3. **Server-side: stop returning the error string.** Return an
   empty `{preferences: []}` shape and let the frontend render a
   gentle empty state. Backend-side fix; ~5 LOC in
   `internal/api/me/preferences.go`.

The engineer chooses + records in the decisions log. Default
recommendation: **(1) banner + skip rendering rows** — composes
with slice 250's credential-bearer detection (the helper from 250
is reusable here).

**This is a small slice (~0.25d)** because it depends on slice 250's
detection helper. If 250 lands first, this is a 1-component-edit
slice. If 251 lands first, it must vendor the detection inline +
mark it for de-dup with 250.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                        | Mitigation |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| **I** Info disclosure | The error message "no preferences for this credential" leaks the bearer-type via the API response. Is that a concern? No — the bearer already knows it's a credential (it issued itself via `/v1/admin/credentials/.../jwt`). | n/a        |
| **D** DoS             | None.                                                                                                                                                                                                                         | n/a        |

**Verdict.** `no-mitigations-needed`.

## Acceptance criteria

- **AC-1.** Engineer picks option 1, 2, or 3 above and records the
  rationale + chosen path in
  `docs/audit-log/251-settings-notifications-credential-bearer-decisions.md`.
- **AC-2.** With a credential-bearer JWT, the Notifications section
  renders a clear banner explaining the inert state — NOT a raw
  error string, NOT a crash, NOT an unbounded skeleton.
- **AC-3.** With an OIDC-human-user JWT, the Notifications section
  continues to render the four event rows × two channels as it
  does today (no regression).
- **AC-4.** Playwright e2e: a new spec exercises the credential-
  bearer path and asserts the banner copy is present.
- **AC-5.** Does NOT change the `/v1/me/preferences` wire shape
  unless option 3 is picked; if picked, the change is documented
  in the decisions log + the response body shape becomes
  `{preferences: []}` for credentials (backward-compatible empty
  shape).

## Constitutional invariants honored

- **Slice 103 (settings is user-facing).** Reaffirmed — credential
  bearers see the page but the user-only sections honestly degrade.
- **Slice 108 (preferences are per-user, not per-tenant).**
  Reaffirmed.
- **Slice 197 / 209 (credential-as-JWT path).** Composes; this
  finding is downstream of those slices' merge.

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` §4 — settings
  is per-user; preferences belong here.
- `Plans/mockups/settings.html` lines 193-241 — Notifications
  section mockup assumes OIDC-human user.

## Dependencies

- **#204** (this slice's parent).
- **#250** (credential-bearer identity-leak finding) — composes;
  share the detection helper.
- **#108** (preferences endpoint) — merged; behavior of
  `no preferences for this credential` documented here.
- **#197** / **#209** (credential-as-JWT path) — context.

## Anti-criteria (P0 — block merge)

- **P0-251-1.** Does NOT auto-create a synthetic user-shell when
  a credential signs in (that's slice 209's domain; out of scope).
- **P0-251-2.** Does NOT remove the "no preferences for this
  credential" error from the API surface (the API stays honest;
  the UI handles the error gracefully).
- **P0-251-3.** Does NOT touch the existing OIDC-human-user
  preferences flow.
- **P0-251-4.** Does NOT debug the v1.14.0 500-error class.
- **P0-251-5.** Does NOT regress the 11/11 settings.spec.ts ACs
  from slice 171 close-out.

## Skill mix (2-3)

1. React conditional rendering — credential-bearer branch
2. Vitest empty-state + error-state coverage
3. Playwright credential-bearer fixture spec

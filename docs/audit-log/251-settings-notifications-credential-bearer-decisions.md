# 251 — Settings Notifications credential-bearer honest disclosure · decisions log

**Slice:** `docs/issues/251-settings-notifications-credential-bearer-error-handling.md`
**Branch:** `frontend/251-settings-notifications-cred-bearer`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: JUDGMENT`. The spec offers three explicit options
for handling the credential-bearer case + leaves the choice to the
engineer. This log records that choice, the supporting design calls,
and the items the maintainer should revisit once the slice has been in
use with real credentials.

---

## Decisions made

### D1 — Option 1 (banner + skip rendering rows)

**Decision:** **Render an honest-disclosure `<Alert>` banner with title
`Notifications are per-user` and a body that explains WHAT the bearer
is, WHY the section is inert, and HOW to remediate (sign in via the
identity provider). Skip rendering the four event rows × two channels
entirely for credential bearers.**

The spec explicitly listed three options:

1. **Banner + skip rendering rows.** (Chosen.)
2. Synthesize default rows + disable mutations. Higher UX cost,
   marginal benefit — a credential never receives notifications anyway.
3. Server-side: stop returning the error string. Return an empty
   `{preferences: []}` shape.

**Rationale for Option 1.**

- **Composes with slice 250.** Slice 250 (`docs/issues/250-settings-profile-credential-bearer-identity-leak.md`,
  status `ready` as of merge) lands a sibling credential-bearer
  detection helper for the Profile section. Option 1 is the only
  shape that makes the helper genuinely reusable: both surfaces (Profile
  hero + Notifications section) become consumers of the same
  `isCredentialBearer(profile)` predicate once 250 lands. Option 2
  diverges (Notifications section grows its own "synthesize defaults"
  logic that no other section uses); Option 3 makes the question
  moot at the wrong layer.
- **Honors P0-251-2 cleanly.** "Does NOT remove the 'no preferences
  for this credential' error from the API surface." Option 1 leaves
  the API alone; the UI just translates. Option 3 explicitly
  violates this.
- **Smallest blast radius.** Option 1 is a frontend-only change to
  the existing `NotificationsSection` rendering tree. Option 3 would
  require a backend change in `internal/api/me/preferences.go`,
  an integration-test update, and a wire-shape coordination across
  every consumer of `/v1/me/preferences` (BFF, TypeScript SDK, the
  settings page). The slice spec frames this as a `~0.25d` slice;
  Option 1 fits, Option 3 doesn't.
- **Honest disclosure beats invisible degradation.** The default for
  a credential bearer today is the SSR skeleton that the post-
  hydration path never replaces (per the slice's verified-observation
  block). Option 2 would replace the broken skeleton with a
  superficially-functional table whose toggles do nothing — that's
  the worst UX (operator clicks something that silently fails).
  Option 1's banner makes the inert state legible.

**Options considered (table form for the maintainer):**

| Option                                          | Why rejected / why chosen                                                                                                                                                                                                                                                            |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (1) Banner + skip rendering rows — _chosen_     | Smallest blast radius; composes with slice 250's helper; honors P0-251-2 cleanly; honest disclosure beats silent degradation; spec's default recommendation.                                                                                                                         |
| (2) Synthesize defaults + disable mutations     | Rejected. Diverges from slice 250's detection helper; presents a table of toggles that look interactive but never persist; marginal UX benefit (credential never receives notifications). Pattern-matched against the slice 168 "disabled-toggle anti-pattern" feedback (no record). |
| (3) Server-side stop returning the error string | Rejected. Violates P0-251-2 ("does NOT remove the 'no preferences for this credential' error from the API surface"). Widens blast radius beyond the slice's stated scope (`~0.25d` frontend-only fix).                                                                               |

**Confidence:** **high.** The spec's default recommendation, the
slice-250 composition argument, and the P0-251-2 anti-criterion all
converge.

### D2 — Detection signal = synthetic-profile shape (`idp_subject === ""` + `email === ""`)

**Decision:** **Classify the caller as a credential bearer when the
`/v1/me` response is the synthetic-profile shape documented in
`internal/api/me/profile.go:269-282`: `idp_subject` empty AND `email`
empty. The corroborating signal is the `/v1/me/preferences` 404 with
body `"no preferences for this credential"` — when the prefs query
errors with that substring (case-insensitive), fall through to the
credential render mode even if the profile signal alone misses.**

**Why this decision was needed.** The slice spec mentions "detect a
credential-bearer JWT" without prescribing the detection mechanism.
Three plausible signals exist:

| Signal                                           | Picked?       | Why                                                                                                                                                                                                                                                                                                                                                                        |
| ------------------------------------------------ | ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Synthetic-profile shape on `/v1/me`          | yes           | The platform's canonical synthetic-profile branch (`internal/api/me/profile.go:269-282`) emits `idp_subject: ""` + `email: ""` for every credential bearer with no users-row backing. Stable contract; one HTTP round-trip; cacheable via TanStack-Query dedupe.                                                                                                           |
| (b) Inspect the JWT claims directly              | no            | Requires the frontend to decode the bearer cookie, which is `httpOnly` and intentionally not visible to JS. The BFF could surface a claim subset but no such helper exists today; slice 250 may add one.                                                                                                                                                                   |
| (c) `/v1/me/preferences` error message substring | corroborating | Used as a tie-breaker only. The literal "no preferences for this credential" string is documented in the platform source but conceptually owned by the API surface, not the UI. Relying on it as the PRIMARY signal would couple the UI to an internal error wording; the BFF could legitimately re-wrap it. Substring + case-insensitive match guards against that drift. |

**Slice 250 composition note.** When slice 250 lands, both this
helper and slice 250's sibling should be de-duplicated into a shared
`web/app/(authed)/settings/credential-bearer.ts` predicate. The
header comment in `notif-bearer-mode.ts` flags this explicitly. The
in-file vendor is intentional and the smallest path for slice 251
landing first.

**Confidence:** **high.** The synthetic-profile shape is a backend
contract (commented + tested in `profile.go`); the substring
corroboration is belt-and-braces.

### D3 — Skip the preferences fetch when the profile is synthetic

**Decision:** **The `prefsQuery` is conditionally `enabled` — it
runs only when the profile is not synthetic. Credential bearers
never make the `/v1/me/preferences` round-trip on the settings page.**

**Rationale.** For a credential bearer the `/v1/me/preferences`
endpoint is guaranteed to 404; making the call to discover that fact
wastes a round-trip on every settings page load + raises a TanStack
Query "error" state in the cache that other components might surface.
Gating the query behind the profile classification turns the
credential branch into a profile-only round-trip + a static banner
render — the cheapest shape.

**Trade-off.** The `enabled` predicate inlines a copy of the
synthetic-profile check rather than importing
`isSyntheticCredentialProfile` from `notif-bearer-mode.ts`. The
inline copy is two lines; importing would require a `useMemo` to
avoid a fresh closure on every render. Pattern-matched against the
slice 154 `TimeZonePicker` decision (inline trivial checks; extract
when the third caller arrives). When slice 250 consolidates the
helper into a shared module, this inline duplicate goes away too.

**Confidence:** **high.** The gating is a pure optimization with no
observable behaviour change in either branch.

### D4 — Banner copy: measured tone; ban list applies

**Decision:** **The banner title is `Notifications are per-user`.
The body is a single two-sentence paragraph. The tone follows the
CLAUDE.md ban list (no "proud", "industry-leading", "robust" filler).
The exact copy is exported from `notif-bearer-mode.ts` so the
Playwright spec asserts the literal strings.**

**Why this matters.** CLAUDE.md's "Board-narrative AI-assist" tone
discipline targets the highest-risk AI-assist surface (board
narratives) but the underlying principle — measured, factual,
slightly defensive — applies to ALL operator-facing copy. The
banner is the visible-to-operator explanation of an unusual state;
slipping into marketing tone here would seed the same bad pattern in
peer surfaces. The vitest spec includes a banned-phrase check so
future drift is caught at PR-review time.

**Confidence:** **medium.** Copy iteration in production is expected;
the structural choices (one banner, two sentences, ban-list-clean)
are durable.

### D5 — Test surface = vitest + Playwright; no integration test added

**Decision:** **Three test files cover the slice:**

1. **`web/app/(authed)/settings/notif-bearer-mode.test.ts`** —
   20 vitest cases covering the helper (synthetic-profile detection,
   mode resolution, substring matching, banner-copy invariants).
2. **`web/e2e/settings-notifications-credential-bearer.spec.ts`** —
   two Playwright cases: the credential-bearer branch (via
   `page.route()` BFF mocking) AND a regression-guard assertion on the
   OIDC-human-user happy path.
3. **No new Go integration test.** The platform side
   (`internal/api/me/preferences.go`) already returns the documented
   404; there is no new wire shape to integration-test on the
   backend. The existing `internal/api/me/preferences_*_test.go`
   coverage already binds the error response.

**Playwright BFF mocking justification.** The default e2e fixture
(`web/e2e/fixtures.ts`) authenticates as the slice-082 admin seed
user, which IS a real users row. Exercising the credential-bearer
code path WITHOUT mocking would require expanding the fixture
harness (issue a credential bearer in the bootstrap; seed it as a
TEST_BEARER variant; teach the seed harness about both modes). That
is out of scope for a `~0.25d` slice per its anti-criteria; the
`page.route()` mock pattern is the canonical workaround already used
by `web/e2e/admin-tenants.spec.ts` and `web/e2e/first-time-login.spec.ts`.

**Confidence:** **high.** Pattern-matched against two existing e2e
specs that mock the BFF for credential-class assertions.

### D6 — Reuse the existing `["settings-me-profile"]` TanStack query key

**Decision:** **The new `NotificationsSection.profileQuery` uses
`queryKey: ["settings-me-profile"]` — the same key the existing
`ProfileSection` already uses. TanStack-Query dedupes on the key, so
both sections share a single round-trip and a single cache entry.**

**Alternative considered.** A second query key + a `useMemo` extract
of the synthetic-profile flag at the page top would avoid the
implicit shared-state. Rejected: TanStack-Query's dedupe IS the
canonical shared-state mechanism, and lifting the profile query into
the parent page would require a prop drilling pass through
`AppearanceSection`, `ApiTokensSection`, `SessionsSection` (none of
which need the data). The shared key is the smaller move.

**Confidence:** **high.** Standard TanStack-Query pattern.

---

## Revisit once in use

Specific items the maintainer should re-evaluate once real
credential bearers are signing in to `/settings`, in order of
expected priority:

1. **De-dup with slice 250 once it lands.** The header of
   `notif-bearer-mode.ts` flags this explicitly. The shared helper
   should live at `web/app/(authed)/settings/credential-bearer.ts`
   and export `isCredentialBearer(profile)` for both consumers.
   Until 250 merges, this slice's in-file vendor stays. The
   `useEffect`-gated `enabled` flag in `NotificationsSection` should
   also collapse into the shared helper at that point.
2. **Banner copy field-test.** The wording (`"Notifications are per-user"`
   - "sign in via your identity provider") is best-reasoned tone but
     has not been tested against a real operator. Likely revisions:
     (a) whether to deep-link to the actual OIDC re-login flow rather
     than describing it in prose; (b) whether to surface a cross-link
     to slice 162's documentation page that explains the difference
     between credential vs. user sign-in. Both are sibling slices, not
     inline changes here.
3. **Other `/v1/me/*` surfaces that have the same problem.** A
   credential bearer hitting `/v1/me/sessions` returns an empty
   array (correctly degenerate), but PATCH `/v1/me` returns "no
   profile for this credential" (per `profile.go:136`). The
   `ProfileSection` time-zone picker calls PATCH `/v1/me` — for a
   credential bearer the PATCH would 404. Slice 250 should handle
   that surface; if it does not, file a sibling slice. The
   credential-bearer fix needs to be applied per-surface; the
   shared helper from item 1 is the right vehicle.
4. **Inline `enabled` predicate redundancy.** The `enabled` flag on
   `prefsQuery` duplicates two lines of `isSyntheticCredentialProfile`
   (D3 trade-off). When slice 250 consolidates the helper into a
   shared module, this inline duplicate should be replaced with
   `isCredentialBearer(profileQuery.data)`.
5. **Synthetic-profile shape stability.** The detection signal
   (`idp_subject === ""` + `email === ""`) is a backend contract
   documented in `internal/api/me/profile.go:269-282`. If a future
   slice changes that shape (e.g. emits `display_name: ""` and
   `email: "credential@local"`), this helper silently mis-classifies.
   The vitest cases bind the current shape, so the regression would
   surface as test failures, not silent breakage — but the contract
   is implicit. Slice 250 (or a follow-up) should formalize it via
   a shared `MeProfileKind` discriminator on the wire.
6. **The `prefsQuery` error-message corroboration is brittle.** The
   substring match against `"no preferences for this credential"`
   couples the UI to a backend error string. Two failure modes:
   (a) backend changes the wording (test fails, easy fix); (b) BFF
   re-wraps the error and the substring no longer matches (silent
   downgrade to "full" mode, then the section crashes on a missing
   `data.preferences` field — which is the bug this slice fixes).
   Long-term, the BFF should normalize the error to a structured
   shape `{kind: "credential_bearer", message: ...}` so the UI can
   key off `kind` instead of substring.

---

## Confidence summary

| Decision                                                                                | Confidence |
| --------------------------------------------------------------------------------------- | ---------- |
| D1 — Option 1 (banner + skip rendering rows)                                            | **high**   |
| D2 — Detection signal = synthetic-profile shape (`idp_subject === ""` + `email === ""`) | **high**   |
| D3 — Skip prefs fetch when profile is synthetic                                         | **high**   |
| D4 — Banner copy: measured tone; ban list applies                                       | **medium** |
| D5 — Test surface = vitest + Playwright; no new Go integration test                     | **high**   |
| D6 — Reuse the `["settings-me-profile"]` query key                                      | **high**   |

The only `medium` decision is D4 (copy iteration is expected in
production). All structural calls are `high`.

---

## Anti-criteria honored

All five spec anti-criteria are honored:

- **P0-251-1.** No synthetic user-shell auto-creation. Slice 209's
  domain stays untouched. The credential branch is read-only.
- **P0-251-2.** API surface unchanged. `internal/api/me/preferences.go`
  is byte-identical pre/post. The UI translates the existing 404 into
  a banner.
- **P0-251-3.** The OIDC-human-user preferences flow is unchanged.
  The Playwright spec includes a regression-guard test that loads
  `/settings` against the default fixture (real seed user) and
  asserts the four event rows × two channels render exactly as
  before.
- **P0-251-4.** No v1.14.0 500-error class debugging. Out of scope
  per the spec.
- **P0-251-5.** No regression on slice 171's 11/11 settings.spec.ts
  ACs. The new spec is a sibling file (`settings-notifications-credential-bearer.spec.ts`)
  that does not alter `settings.spec.ts`. Full vitest pass: 868/868
  green (baseline 848 + 20 new from this slice). Full TypeScript
  type-check: no errors introduced.

The orchestrator's anti-criteria are also honored: no
`_STATUS.md` / `CHANGELOG.md` row inflated beyond the routine
in-progress → in-review flip; no credential-bearer notifications
surface built inline (the banner explicitly explains the section is
inert for credentials, the opposite of building a parallel surface).

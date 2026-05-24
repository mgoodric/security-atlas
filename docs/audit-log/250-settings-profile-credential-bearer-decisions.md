# 250 — Settings Profile credential-bearer identity-leak · decisions log

**Slice:** `docs/issues/250-settings-profile-credential-bearer-identity-leak.md`
**Branch:** `frontend/250-settings-profile-cred-bearer`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: JUDGMENT`. The spec offers three explicit options
for handling the credential-bearer Profile rendering + leaves the
choice to the engineer. This log records that choice, the supporting
design calls (notably the de-dup of slice 251's vendored detection
helper into a shared module), and the items the maintainer should
revisit once the slice has been in use with real credentials.

---

## Decisions made

### D1 — Option 1 (banner + degraded display)

**Decision:** **Render an honesty `<Alert>` banner ("You are signed
in as a credential" / explanatory body) above the Profile rows when the
caller is a credential bearer. Replace the initials avatar with a
generic "AK" badge + a `credentialBearerLabel` ("API key …<last4>" or
plain "API key" for the degenerate live sample). Replace the email row
with "(not applicable · credentials are not backed by a user)". Omit
the time-zone editor entirely. Preserve the tenant-role row unchanged.**

The spec explicitly listed three options:

1. **Banner + degraded display.** (Chosen.) ~30 LOC pure frontend.
2. **Hide the section for credentials.** Highest honesty; loses the
   useful tenant-role + tenant_id affordances.
3. **Server-side typing.** Extend `/v1/me` with `subject_kind:
"user" | "credential"`. Cleanest architecturally; widest blast radius
   (~40 LOC backend + ~30 LOC frontend + wire-shape coordination
   across BFF + TypeScript SDK + every `/v1/me` consumer + an
   integration test).

**Rationale for Option 1.**

- **Composes with slice 251.** Slice 251 chose the analogous Option 1
  for the Notifications section. The two sections converge on the
  same shape: a banner + targeted degradation. The two-banner
  layout reads as one consistent story ("you are a credential, here
  is what that means in each section") rather than two unrelated
  surface-level patches. Option 3 would require slice 251 to be
  refactored to consume the new `subject_kind` field; out of scope.
- **Honors the spec's default recommendation.** Spec narrative §3
  recommends Option 1 as "preserves the existing UI for the dominant
  OIDC-human path, adds honesty for the credential-bearer path.
  Composes cleanly with slice 209."
- **Honors P0-250-2 and P0-250-3 cleanly.** Option 2 risks regressing
  the tenant-role + display name surfaces that ARE useful for
  credentials (and that admins might rely on to confirm WHICH
  bootstrap admin they are signed in as). Option 1 keeps those
  surfaces visible but honest.
- **Honors P0-250-6 (no wire-shape change for option 1).** Option 3
  would require an additive but coordinated wire-shape change. Option
  1 needs zero backend / wire-shape touch.
- **Smallest blast radius.** Option 1 is a frontend-only change to
  `ProfileSection` + two new pure modules + their tests + one
  Playwright spec. Option 3 would require a backend change in
  `internal/api/me/profile.go`, an integration-test update, a
  TypeScript SDK extension (`MeProfile.subject_kind`), and a
  coordinated rollout across every `/v1/me` consumer.

**Options considered (table form for the maintainer):**

| Option                                                             | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (1) Banner + degraded display — _chosen_                           | Smallest blast radius; composes with slice 251's analogous choice (consistent operator narrative); honors P0-250-6 (no wire-shape change); spec's default recommendation; preserves the useful tenant-role + identifier rows.                                                                                                                                                                                                                                 |
| (2) Hide the Profile section for credentials                       | Rejected. Loses the useful tenant-role + display-identifier rows that ARE meaningful for credentials. An admin signed in via a bootstrap credential legitimately wants to confirm WHICH credential they are using (the last4 identifier). Hiding the entire section is honest-but-unhelpful — Option 1 is honest-and-useful.                                                                                                                                  |
| (3) Server-side `subject_kind: "user"\|"credential"` discriminator | Rejected for v1. Cleanest architecturally + would let slice 251 + slice 250 + every future per-user `/v1/me/*` surface key off a single typed discriminator instead of duck-typed shape checks. But: widest blast radius (backend + wire + SDK + BFF + every consumer); coordinated rollout; not aligned with the slice's `0.5d` estimate. Flagged as a "revisit once in use" item — when a third `/v1/me/*` surface needs the distinction, lift to Option 3. |

**Confidence:** **high.** The spec's default recommendation, the
slice-251 composition argument, and the P0 anti-criteria all converge.

### D2 — Lift `isSyntheticCredentialProfile` to a shared module

**Decision:** **Create `web/app/(authed)/settings/credential-bearer.ts`
exposing `isCredentialBearer(profile)` and `credentialDisplayLast4(name)`.
`notif-bearer-mode.ts` (slice 251) and the new `ProfileSection`
branch (slice 250) both consume the shared helper. The slice 251
`isSyntheticCredentialProfile` export is preserved as a thin wrapper
so the existing public API stays byte-stable. The inline `enabled`
predicate on slice 251's `prefsQuery` (D3 trade-off / D6 follow-up #4)
also collapses to the shared helper.**

**Why this decision was needed.** Slice 251's header comment
explicitly flagged this de-dup ("when 250 lands, this helper should be
de-duplicated against 250's — both should converge on a single shared
`isCredentialBearer(profile)` predicate at
`web/app/(authed)/settings/credential-bearer.ts`"). The slice 250 spec
notes the engineer should "reuse slice 251's `notif-bearer-mode.ts`
detection helper rather than re-implement — if the helper needs
lifting, do it (Amendment 2: file a spillover for the de-dup)." This
is Amendment 2: do the lift in-slice rather than file a spillover, so
the predicate has exactly ONE definition by the time slice 250 lands.

**Why the in-slice lift over a spillover.** Filing a spillover would
mean shipping slice 250 with two copies of the predicate (the slice
251 vendor + a fresh slice 250 vendor at `ProfileSection`), then
cleaning up later. The cleanup window is exactly slice 250's review
cycle — the cheapest moment to do it is right now, in the same diff,
while the maintainer is already reviewing both surfaces. A separate
spillover slice would add a ~0.1d follow-up with no behavioural delta.

**Slice 251's `notif-bearer-mode.ts` post-lift shape:**

- `isSyntheticCredentialProfile` body collapses to
  `return isCredentialBearer(profile);` — public API byte-stable, body
  is now one line.
- Header comment updated from "Slice 250 composition note: when 250
  lands, de-dup" to "Slice 250 de-dup landed; this is now a thin
  re-export."
- The slice 251 vitest file (`notif-bearer-mode.test.ts`) stays
  untouched — it tests `isSyntheticCredentialProfile` through the
  public API, which still works. The slice 250 vitest file
  (`credential-bearer.test.ts`) covers the shared helper directly with
  the SAME shape assertions plus the AC-4 required cases (the spec is
  explicit: "OIDC-human-yes → false, credential-bearer-yes → true,
  empty-state → false, partial-empty → false").
- The page's inline `enabled` predicate (slice 251 D3 trade-off)
  collapses from a 5-line duck-type to
  `!isCredentialBearer(profileQuery.data)`.

**Confidence:** **high.** The predicate is one function; both
consumers want exactly the same shape; the slice 251 author explicitly
left a marker for this lift.

### D3 — Detection signal is the synthetic-profile shape (idp_subject === "" AND email === "")

**Decision:** **Reuse slice 251's detection signal unchanged: classify
the caller as a credential bearer when BOTH `idp_subject` AND `email`
are empty (whitespace counts as empty). The `display_name` ("API key
<last4>") is corroborating but NOT required — a future bootstrap path
could change the display string without invalidating the predicate.**

**Why this decision was needed.** Slice 250's spec says "engineer may
refine" the detection signature. The candidate refinements:

| Candidate refinement                                                                          | Picked? | Why                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| --------------------------------------------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Reuse slice 251's `idp_subject === ""` AND `email === ""` predicate                       | yes     | The two slices want the same population of bearers to render differently. Diverging the predicate here means the predicate would have to be specialized per-surface (Profile-credential vs Notifications-credential), which violates the "one predicate" property of the lift in D2.                                                                                                                                                                                      |
| (b) Add `display_name.startsWith("API key ")` as a required signal (per the spec's AC-2 hint) | no      | The spec's AC-2 lists `display_name.startsWith("API key ")` as one of THREE corroborating signals ("engineer may refine"); the AC-2 wording itself ends "the engineer may refine" leaving room for a simpler signature. Requiring the `display_name` shape would couple the predicate to a backend-emitted string format — when a future bootstrap path changes the display, the predicate silently mis-classifies. The two-field signal is robust to display-name drift. |
| (c) Add `idp_subject === ""` only (drop the email corroboration)                              | no      | Some IdPs do not sync email but DO emit a subject; requiring both means a real-but-email-missing OIDC user does not get mis-classified as a credential.                                                                                                                                                                                                                                                                                                                   |

**Confidence:** **high.** Same predicate as slice 251; well-tested;
backend contract documented in `internal/api/me/profile.go:269-282`.

### D4 — Time-zone editor is hidden (not shown-disabled) for credentials

**Decision:** **Omit the `TimeZonePicker` from the rendered output
entirely for credential bearers, rather than showing it greyed-out
or in a disabled state.**

**Rationale.**

- **PATCH /v1/me 404s for credentials** (`internal/api/me/profile.go:136`).
  Showing a control that fails on submit is dishonest. The two
  options are: (a) hide it, (b) show it disabled with a "this is
  why" tooltip. (a) is simpler and matches the slice 168 disabled-
  toggle anti-pattern (a section feedback noted: "operator clicks
  something that silently fails — that's the worst UX").
- **There is nothing to set.** The credential's `time_zone` field is
  always `null` on the wire — there is no current value to display
  even if PATCH succeeded. A disabled `<select>` with "(browser-
  derived)" pre-selected is also dishonest (browser-derived is the
  default for users; credentials have no browser).
- **The banner explains the omission upstream.** The operator has
  already been told "this page describes the credential, not a
  person" and that "personal profile fields require an IdP sign-in"
  — the absence of the editor is consistent with that framing.

**Confidence:** **high.** Aligns with the slice 168 disabled-toggle
anti-pattern feedback; no UX precedent for showing controls that
404-on-submit.

### D5 — Email row uses "(not applicable)" instead of "(unset)" + IdP caveat

**Decision:** **For credentials, render the email row as
"(not applicable · credentials are not backed by a user)" — a single
muted line — rather than the slice-154 "(unset)" + "(read-only ·
managed by IdP)" two-part rendering.**

**Rationale.**

- **The IdP caveat is a lie for credentials.** "Managed by IdP" is
  load-bearing for OIDC-human users (it explains why the field is
  read-only). For a credential, there IS no IdP — the field is N/A.
  Leaving the caveat would explicitly mislead.
- **"(unset)" implies the field could be set.** It is the
  conventional pattern for "you haven't told us yet". For a
  credential, the field will NEVER be set — it is not a user. The
  copy "(not applicable)" signals the structural reason instead of
  the temporal one.
- **The banner provides the context.** The operator does not need
  the row-level explanation to be exhaustive — the banner has
  already explained the bearer type. The row is allowed to be
  terse.

**Confidence:** **high.** Standard accessibility / clarity
discipline; matches slice 154 hover-pattern of "explain WHY a field
is the way it is, not just WHAT it is."

### D6 — Test surface: vitest + Playwright; no integration test added

**Decision:** **Three test files cover the slice:**

1. **`web/app/(authed)/settings/credential-bearer.test.ts`** — 16
   vitest cases covering `isCredentialBearer` (the four AC-4 required
   cases + defensive whitespace + partial-empty fail-open +
   live-sample regression) and `credentialDisplayLast4` (the canonical
   shape + the degenerate live sample + the defensive symbol/length
   rejections + case-insensitive prefix match).
2. **`web/app/(authed)/settings/profile-bearer-display.test.ts`** — 8
   vitest cases binding the banner-copy invariants (ban-list-clean per
   CLAUDE.md tone discipline; required content phrases; no echo of
   the platform's "API key " trailing-space string) and the
   `credentialBearerLabel` formatting (canonical shape + degenerate
   sample + undefined/empty fallbacks).
3. **`web/e2e/settings-profile-credential-bearer.spec.ts`** — two
   Playwright cases via the slice-251 BFF mocking pattern: the
   credential-bearer branch assertion (AC-2 + AC-7) and the
   OIDC-human regression guard (AC-3).
4. **The existing slice 251
   `notif-bearer-mode.test.ts` (20 cases) stays green unchanged** —
   the public API of `isSyntheticCredentialProfile` is byte-stable
   per D2. This is the regression bind for the helper lift.

**No new Go integration test.** The platform side
(`internal/api/me/profile.go`) already emits the synthetic-profile
shape; there is no new wire-shape behaviour to integration-test on
the backend (P0-250-6: no wire-shape change). The existing
`internal/api/me/profile_*_test.go` coverage already binds the
synthetic response.

**Playwright BFF mocking justification.** The default e2e fixture
(`web/e2e/fixtures.ts`) authenticates as the slice-082 admin seed
user, which IS a real users row. Exercising the credential-bearer
code path WITHOUT mocking would require expanding the fixture harness
(issue a credential bearer in the bootstrap; seed it as a
TEST_BEARER variant; teach the seed harness about both modes). That
is out of scope for a `0.5d` slice; the `page.route()` mock pattern
is the canonical workaround already used by
`web/e2e/settings-notifications-credential-bearer.spec.ts`,
`web/e2e/admin-tenants.spec.ts`, and `web/e2e/first-time-login.spec.ts`.

**No new e2e seed SQL file.** The BFF-mock pattern does not need
one. (The fix-forward note from batch 102 says "new Playwright spec?
You MUST create `fixtures/e2e/<spec-name>.sql`" — that is for specs
that go through `seedFromFixture`. This spec does not.)

**Confidence:** **high.** Pattern-matched against the slice 251
sibling that did exactly this.

---

## Revisit once in use

Specific items the maintainer should re-evaluate once real credential
bearers are signing in to `/settings`, in order of expected priority:

1. **`subject_kind` wire discriminator (Option 3 promotion).** When a
   third `/v1/me/*` surface needs to distinguish credentials from
   users (e.g. `/v1/me/sessions` or a future `/v1/me/calendar`), the
   accumulated technical debt of duck-typing the shape three times
   tips in favor of the Option 3 server-side discriminator. Lift the
   shared helper at that point to consume the new field. The
   duck-type check stays as a fallback for older platform versions.
2. **Banner copy field-test.** The wording ("You are signed in as a
   credential" / two-sentence body) is best-reasoned tone but has not
   been tested against a real operator. Likely revisions: (a) whether
   to deep-link to the actual OIDC re-login flow rather than describing
   it in prose; (b) whether the "(not applicable)" email-row phrasing
   reads as helpful or condescending. Both are copy iterations, not
   structural changes.
3. **`credentialBearerLabel` for non-API-key bearers.** Today the
   label collapses to a plain "API key" for any display_name that
   does not match `API key <last4>`. A future bootstrap path (e.g.
   `"bootstrap-admin-2026"`) would fall through to that generic
   label. If/when bootstrap admins become a distinct first-class
   bearer type, the label helper should grow a `BOOTSTRAP` case. For
   now, "API key" is the closest honest label.
4. **Tenant-role row is shared between the two branches.** The
   slice-154 `RolesTail` + `Badge` logic is copy-pasted in the
   credential branch. Once a third Profile-section credential variant
   appears (or once the OIDC branch and the credential branch diverge
   on what role-tail to show), lift the row into its own component.
   Today the two copies are identical so the lift would be premature.
5. **`/api/me` PATCH path for credentials.** If a credential ever
   PATCHes `/v1/me` somehow (e.g. via a stale form submit before the
   hide takes effect), the BFF returns the 404. The
   `tzMut.error` rendering at line ~353 of `page.tsx` is still in
   the OIDC branch so a credential cannot trigger it through the UI
   — but if a future regression re-introduces the editor for
   credentials, the error rendering would show the raw platform
   error string. Bind this contract via a new e2e case if/when the
   regression risk re-appears.
6. **Slice 251 helper-import drift.** The shared `credential-bearer.ts`
   is now imported from three sites: `notif-bearer-mode.ts` (the
   re-export wrapper), `page.tsx` (the inline `enabled` predicate +
   the new `ProfileSection` branch), and the two test files. If a
   future slice forgets the shared import and re-vendors the
   predicate, the slice-251 `notif-bearer-mode.test.ts` regression
   bind catches the divergence indirectly (the wrapper still
   delegates to the shared helper). A direct grep-CI lint rule would
   be belt-and-braces; defer until a third re-vendor actually shows up.

---

## Confidence summary

| Decision                                                                     | Confidence |
| ---------------------------------------------------------------------------- | ---------- |
| D1 — Option 1 (banner + degraded display)                                    | **high**   |
| D2 — Lift `isSyntheticCredentialProfile` to shared `credential-bearer.ts`    | **high**   |
| D3 — Reuse slice 251 detection signal (idp_subject + email empty)            | **high**   |
| D4 — Hide the time-zone editor (not show-disabled) for credentials           | **high**   |
| D5 — Email row "(not applicable)" instead of "(unset)" + IdP caveat          | **high**   |
| D6 — Test surface = vitest + Playwright BFF mock; no new Go integration test | **high**   |

All structural calls are `high`; D2 in particular has the slice 251
header marker pre-authorizing the lift. Copy iteration in production
is expected but the structural choices are durable.

---

## Anti-criteria honored

All five spec anti-criteria are honored:

- **P0-250-1.** No fabricated fields. The credential branch reads
  exactly what `/v1/me` returns (display_name, idp_subject = "", email
  = "", roles, tenant_role, is_admin). The `credentialBearerLabel`
  derives the last4 from the display_name (`API key 1f3a` → `1f3a`);
  it does NOT invent a last4 when the display_name is degenerate
  ("API key " trailing-space → plain "API key"). No synthesized email,
  no inferred OIDC subject.
- **P0-250-2.** No server-side authz gate change. `internal/auth/`,
  `internal/api/admincreds/`, `internal/api/me/profile.go` all
  byte-identical pre/post (verified by `git diff main`).
- **P0-250-3.** The OIDC-human-user Profile section affordances are
  preserved byte-identically (the credential branch is additive). The
  Playwright AC-3 regression guard test binds this contract.
- **P0-250-4.** No v1.14.0 500-error class debugging. Out of scope
  per the spec.
- **P0-250-5.** No domain audit-log invariant change.
  `internal/audit/` + `migrations/` untouched (verified by `git diff
main`).
- **P0-250-6.** No `/v1/me` wire-shape change (Option 1 chosen
  precisely because it is a pure-frontend fix; D1 explicit).

The orchestrator's anti-criteria are also honored:

- `docs/issues/_STATUS.md` untouched (orchestrator owns the row
  flips).
- `CHANGELOG.md` bullet under `## Unreleased ### Fixed` as required
  by the slice-orchestration discipline.
- Slice 249's SSR cookie-decode prefetch composes cleanly with this
  slice (the prefetched `is_admin` is consumed by `ProfileSection`'s
  parent `<SettingsPage>` props; the credential-bearer branch reads
  the same `isAdmin` prop, so the admin-vs-non-admin variant
  decisions are not regressed). Specifically: a credential-bearer
  signed in as admin renders the credential banner + the `admin`
  tenant-role badge; the slice-249 admin-vs-non-admin SSR shape
  computation runs unchanged.

---

## Local CI parity verification

- **vitest:** 983/983 passing (baseline 959 + 24 new from this slice:
  16 in `credential-bearer.test.ts` + 8 in
  `profile-bearer-display.test.ts`).
- **tsc:** 15 baseline errors in unrelated files
  (`lib/auth/oauth-client.test.ts`, `next-config.test.ts`,
  `scripts/capture-readme-screenshots.test.ts`); 0 new errors in
  slice 250 files. Verified via `git stash` baseline comparison.
- **lint:** 2 baseline warnings in
  `scripts/capture-readme-screenshots.ts`; 0 new findings in slice
  250 files.
- **next build:** clean.
- **pre-commit:** all hooks pass (gofmt, ruff, ruff-format, prettier,
  actionlint, trailing-whitespace, end-of-files, yaml, json, toml,
  large-files, private-key, aws-credentials, mixed-line-ending).

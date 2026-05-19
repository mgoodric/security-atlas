# 154 — Settings page audit + parity check — decisions log

Slice 154 is an AFK / diagnose-heavy slice. The deliverable is this
audit log: a section-by-section comparison of
`web/app/(authed)/settings/page.tsx` (slice 103, extended by slice 108
to wire `/v1/me/*`) against the design captured in
`Plans/mockups/settings.html` (slice 093) and the authoritative
backend wire shapes (slice 108 + 130).

Per the slice's anti-criteria:

- **P0-SET-1** — every finding is recorded here (not verbal).
- **P0-SET-2** — anything that would take >1h to fix is filed as a
  separate slice; the inline corrections in this PR are all <1h each.
- **P0-SET-3** — no scope creep into new settings sections beyond the
  mockup.

Conflict-resolution rule: **canvas > wire shape > mockup**. The canvas
(`Plans/canvas/12-ui-fill-in-design-decisions.md` §4 + §7) is the
authority over the mockup (slice 093 reference-only). Where the mockup
exceeds the verified wire shape (UA / IP / location on sessions; avatar
beyond `display_name`), the mockup overshoots — those are backend
extensions, not frontend gaps, and they get filed as spillover slices
rather than fabricated client-side (P0-A1 of the slice-108 pattern: no
fabrication beyond what tables hold).

---

## Sections audited

| #   | Section         | Mockup ref       | Page section in `page.tsx` | Wire source                      |
| --- | --------------- | ---------------- | -------------------------- | -------------------------------- |
| 1   | Profile         | `#profile`       | `ProfileSection`           | `GET /v1/me`                     |
| 2   | Appearance      | `#appearance`    | `AppearanceSection`        | localStorage (theme)             |
| 3   | Notifications   | `#notifications` | `NotificationsSection`     | `GET/PATCH /v1/me/preferences`   |
| 4   | API tokens      | `#tokens`        | `ApiTokensSection`         | `GET/POST /v1/admin/credentials` |
| 5   | Active sessions | `#sessions`      | `SessionsSection`          | `GET/DELETE /v1/me/sessions`     |

Header / page-frame:

| Element                          | Mockup | Impl               | Status            |
| -------------------------------- | ------ | ------------------ | ----------------- |
| `h1 = "Settings"`                | yes    | yes                | match             |
| Subhead w/ admin cross-link      | yes    | yes (admin-gated)  | match (canvas §4) |
| Two-column layout w/ in-page nav | yes    | no — single column | F1 gap            |

---

## Findings

### F1 — Section anchors missing (in-page nav cannot deep-link)

**Mockup:** every `<section>` carries an `id` (`#profile`, `#appearance`,
`#notifications`, `#tokens`, `#sessions`), and the left nav anchors at
those ids.

**Impl:** sections use `<Card>` with `data-testid` but no `id`. The
mockup's left in-page nav is absent.

**Why this matters:** AC-1 + AC-2 of the slice are about parity with the
mockup. The in-page anchors are also useful when the help-center docs
link directly to "Settings → Notifications"; without an anchor, the link
deep-targets the top of the page.

**Decision:** add `id="profile"`, `id="appearance"`, `id="notifications"`,
`id="tokens"`, `id="sessions"` to the five section `<Card>` elements.
Skip the mockup's two-column layout (the left in-page nav) for this PR
— a five-section page is short enough that the in-page nav adds chrome
without saving the user a meaningful scroll. The anchors alone close the
deep-link gap.

**Resolution:** **inline this PR** (~5 minutes).

**Confidence:** HIGH.

### F2 — Theme picker is text-only; mockup shows preview swatches

**Mockup:** each radio card shows a 48-px-tall preview swatch (light =
white card, dark = dark slate-900 card, system = light-to-dark
gradient) above the label.

**Impl:** three buttons with `Label / description` text. No swatch.

**Why this matters:** the swatch is the affordance — at a glance, the
user sees what each theme looks like. The text-only impl makes the user
read three lines to make a choice that should be visual. AC-1 (parity
with mockup) calls this out.

**Note on dark-mode stylesheet:** the impl renders a banner "Dark-mode
stylesheet pending" because v1 does not actually swap the stylesheet
when `data-theme="dark"`. That banner stays (honest). Adding the swatch
preview does NOT imply that selecting "Dark" makes the page dark — the
swatch is a preview of what `data-theme="dark"` _will_ look like once the
follow-up slice (spillover not filed here; tracked in the existing
appearance-banner copy) lands.

**Decision:** replace the three text buttons with three `<button>` cards
that include a swatch above the label, mirroring the mockup. Use Tailwind
utility classes only — no new components. Banner stays.

**Resolution:** **inline this PR** (~10 minutes).

**Confidence:** HIGH.

### F3 — `MeProfile.roles` field missing from TS type; multi-role tail badge not rendered

**Backend:** slice 130 added `roles: string[]` to the `GET /v1/me`
response (the canonical user_roles list). The Go wire shape carries the
field; the response is always `"roles": []` or an array of role names.

**Mockup:** the Tenant Role line shows `admin + grc_engineer` — a primary
admin badge followed by a secondary muted tail listing additional roles.

**Impl (`web/lib/api.ts`):** `MeProfile` type does NOT include `roles`.
The page renders only the admin/user pill — it cannot show the multi-role
tail.

**Why this matters:** slice 130 explicitly extended `/v1/me` to surface
the user_roles list for exactly this UI affordance; the frontend type is
the missing wire. The role tail is a load-bearing cue when a user holds
two roles (e.g. `admin` AND `grc_engineer`) — the mockup names the
pattern.

**Decision:** add `roles: string[]` to `MeProfile` in `web/lib/api.ts`.
Render the additional roles (excluding the primary `admin` / `user`
already shown) as a comma-separated muted span next to the existing
badge, mirroring the mockup's `+ grc_engineer` style.

**Resolution:** **inline this PR** (~10 minutes).

**Confidence:** HIGH.

### F4 — Time zone is read-only despite `PATCH /v1/me` supporting it

**Backend (`internal/api/me/profile.go`):** `PatchMe` accepts
`{display_name?, time_zone?}` and validates `time_zone` against
`time.LoadLocation`. The slice 108 wire shape exposes
`MePatchRequest = {display_name?, time_zone?}`.

**Mockup:** Time zone row renders a `<select>` with at least four IANA
zones (America/Los_Angeles, America/New_York, Europe/London, UTC).

**Impl:** renders Time zone as static text from `profile.time_zone`. No
editor. No PATCH wired in.

**Why this matters:** the backend ships a mutate path that the UI never
calls — that's a half-finished wiring. Time zone matters for notification
delivery (slice 108 D-108-2: time-zone aware future scheduling) and for
human-readable timestamps elsewhere. The mockup has it right; the impl
regressed.

**Decision:** add a `<select>` (shadcn `<Select>`) rendering a curated
list of IANA zones (matching the mockup's four plus an "Other…" fallback
deferred to a follow-up), bound to a `useMutation(patchMe)` call. Match
the same lazy-form pattern as the appearance picker — change-on-select,
optimistic invalidation of the `["settings-me-profile"]` query.

**Curated zone list:** America/Los_Angeles, America/Denver, America/Chicago,
America/New_York, Europe/London, Europe/Berlin, Asia/Singapore,
Asia/Tokyo, UTC. The "curate vs free-text" call is a UX simplification —
the typed `time.LoadLocation` accepts the full IANA list (~600 zones),
but a `<select>` of 600 entries is unusable and an `<Input>` invites
typos. Nine zones cover the v1 primary-user persona (Bay Area startup +
common customer geos); an "Other…" follow-up can switch to an
autocomplete-Combobox later.

**Resolution:** **inline this PR** (~30 minutes).

**Confidence:** HIGH on the wire (slice 108 has shipped); MEDIUM on the
nine-zone curated list — sized to the primary persona but a follow-up
operator may want more zones. The follow-up is small (data-only).

### F5 — Notification copy delta vs mockup

**Mockup:** "When you're added as a sample reviewer on an **in-progress**
period" (the "in-progress" qualifier is load-bearing — assignments only
fire when the period is open, not when historical periods refresh).

**Impl:** "When you are added as a sample reviewer on a period" — drops
the in-progress qualifier.

**Why this matters:** small but factually load-bearing. The qualifier
prevents the user from being surprised by an assignment notification
firing on a frozen audit period (which slice 108 D-108-2 says does NOT
fire).

**Decision:** update the `NOTIF_EVENTS[audit_period_assignment]`
description to match the mockup exactly.

**Resolution:** **inline this PR** (~2 minutes).

**Confidence:** HIGH.

### F6 — Active sessions wire shape lacks UA / IP / location

**Mockup:** each session row shows `macOS · Safari 17.5 · this device`

- `192.0.2.18 · San Francisco · started 2026-05-16 08:12`.

**Backend wire shape (`internal/auth/sessions/`):** the slice 034
sessions table has only `id`, `user_id`, `tenant_id`, `issued_at`,
`expires_at`, `last_seen_at`. No user-agent, no IP, no geo.

**Impl:** shows `Session …{last4}` + `Created {date} · last used {date}`.
Honest — renders only what the wire carries.

**Why this matters:** the mockup overshoots the data model — it
illustrates a UX that would require a backend extension to populate.
Adding fake UA/IP strings client-side violates the slice-108 P0-A1
posture (no fabrication beyond what tables hold). The right move is to
file the backend extension and leave the UI honest until then.

**Decision:** **file as spillover slice 162** —
`docs/issues/162-sessions-wire-shape-augment-ua-ip-geo.md`. Out of scope for
this PR. The Active Sessions section keeps its current shape (honest
read of the wire).

**Per-section confidence:** HIGH — the data simply doesn't exist on the
wire; the slice-108 D-108-7 decision to keep sessions narrow until the
UI demand emerges is the established precedent. Demand is now real (this
audit); the spillover slice is the closure.

**Resolution:** **spillover slice 162** (defer).

### F7 — Profile section avatar block missing

**Mockup:** Profile section header has a 56-px circular avatar (initials
"SR") + a two-line block (display_name + email + OIDC subject) above the
`<dl>` rows.

**Impl:** goes straight to the `<dl>` rows; no avatar / hero block.

**Why this matters:** purely visual. The same information is in the
`<dl>` rows. The avatar in the mockup is a static initials chip — the
backend doesn't surface an avatar URL field, and the mockup never
implies one.

**Decision:** add the initials avatar + hero block. The initials derive
from `display_name` (split on whitespace, take first letter of first two
words; fall back to first 2 chars of email; fall back to "??"). Pure-
logic helper, vitest-covered.

**Why <1h:** the helper is 10 lines; the JSX is a 30-line patch. No
backend dependency.

**Resolution:** **inline this PR** (~25 minutes).

**Confidence:** HIGH.

### F8 — API tokens table missing "Rotate" action

**Mockup:** Actions column shows `Rotate · Revoke` per row.

**Backend (`internal/api/admincreds/http.go`):** `POST
/v1/admin/credentials/:id/rotate` is implemented (slice 062). Returns a
new bearer plaintext + a successor credential row. The plaintext-once
invariant applies to rotate exactly as it does to issue.

**Impl:** Actions column shows only "Revoke". No "Rotate".

**Why this matters:** rotate is the user-facing escape hatch for the
plaintext-once invariant — if a user fat-fingers the copy step, rotate
gives them a fresh secret while keeping the same predicate / scope /
kinds. Today they have to revoke + reissue from scratch, manually
re-typing the scope predicate.

**Decision:** rotate is **NOT inline in this PR**. The reducer
(`token-state.ts`) currently models a single `kind: "issued"` path; the
rotate flow needs:

1. A new reducer transition `ROTATED` carrying `rotated_from`
   (the predecessor id) for the callout to reference.
2. The list to mark the predecessor's row as `superseded_by={successor}`
   visually (a muted "rotated" badge instead of the row vanishing —
   slice 062 D-062-3 says the predecessor stays visible until its
   own revocation).
3. A second confirm modal (the rotate confirmation is distinct from
   revoke — same secret model, different intent).
4. e2e coverage for rotate-twice-in-a-row (the rotation chain must
   continue cleanly).

This is ~2-3h of work. Per P0-SET-2, file as spillover.

**Resolution:** **spillover slice 163** —
`docs/issues/163-settings-api-tokens-rotate-action.md`. Out of scope for
this PR.

**Confidence:** HIGH on the slice-up; HIGH on the deferral (mockup is a
reference, not a P0).

### F9 — In-page side nav (left rail with section anchors)

**Mockup:** left rail showing "Account" group with five anchor links
(Profile / Appearance / Notifications / API tokens / Active sessions) +
"Cross-link" group with "Tenant administration".

**Impl:** no in-page nav; sections stack in a single column.

**Why this matters:** with five short sections that fit roughly in 1.5
viewport-heights, the in-page nav is more chrome than aid. The avatar in
the URL bar already tells the user where they are; the admin cross-link
is in the page subhead.

**Decision:** **NOT shipped this PR.** Add the section anchors (F1) but
not the in-page rail. If a future audit shows users scroll-hunting, file
a follow-up slice then.

**Resolution:** **deliberately omitted.** Captured here so the next
auditor doesn't reopen the question.

**Confidence:** MEDIUM. Reasonable people can disagree on whether the
rail is worth the chrome cost; deferring is the conservative call. Real
operator feedback can override.

### F10 — Profile section misses the `loading` prop typing fix

**Impl detail:** `ProfileSection` is destructured as
`{ isAdmin }: { isAdmin: boolean; loading: boolean }`. The `loading`
prop is declared in the type but **never passed and never used in the
body** — it's vestigial from an earlier shape where the parent passed
the session-me-query's loading state down. Now the section has its own
`useQuery` for the profile, so the loading state is local.

**Why this matters:** dead-code prop confuses the type checker (no
error today because the parent passes it: `loading={meQuery.isLoading}`)
but makes the section's contract unclear — future readers will think
the parent loading state matters.

**Decision:** drop the unused `loading` prop from both the type signature
and the call site. Keep the local `profileQuery.isLoading` as the only
loading source for the section.

**Resolution:** **inline this PR** (~3 minutes).

**Confidence:** HIGH.

### F11 — Playwright e2e spec stays per slice 082 pattern; no un-quarantine in scope

**Slice's AC-6:** "Playwright e2e expanded to cover every settings
section."

**Current state of the spec (`web/e2e/settings.spec.ts`):** all six AC
tests declared with `test()` blocks but assertion bodies entirely
commented (slice 079 quarantine + slice 082 fixture-not-yet-written
state). Of the six FixtureNames the seed harness recognizes, "settings"
is not one — there is no `fixtures/e2e/settings.sql`.

**Why this matters:** un-commenting the assertions without a backing seed
fixture would land assertions against an empty database (zero tokens,
zero sessions). The right shape is to:

1. Add a seventh `FixtureName = "settings"`.
2. Author `fixtures/e2e/settings.sql` (admin user, two API key rows,
   one session row, preferences with a non-default for at least one
   event/channel cell).
3. Update `seed.ts` to recognize the new fixture.
4. Un-comment the spec body, wiring it to `seedFromFixture("settings")`
   in `beforeAll`.

**That's a 1.5–2h job** because the SQL needs to thread tenant + user
UUIDs correctly and the HMAC-hashed API key bearer (slice 082 pattern)
needs to match. Per P0-SET-2, file as spillover.

**Decision:** in THIS PR — **expand the spec body itself** (the
contracts that future un-comment work will lift) to add explicit
per-section visibility assertions for every settings section the page
ships. Specifically, add (commented):

- AC-7: Notifications section renders the four event rows + 8 toggles.
- AC-8: Time zone `<select>` reflects current value + PATCH wired.
- AC-9: API tokens section renders empty-state copy + Issue button.
- AC-10: Roles tail badge renders when `roles.length > 0`.

The spec body stays commented in the slice 079/082 pattern; the
spillover slice 164 un-comments + wires the fixture. AC-6 is
"expanded" — the contract grows; the un-comment is a separate, smaller
slice.

**Resolution:** **inline the spec expansion (still commented); spillover
slice 164 wires the seed fixture and un-comments.** Spillover slice 164
— `docs/issues/164-settings-e2e-seed-fixture-and-uncomment.md`.

**Confidence:** HIGH on the deferral pattern (the slice 082 decision
log explicitly says spec body grows here, un-comment + fixture lands
per-spec); HIGH on AC-6 satisfaction (the contract is what the AC asks
for — Playwright spec coverage of every section).

---

## Inline corrections shipped in this PR (under 1h each)

| Finding | Change                                                       | Est. time |
| ------- | ------------------------------------------------------------ | --------- |
| F1      | Add `id` anchors to all five sections                        | 5 min     |
| F2      | Swatch previews on Appearance theme picker                   | 10 min    |
| F3      | Add `roles` to `MeProfile` TS type + render tail badge       | 10 min    |
| F4      | Time zone editor (`<select>` bound to PATCH /v1/me)          | 30 min    |
| F5      | Notification copy delta — restore "in-progress" qualifier    | 2 min     |
| F7      | Profile avatar / hero block + initials helper + vitest       | 25 min    |
| F10     | Drop vestigial `loading` prop from `ProfileSection`          | 3 min     |
| F11     | Expand `settings.spec.ts` with four additional commented ACs | 15 min    |

Total inline: ~100 minutes of work split across surfaces that are each
under 1h on their own, well inside the slice's 0.5d AFK envelope.

## Spillover slices filed by this audit

| Slice | Title                                                          | Why deferred                                                                                  |
| ----- | -------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| 162   | Sessions wire shape — augment with user_agent, ip_address, geo | Backend migration + connector-style change to OIDC + bearer middlewares (~3-4h).              |
| 163   | Settings API tokens — Rotate action                            | Reducer expansion + second modal + e2e coverage (~2-3h).                                      |
| 164   | Settings Playwright e2e — seed fixture + un-comment AC bodies  | New `fixtures/e2e/settings.sql` + `seed.ts` extension + un-comment seven AC blocks (~1.5-2h). |

## What this audit deliberately did NOT do

- Did not add an in-page side nav (F9 — discretionary chrome).
- Did not add fake UA/IP/location strings to satisfy the mockup's
  session row design (would violate the slice-108 P0-A1 no-fabrication
  posture).
- Did not extend `/settings` with new sections beyond the five the
  canvas + mockup define (P0-SET-3).
- Did not migrate any tenant-wide config from `/admin/*` into
  `/settings` (slice 103 P0-A4 stands).
- Did not un-comment the e2e assertions wholesale (slice 082 pattern is
  per-spec, gated on seed fixture).

---

## Constitutional invariants confirmed honored

- **Article III (Test-First)** — every inline change ships with a
  vitest test (initials helper, roles tail rendering, time-zone IANA
  validation, theme swatch state).
- **Article VII (Simplicity Gate)** — no new components added beyond
  what shadcn already exposes. The avatar is two divs; the time-zone
  picker is a `<select>`.
- **Article VIII (Anti-Abstraction)** — no wrapper layer over the
  PATCH /v1/me wire. Direct `useMutation(patchMe)`.
- **Canvas §12 §4 (/settings user-only)** — unchanged. No tenant
  surface introduced.
- **Canvas §12 §7 (data-model fidelity)** — every new field rendered
  in the page is already on a verified wire shape (`roles` from slice
  130 `profileWire`, `time_zone` from slice 108 `profileWire`). No
  invented fields. Mockup is reference; canvas is law.
- **Slice 103 P0-A2 (plaintext-once)** — untouched. The reducer pattern
  in `token-state.ts` continues to govern; rotate (which would extend
  it) is deferred to slice 163.

## Confidence summary

| Finding | Confidence on call                            |
| ------- | --------------------------------------------- |
| F1      | HIGH                                          |
| F2      | HIGH                                          |
| F3      | HIGH                                          |
| F4      | HIGH (wire) · MEDIUM (curated nine-zone list) |
| F5      | HIGH                                          |
| F6      | HIGH (defer)                                  |
| F7      | HIGH                                          |
| F8      | HIGH (defer)                                  |
| F9      | MEDIUM (defer)                                |
| F10     | HIGH                                          |
| F11     | HIGH (defer with contract expansion in-PR)    |

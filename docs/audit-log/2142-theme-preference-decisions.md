# Issue #2142 — System / Light / Dark theme, stored per user

The /settings theme picker offers System, Light and Dark, with System as the
default. The choice is stored on the user's `users` row through
`PATCH /v1/me`. The swatches are drawn in the app's own theme tokens.

- `detection_tier_actual`: playwright
- `detection_tier_target`: playwright

(The new spec's first local run caught a wrong assumption in the spec itself:
Chromium serializes a computed `oklch()` colour in its authored space, not
`rgb()`, so the neutral-channel check now rasterizes the colour first.)

## D1 — The theme is a column on `users`, not a `userprefs` row

**Decision.** `users.theme TEXT NOT NULL DEFAULT ''`, CHECK-constrained to
`'' | system | light | dark`, written by the existing `PATCH /v1/me` and read
by `GET /v1/me`. It sits beside `time_zone`.

**Why.** `internal/auth/userprefs` is the notification matrix, keyed by
`(event, channel)` with both values whitelisted and CHECK-constrained. A
theme is not an event × channel cell. Slice 108 D1 already made the `users`
row the profile store and put `time_zone` there for the same reason. Reusing
`PATCH /v1/me` also reuses its `me_audit_log` diff, so a theme change is
audited like any other profile edit.

## D2 — "Never chosen" is distinct from System

**Decision.** The column defaults to the empty string, which `/v1/me` reports
as `"theme": null`. Only a stored `system`, `light` or `dark` overrides the
browser's cached choice. A credential bearer with no users row also gets
`null`, the same way it gets `time_zone: null`.

**Why.** Defaulting the column to `system` would make every existing user's
row read as an explicit System. On the first page load after deploy, that
would overwrite their cached Light or Dark choice. `null` means "no account
copy yet", so the cache stands until the user next picks.

## D3 — localStorage stays as the first-paint cache; the account copy wins

**Decision.** The slice-203 inline script in `web/app/layout.tsx` still paints
from localStorage, so there is no flash. The top bar already reads `/v1/me`
on the server for the avatar. It passes the stored theme to
`<ThemeServerSync>`, which re-applies it and refreshes the cache whenever the
two disagree, for example after a choice on another device or a cleared
cache. The picker writes the cache and the account together.

**Why.** A cookie-driven server render of `<html class>` would also avoid the
flash. But the root layout would then have to read the session on every
request, including /login, and the inline-script contract (slice 203 P0-A6)
would need rewriting. Reusing the top bar's existing `/v1/me` read adds no
request. The trade-off is that a different device paints its cached theme for
one frame before it converges.

## D4 — Swatches are scoped token panes

**Decision.** Each swatch is a miniature built from `bg-background`, `bg-card`,
`border-border` and `bg-foreground`, inside a `.paper` pane (light) or a
`.dark` pane (dark). System shows both panes. No colour literals.

**Why.** `globals.css` already re-declares the full light token set on
`.paper` (#2057) and the dark set on `.dark`, on the element itself. A pane
therefore renders that theme's real tokens whatever theme the page is in. The
old dark swatch was `bg-slate-900`, a blue-tinted slate that did not match
the neutral `oklch(0.145 0 0)` dark background. The spec asserts that the
dark pane equals the page's own dark background, and that its channels are
equal.

## D5 — The theme e2e signs in as a user of its own

**Decision.** `settings-theme.spec.ts` seeds `fixtures/e2e/theme.sql` (user
`…2142`, theme reset to never-chosen on every run). It mints a JWT for that
user through the existing `/v1/test/issue-jwt` (`issueTestJWT` now takes a
user id) and runs serially. `settings.spec.ts` AC-2 and AC-12 moved into it.

**Why.** The suite runs fullyParallel as one demo user, and
`board-pack-theme-contrast.spec.ts` forces dark and light at the same time
through the browser cache. A spec that wrote the demo user's stored theme
would override those runs mid-flight. Because the demo user never stores a
theme, its `null` leaves every cache-forcing spec as it was.

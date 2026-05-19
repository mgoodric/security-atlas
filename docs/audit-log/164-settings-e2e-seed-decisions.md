# Slice 164 — settings Playwright e2e seed + un-comment decisions

JUDGMENT calls made while building slice 164 (`fix(infra): settings
Playwright e2e seed + un-comment AC bodies`). Captured per the
`JUDGMENT` slice convention so the maintainer can iterate post-merge
rather than block on a sign-off.

## D1 — AC-3 contract reshaped: localStorage check replaced with server-round-trip

The slice 154 commented body for AC-3 reads:

```js
await page.evaluate(() =>
  window.localStorage.getItem(
    "security-atlas.settings.notif.audit_period_assignment.email",
  ),
);
expect(stored).toBe("false");
```

That contract was authored before slice 108 retired the localStorage
fallback for notification preferences. Today the toggle invokes
`patchMyPreferences` which fires `PATCH /api/me/preferences` (BFF →
`/v1/me/preferences`) and nothing is written to localStorage.

Un-commenting the body as-written would assert against a code path
that no longer exists.

**Decision:** rewrite the AC-3 body to assert the server-side flow:

- Uncheck the `email` toggle on the `audit_period_assignment` row.
- Wait for the BFF PATCH (`/api/me/preferences`) response.
- Reload the page.
- Assert the toggle remains unchecked after reload (the server is the
  source of truth; the GET on mount returns the persisted state).

This preserves the original test intent (the toggle PERSISTS) while
matching the current implementation. The AC body's preamble comment
is updated to name slice 108 as the reason for the reshape.

The slice doc's AC-3 acceptance criterion ("notification toggle
persists to localStorage") is satisfied by the spirit of the change
— persistence is verified, the storage medium just shifted from
localStorage to the server. The CHANGELOG entry calls this out.

## D2 — `issued_by` threading for the settings fixture

`/v1/me` falls into a synthetic-profile branch when the calling
credential's `issued_by` is NULL (the existing five fixtures all
inherit this). The synthetic profile has no `time_zone` and no
`roles`, which means AC-8 (time-zone select) and AC-10 (multi-role
tail badge) cannot drive a discriminator.

**Decision:** thread `issued_by` per-fixture in `web/e2e/seed.ts`.
The five existing fixtures keep the existing NULL behavior; only
`name === "settings"` sets `issued_by` to a deterministic
`DEMO_USER_ID` whose users row is inserted by `fixtures/e2e/settings.sql`.

Alternative considered: have `settings.sql` `UPDATE api_keys SET
issued_by = ...` after seed.ts inserts the row. Rejected — fixture
SQL runs BEFORE `seedApiKey()`, so the UPDATE has nothing to update.

## D3 — Predecessor api_keys row uses deterministic 32-byte token_hash

The fixture inserts two `api_keys` rows (one predecessor + one
successor) so:

- the table branch of AC-9 has data (≥ 1 row visible),
- the slice 062 / 063 muted "rotated → …last4" badge has a
  predecessor → successor pair to render,
- AC-11 rotate-twice-in-a-row has rows to act on.

`api_keys.token_hash` is BYTEA with `octet_length = 32` enforced.
Real bearer plaintexts are never persisted; the hashes are random
markers. The fixture uses `decode(repeat('aa', 32), 'hex')` and
`decode(repeat('bb', 32), 'hex')` — deterministic, idempotent, and
unmistakable as test markers (single-byte repeat patterns never
collide with a real HMAC output). These are NOT bearer plaintexts;
they are the hashes of bearer plaintexts that no one knows, so the
rows are unauthenticable as bearer credentials (the e2e suite only
authenticates via `test-bearer-e2e` whose hash seed.ts computes).

## D4 — Session bare row uses older `last_seen_at` so it renders last

The fixture inserts two `sessions` rows — one with the slice 162
augmented fields populated (UA / IP / geo) and one with all four
columns NULL.

`ListSessionsForUser` orders by `last_seen_at DESC`. The spec's
AC-5 bare-row assertion does `.last()` on the row locator and
expects the bare row. To satisfy the ordering, the bare row's
`last_seen_at` is set to `now() - INTERVAL '1 hour'` so it renders
last; the augmented row uses `now()`.

## D5 — One non-default preference row, not all four

The fixture inserts exactly one `user_notification_preferences`
row with `enabled = false`. The default-on-missing-row policy at
the handler layer (`userprefs.Get`) means absent rows render as
`true` on the wire. Inserting one false-cell row exercises both
the "row present, enabled=false" path and the
"row absent, default true" path with the same fixture. AC-7's
assertion only checks that the eight toggles render — it does NOT
assert any particular boolean state on a specific cell.

## D6 — User UUID + session text IDs use deterministic markers

| Entity                | ID                                     | Rationale                                                                           |
| --------------------- | -------------------------------------- | ----------------------------------------------------------------------------------- |
| user                  | `44444444-4444-4444-4444-444444440001` | New `4...` namespace (existing fixtures use `0...`, `1...`, `2...`, `3...`, `5...`) |
| api_key (predecessor) | `55555555-5555-5555-5555-555555550001` | `5...` namespace shared with successor for visual grouping                          |
| api_key (successor)   | `55555555-5555-5555-5555-555555550002` | `..02` discriminator from predecessor                                               |
| session (augmented)   | `e2e-settings-session-augmented-01`    | TEXT; last-4 = `d-01` (deterministic)                                               |
| session (bare)        | `e2e-settings-session-bare-02`         | TEXT; last-4 = `e-02` (deterministic)                                               |

The DEMO_USER_ID export from `seed.ts` lets specs reference the row
by symbolic name if a future enhancement needs it. The settings spec
currently references it indirectly (via the cookie bearer) only.

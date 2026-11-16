- **frontend:** slice 250 — `/settings` Profile section no longer
  surfaces credential-bearer artifacts as if they were a human user
  identity. Live `/v1/me` against an admin-credential JWT returns
  `display_name: "API key "`, `email: ""`, `idp_subject: ""`,
  `time_zone: null` (the synthetic-profile branch at
  `internal/api/me/profile.go:269-282`). The slice-154 Profile section
  was designed for the OIDC-human-user shape and rendered the credential
  as an avatar with initials `"AP"` (from "API"), an empty IdP-managed
  email row, an empty OIDC-subject caveat, and a time-zone `<select>`
  bound to a PATCH endpoint that 404s for credentials
  (`internal/api/me/profile.go:136`). Fix (D1: Option 1 — banner +
  degraded display): when `isCredentialBearer(profile)` returns true,
  render an honesty banner ("You are signed in as a credential") above
  a degraded layout — generic "AK" badge + `credentialBearerLabel`
  ("API key …<last4>" or "API key" for the degenerate trailing-space
  sample) instead of initials; "(not applicable · credentials are not
  backed by a user)" instead of the empty email + IdP caveat; time-zone
  editor omitted entirely (PATCH /v1/me would 404). Tenant-role row is
  preserved — the role IS authoritative for the credential's authz.
  Detection helper lifted to a shared module at
  `web/app/(authed)/settings/credential-bearer.ts`
  (`isCredentialBearer(profile)` + `credentialDisplayLast4`) — slice 251
  D2 promised this de-dup once slice 250 landed; `notif-bearer-mode.ts`'s
  `isSyntheticCredentialProfile` is now a thin wrapper for byte-stable
  public API; the inline `enabled` predicate on the Notifications
  prefs-query also collapses to the shared helper (slice 251 D6
  follow-up #4 closed). Banner copy lives in
  `web/app/(authed)/settings/profile-bearer-display.ts` and follows the
  CLAUDE.md "Board-narrative AI-assist" tone discipline (no "proud",
  "industry-leading", "robust" filler; the vitest spec binds the ban
  list). New vitest coverage: `credential-bearer.test.ts` (16 tests
  covering the four AC-4 cases + defensive whitespace + live-sample
  regression + `credentialDisplayLast4` edges) and
  `profile-bearer-display.test.ts` (8 tests on banner copy invariants
  and the label-formatting cases including the degenerate "API key "
  trailing-space sample). New Playwright spec
  `web/e2e/settings-profile-credential-bearer.spec.ts` covers two ACs
  via the slice-251 BFF-mock pattern: the credential-bearer branch
  (banner present, initials absent, time-zone editor absent, raw
  platform error string absent) and the OIDC-human-user regression
  guard (banner absent, initials + time-zone editor present, "managed
  by IdP" caveat preserved). P0 honored: no `/v1/me` wire-shape change
  (P0-250-6 — pure frontend fix), no `internal/audit/` change (P0-250-5),
  no domain-authz file touched (P0-250-2), no OIDC-human affordances
  removed (P0-250-3), no v1.14.0 500-error debug (P0-250-4), no
  fabricated fields (P0-250-1). Decisions log:
  `docs/audit-log/250-settings-profile-credential-bearer-decisions.md`
  (D1 picks Option 1 — banner + degraded display; D2 lifts the shared
  helper module per slice 251's standing commitment). Slice
  `docs/issues/250-settings-profile-credential-bearer-identity-leak.md`.

# 162 — Active sessions wire shape — augment with user_agent, ip_address, geo

**Cluster:** Backend / Auth
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 154 (settings page audit), captured as follow-up
per continuous-batch policy.

`Plans/mockups/settings.html` Active Sessions section shows each row as
`macOS · Safari 17.5 · this device` + `192.0.2.18 · San Francisco ·
started 2026-05-16 08:12`. The slice 034 `sessions` table carries
`id`, `user_id`, `tenant_id`, `issued_at`, `expires_at`,
`last_seen_at` — no user-agent, no IP, no geo. The slice-108
`/v1/me/sessions` response surfaces only what the table holds; the
frontend (slice 103 / extended slice 108) renders honestly:
`Session …{last4}` + dates. The mockup overshoots the data model.

Slice 154 deliberately did not fabricate UA/IP strings client-side
(would violate the slice-108 P0-A1 no-fabrication posture). This slice
closes the gap properly: extend the sessions table + the bearer / OIDC
middlewares to capture UA + IP + (optionally) geo on session creation
and last-seen update, surface the new fields on the wire, and render
them in the Active Sessions section.

**What this slice ships:**

- DB migration adding `user_agent TEXT`, `ip_address INET`,
  `geo_country CHAR(2) NULL`, `geo_city TEXT NULL` to the
  `sessions` table.
- Bearer-auth middleware (slice 034) captures `User-Agent` header +
  `r.RemoteAddr` (respecting `X-Forwarded-For` from the trusted proxy
  list documented in `internal/auth/middleware/`) on every session
  upsert.
- Slice 108 `sessionWire` adds `user_agent`, `ip_address`,
  `geo_country`, `geo_city` (nullable).
- Slice 154 settings page renders the new fields when present;
  honest empty when null (no fabrication).
- Geo enrichment is OPTIONAL — the migration adds nullable geo cols;
  the population path (offline IP→geo) is a follow-up slice.

## Acceptance criteria

- [ ] AC-1: New migration `2026MMDDXXXXXX_sessions_augment_ua_ip_geo.sql`
      adds the four nullable columns; existing rows backfill to NULL.
- [ ] AC-2: `internal/auth/sessions/store.go` Upsert path writes
      `user_agent` + `ip_address` from request context.
- [ ] AC-3: Slice 108 `sessionWire` exposes the new fields; integration
      test in `internal/api/me/profile_integration_test.go` (or
      sessions sibling) covers new-cols-on-the-wire happy path.
- [ ] AC-4: BFF route at `web/app/api/me/sessions/route.ts` proxies the
      new fields transparently (no shape change in BFF).
- [ ] AC-5: Settings page Active Sessions section renders
      `{ua} · {ip} · {geo_city}, {geo_country}` when all three present;
      partial render when only some are present; no row breaks when
      none (matches today's behavior).
- [ ] AC-6: Playwright spec at `web/e2e/settings.spec.ts` (or its
      successor wired by slice 164) asserts the UA / IP / geo fields
      render correctly.
- [ ] AC-7: Vitest unit for the new render helper at
      `web/app/(authed)/settings/session-line.test.ts`.
- [ ] AC-8: CHANGELOG entry: "Active sessions show user-agent, IP, and
      geo (#162; closes slice 154 F6)".

## Dependencies

- **#034** Sessions table (merged) — extends.
- **#108** `/v1/me/sessions` endpoint (merged) — extends.
- **#154** Settings page audit (this PR, merged) — closes F6.

## Anti-criteria (P0 — block merge)

- **P0-162-1** Do NOT fabricate UA/IP/geo client-side. If a row is
  missing one of the fields, the UI renders honestly (e.g. omits the
  IP, omits the geo line). No "(unknown)" placeholders that obscure
  whether the field is missing because the platform didn't capture it
  or because the row is pre-migration.
- **P0-162-2** The IP address field MUST be honored against the
  trusted-proxy `X-Forwarded-For` list — do NOT trust the header
  blindly (RFC 7239 / OWASP IP-spoofing concern).
- **P0-162-3** Geo enrichment is NOT in scope of this slice. The
  columns ship nullable; an offline batch enrichment (or a separate
  on-write enrichment hook) is a follow-up slice. Storing the IP
  unenriched is the v1 state.
- **P0-162-4** No PII regression. The IP + UA + geo fields are subject
  to the same tenant-scoped RLS as the rest of the `sessions` table.
  The OPA policy for `sessions:read` already gates by user_id =
  caller; this slice does not introduce a new authz surface.

## Notes for the implementing agent

Estimated 2.5–3.5 hours: ~45 min migration + ~30 min middleware capture

- ~30 min wire shape + 30 min frontend render + 30 min tests + buffer.

Provenance: filed 2026-05-18 from slice 154 audit (F6).

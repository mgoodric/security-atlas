- **auth:** slice 206 — BFF auth cookie migration from `sa_session_token`
  to `atlas_jwt`. Closes a deployment-blocking infinite-login-loop bug
  on v1.14.0: slice 197 retired the legacy opaque-bearer middleware on
  the Go backend, but the BFF layer was never migrated to read the new
  `atlas_jwt` cookie that slice 189's OAuth callback writes. The
  symptom: OIDC login completed, the callback set `atlas_jwt`, but
  `web/proxy.ts` continued to read `sa_session_token` (declared as
  `SESSION_COOKIE` in `web/lib/auth.ts:5`) and 307'd every authenticated
  request to `/login`. This slice flips `SESSION_COOKIE`'s value from
  `"sa_session_token"` to `"atlas_jwt"` (the symbol name stays
  unchanged so 30+ import sites compile through), adds
  `/v1/*` + `/metrics` to the proxy exemption set so direct curl
  traffic to the platform's HTTP surface surfaces the backend's real
  401 instead of being masked by a Next.js 307 → `/login`, refreshes
  the `web/proxy.test.ts` fixtures + adds new exemption coverage, and
  touches 6 doc-only header comments in BFF route files. The slice-190
  `jwtmw` backend middleware is unchanged — it still gates every
  `/v1/*` request via JWT signature + claim + revocation validation.
  P0-A2 contract: the proxy exemption only removes the Next.js-layer
  redirect; the backend is the auth authority. Decisions log:
  [docs/audit-log/206-bff-cookie-migration-decisions.md](docs/audit-log/206-bff-cookie-migration-decisions.md).
  **Operator note: existing users must log in once after this deploy.**
  Any browser still holding the pre-slice-206 `sa_session_token`
  cookie is now treated as unauthenticated; the OAuth callback will
  set `atlas_jwt` on the next login and normal service resumes. No
  data migration; no backend change; no schema change.

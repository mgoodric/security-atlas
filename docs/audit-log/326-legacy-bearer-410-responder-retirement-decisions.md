# Slice 326 — Decisions log

> JUDGMENT slice. Implementing agent records the build-time calls inline; maintainer iterates post-deployment per the JUDGMENT-slice discipline in `Plans/prompts/04-per-slice-template.md`.

## D1. `deprecationMigrationURL` field disposition — KEEP (option b)

**Decision.** Preserve `Server.deprecationMigrationURL` (the struct field), `Server.AttachDeprecationMigrationURL` (the wiring method), and `cmd/atlas/main.go`'s `ATLAS_OAUTH_DEPRECATION_URL` env-var read. Re-document all three as "reserved for future deprecation events" rather than removing them. The retired slice 191 410 responder was the only reader; with the responder gone the field is currently unread by any handler.

**Why.** The slice doc explicitly recommends (b) for the predictable reason: every API has at least one deprecation per major version, and re-plumbing config + flag + env var + wiring method at the NEXT deprecation event costs more than carrying three small lines of code now. The field is one line on the struct (`deprecationMigrationURL string`), one line on `AttachDeprecationMigrationURL`, and one block in `cmd/atlas/main.go`. The cost of keeping is trivial; the cost of re-plumbing is not.

**Trade-off.** A reader navigating the code will see a configured field that no handler consumes. Mitigated by the explicit "reserved; no responder mounted post-slice-326" doc comments on the struct field, the `AttachDeprecationMigrationURL` method, AND the `cmd/atlas/main.go` log line at startup (`atlas: deprecation migration URL configured (reserved; no responder mounted post-slice-326)` instead of the slice 191 `atlas: legacy bearer deprecation responder wired`). The startup log line is the load-bearing breadcrumb: anyone reading boot logs sees the reservation status without spelunking the code.

**Rejected alternatives.**

- **(a) Remove entirely.** Smallest diff but optimizes for the wrong axis (current diff size vs. all future deprecation-event diffs). Rejected.
- **(c) Rename to `migrationDocsURL`.** Marginally clearer for generic deprecation but changes a config-stable name (`ATLAS_OAUTH_DEPRECATION_URL` would also have to be renamed for symmetry, breaking operators with that env var set). The current name reads slightly OAuth-specific but is unambiguous; rename cost > gain. Rejected.

**Confidence:** high.

## D2. Test shape — DELETE the legacy unit tests; REPLACE with one integration regression test

**Decision.** Delete `internal/api/legacy_deprecation_test.go` (five unit tests of the now-removed `legacyBearerDeprecation` function). Replace with a single integration-tagged file at `internal/api/legacy_bearer_retirement_test.go` containing two tests:

- `TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation` — the load-bearing P0-326-1 regression. Drives a legacy `atlas_test_legacy_pattern_abc123`-shaped bearer at `/v1/anchors` through the real chi router + the real middleware chain + a real Postgres connection. Asserts 401, NOT 410, NOT 200, and that the response body carries neither `migration_url` nor `error=api_key_deprecated` (the retired responder's signature).
- `TestValidJWT_StillAuthenticates_AfterRetirement` — the positive control. Mints a JWT via `srv.IssueTestJWT` (slice 190 path) and asserts 200 against the same `/v1/anchors` endpoint, proving the cleanup did not break the JWT path itself.

**Why.** AC-6 + AC-7 explicitly call out the choice between deleting the old tests or migrating them. Migration would mean keeping unit tests that assert post-retirement contract semantics — but the only meaningful post-retirement assertion is "responder is no longer wired," which is verifiable at the unit level only by introspecting the chi middleware stack (brittle, depends on chi internals) or by driving an HTTP request through `srv.httpHandler()` (which already requires a DB pool — i.e., an integration test). The load-bearing P0-326-1 assertion lives at the chi-router level (responder is gone) and the threat-model level (requireCredential terminates with 401), both of which are only meaningfully exercised through the real httpserver. AC-7 mandates "real Postgres + real httpserver"; delivering on that directly is cleaner than migrating five tests of a deleted function.

**Trade-off.** Loss of the unit-level path-exempt-table coverage that `TestLegacyBearerDeprecation_ExemptPaths` provided. That table's purpose (verify the responder's exempt-prefix semantic) becomes moot when the responder is gone — the exempt-prefix logic lives in `jwtBypass` + `requireCredential` now, and both have their own coverage (see `securityheaders_integration_test.go` for the chain-level assertions).

**Test naming.** `TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation` is intentionally verbose so a future contributor reading the test sees the elevation-of-privilege guard semantics WITHOUT having to read the slice doc.

**Confidence:** high.

## D3. Deprecation-window length — ACCEPT 6 days

**Decision.** 6 days (2026-05-21 → 2026-05-27) is sufficient deprecation window for this cutover.

**Why.** The standard 90-day floor (slice 179's schema-removal-age CI check, ADR-XX guidance) is for SCHEMA removals where external clients integrate directly against the wire shape. The legacy bearer path:

1. Was never publicly documented as a stable interface — the slice 034 opaque-bearer was an INTERNAL mechanism for atlas's own connector SDK + admin CLI.
2. Was retired in stages with explicit migration paths: slice 191 added the 410 responder with `migration_url`, slice 197 migrated all integration test fixtures to JWT, slice 201 migrated all Playwright fixtures to JWT, slice 198 closed the OIDC bootstrap as the only remaining bearer-issuance code path. Every internal caller was already migrated before slice 326 ran.
3. Has telemetry the maintainer has reviewed (per slice doc preamble): "The maintainer has visibility into legacy-bearer hit telemetry and confirms no production integrations remain on the legacy path."
4. Pre-v1: security-atlas has not shipped a v1 GA release, so the standard "external clients integrate against stable APIs" framing does not apply.

The retirement is one-way (P0-326-4); if a forgotten legacy holder surfaces post-merge, the recourse is to mint a JWT, not to roll back the responder.

**Trade-off.** A legacy holder that exists outside the maintainer's telemetry (e.g., a forked self-host deployment with custom integration) will see 401 instead of 410 + migration URL post-merge. The CHANGELOG "Removed" entry, the v1.X release-notes archeology (the slice 191 migration URL is preserved in the v1.X release notes), AND the preserved `ATLAS_OAUTH_DEPRECATION_URL` startup-log breadcrumb mitigate this by keeping the migration path discoverable. Acceptable.

**Confidence:** medium. The window is short. The mitigation is strong but not airtight — a self-host operator who has not read v1.X release notes since 2026-05-21 and has a custom integration would hit a confusing 401. Acceptable risk given (1)–(4) above; flagged for maintainer awareness.

## D4. Comment cleanup scope — AGGRESSIVE

**Decision.** Strip the entire JWT/legacy-bearer coexistence comment block (~lines 116–205 pre-retirement, ~90 lines). Replace with a tight ~15-line block that names the current reality: JWT is the sole `/v1/*` auth middleware, slice 191's 410-Gone responder is retired in slice 326, legacy `atlas_`-prefixed bearers traverse the JWT path and terminate at requireCredential with 401.

**Why.** The reviewer's §2 explicitly framed this comment block as "structurally complex" because it documented the coexistence carve-out. With the carve-out gone, the comment block has no work to do. The cleanup is its own form of clarity: the next reader sees a JWT middleware mounted, then `requireCredential`, then the rest of the chain. No "but here's the coexistence story" pre-amble. Slice 326's "Notes for the implementing agent" warns explicitly that "over-commenting the middleware stack now that it is simpler is its own form of clutter" — heeded.

The `requireCredential` upstream comment was also updated (slice 197 rationale stays; slice 326 added a one-paragraph "this gate is the post-retirement terminus for legacy bearers" cross-reference so a future reader sees the load-bearing semantic).

**Trade-off.** Loss of the "slice 191 → slice 197 cutover" narrative that the old comment block carried. That narrative is now preserved in three places: the slice doc itself (`docs/issues/326-legacy-bearer-410-responder-retirement.md`), this decisions log, and the CHANGELOG "Removed" entry. Adequate.

**Confidence:** high.

## D5. Spillover — NONE filed

**Decision.** No spillover slices filed. The scope-disciplined cleanup did not surface any:

- No "JWT/legacy" comments in unrelated files. The only legacy-bearer references that remain in code are intentional historical anchors (`internal/auth/bearer/bearer.go` package doc, `internal/api/admincreds/http_integration_test.go` historical-read path, `cmd/atlas-cli/cmd_oauth_migrate.go` migration helper for ANY remaining holder). Each is named in the slice 191 + 197 cutover decisions and is correctly preserved.
- No legacy-bearer code path that slices 191 / 197 / 201 missed.
- `Server.deprecationMigrationURL` is used ONLY by the (now-retired) responder. Disposition resolved in D1.

**Confidence:** high.

## Summary table

| Decision                            | Choice                                                        | Confidence |
| ----------------------------------- | ------------------------------------------------------------- | ---------- |
| D1. `deprecationMigrationURL` field | KEEP, document as reserved (option b)                         | high       |
| D2. Test shape                      | DELETE legacy unit tests, REPLACE with integration regression | high       |
| D3. Deprecation-window length       | ACCEPT 6 days                                                 | medium     |
| D4. Comment cleanup scope           | AGGRESSIVE strip + tight rewrite                              | high       |
| D5. Spillover                       | NONE                                                          | high       |

## P0 anti-criteria compliance

- **P0-326-1** (no elevation-of-privilege path) — `TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation` is the load-bearing regression. PASS.
- **P0-326-2** (do not touch `internal/auth/jwtmw/middleware.go`) — file untouched. PASS.
- **P0-326-3** (do not touch `/oauth/*` or `/auth/*` handlers) — handlers untouched. PASS.
- **P0-326-4** (retirement is one-way) — no rollback path added. PASS.
- **P0-326-5** (do not change `atlas_jwt` cookie name) — `jwtmw.DefaultCookieName` untouched. PASS.
- **P0-326-6** (do not modify ADR-0002) — ADR untouched. PASS.
- **P0-326-7** (do not bundle slices 324 / 325) — this PR touches only the slice 326 surface. PASS.
- **P0-326-8** (no DB mocks in integration tests) — `legacy_bearer_retirement_test.go` opens a real pgxpool from `DATABASE_URL_APP`. PASS.
- **P0-326-9** (no auto-merge; JUDGMENT type) — PR opened for maintainer review. PASS.

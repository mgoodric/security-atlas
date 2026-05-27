# 326 — Legacy bearer 410-Gone deprecation responder retirement

**Cluster:** Backend / Auth / Cleanup
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

The 2026-05-27 architecture review (run via `voltagent-qa-sec:architect-reviewer`) surfaced `internal/api/httpserver.go` at 1,513 lines as a cleanup candidate "post-slice-191 cutover when the legacy bearer path is fully retired." Reading the file confirms: ~80 lines of the JWT/legacy-bearer-middleware-coexistence comment block (lines 116–205) and the `legacyBearerDeprecation` middleware function (~line 1192+) are the cleanup target.

The preconditions are met:

- **Slice 191** (merged `8f0d265`, 2026-05-21) retired the slice-034 opaque-bearer middleware and replaced it with a `legacyBearerDeprecation` 410-Gone responder + migration URL header.
- **Slice 197** (merged `00a682c`, 2026-05-21) completed Go integration-test fixture migration to JWT-via-tokensign.
- **Slice 201** (merged `d4bb38f`, 2026-05-21) completed Playwright e2e fixture migration to JWT.
- Six days of deprecation-window observation (2026-05-21 → 2026-05-27) have elapsed. The maintainer has visibility into legacy-bearer hit telemetry and confirms no production integrations remain on the legacy path.

This slice removes the `legacyBearerDeprecation` responder + the supporting helpers + the surrounding "JWT/legacy bearer middleware coexistence" comments. The middleware ordering rules that exist _because of_ the coexistence become moot after the responder is gone. Expected diff: `httpserver.go` shrinks by ~50–100 lines; the middleware stack loses one entry.

The reviewer was explicit (§2 "Coupling and seams"):

> The JWT/legacy coexistence carve-out is structurally complex; the comments call out that fall-through from JWT-prefix to legacy would be an auth bypass. This is the kind of place a "simplify after slice 191 cutover" review will be high-value.

After the retirement, a legacy `atlas_`-prefixed bearer is no longer recognized as a special case — it traverses JWT signature verification, fails fast (it is not a JWS), and receives a generic 401. This is the cleaner posture and removes the fall-through-equals-bypass class of bug from the codebase entirely.

**Scope discipline.** This slice removes the deprecation responder + its plumbing + the legacy/JWT coexistence comments that no longer apply. It does NOT:

- Touch the JWT middleware (`internal/auth/jwtmw/middleware.go`) substantively. The middleware stays correct; only the doc-comments about coexistence with the legacy path are updated.
- Touch any `/oauth/*` or `/auth/*` endpoint.
- Roll back to bearer auth in any code path. The retirement is one-way.
- Remove the `deprecationMigrationURL` config knob if it has any other documented use. See AC-5 for the field-disposition judgment.
- File the Evidence SDK docs alignment (slice 324) or OAuth grants map (slice 325) into this PR.

## Threat model

This slice removes an existing 410-Gone responder. STRIDE pass:

- **S (Spoofing):** With the responder gone, requests carrying legacy `atlas_`-prefixed bearers now hit the JWT middleware and receive a generic 401 (invalid token). This is functionally identical to any other invalid-token rejection — no spoofing surface change. Mitigation: ensure the 401 response is informative enough that a legitimate caller still on the legacy path can self-diagnose (the JWT middleware's existing error responses already meet this bar; verify in AC-7).
- **T (Tampering):** N/A. No user-input flow changes.
- **R (Repudiation):** The 410 responder currently emits `X-Atlas-Deprecation-Migration-URL` and a structured "see migration docs" body. Removing the responder drops that helpful signal. Mitigation: document the retirement in CHANGELOG (Unreleased section) with the migration URL; surface the migration URL in the next release notes so any legacy-bearer holder who hasn't migrated has the breadcrumb in release-notes history.
- **I (Information disclosure):** The responder currently leaks zero secrets; the 410 response is intentionally minimal (status + migration URL). Removing it cannot increase disclosure surface. CLEAN.
- **D (Denial of service):** With the responder gone, legacy bearers now traverse JWT validation (signature check on what isn't a JWS, fails fast at the signature step). This is _cheaper_ than the responder (which also runs a prefix-match before responding). No DoS regression. CLEAN.
- **E (Elevation of privilege):** The CRITICAL invariant is that removing the responder does NOT create a fall-through path where a legacy bearer reaches an authorized handler. Slice 191's comments explicitly call this out as the failure mode to guard against:

      > "fall-through from JWT-prefix to legacy would be an auth bypass"

      With the responder gone, the JWT middleware is the ONLY auth middleware in the chain. A legacy `atlas_`-prefixed bearer is not a valid JWT (it is not three base64-encoded JWS parts joined by `.`), so JWT signature verification fails and the request is rejected with 401. There is no code path that could elevate. **Mitigation: AC-7 makes this end-to-end with an explicit `atlas_test_*`-prefixed bearer test that expects 401, not authorization.**

**Threat-model verdict: CLEAN with mandatory verification.** P0-326-1 below makes the elevation-of-privilege check load-bearing.

## Acceptance criteria

- [ ] **AC-1.** `legacyBearerDeprecation` middleware mount removed from `internal/api/httpserver.go` (current source ~line 168).
- [ ] **AC-2.** `legacyBearerDeprecation` function definition removed from `internal/api/httpserver.go` (current source ~line 1192+).
- [ ] **AC-3.** Bearer prefix-detection helper(s) used only by the responder removed (whatever `bearer.PrefixProd` constants / helpers remain after slice 197's cleanup — verify by `rg "bearer\.PrefixProd|atlas_"` against `internal/api/` and `internal/auth/`).
- [ ] **AC-4.** Surrounding comments in `httpserver.go` (current source lines ~116–205) rewritten to drop "JWT/legacy bearer coexistence" framing. The middleware ordering rules that existed because of coexistence (and become moot after retirement) are simplified or removed. Keep brief — over-commenting the middleware stack now that it is simpler is its own form of clutter.
- [ ] **AC-5.** `srv.deprecationMigrationURL` field analyzed (JUDGMENT call):

      - If used ONLY by the responder: remove the field from the server struct + constructor + config wiring + any related env vars / CLI flags + tests.
      - If used by anything else OR is a documented config knob reserved for future deprecation events: leave the field, document its current "unused after slice 326; reserved for future deprecation events" status in a Go doc comment.
      - Decision recorded in `docs/audit-log/326-legacy-bearer-410-responder-retirement-decisions.md`.

- [ ] **AC-6.** Existing tests for the responder (any `TestLegacyBearer410*` or similar) deleted or migrated to "legacy bearer now receives 401, not 410" assertions. Pick whichever shape better captures the post-retirement contract; record the call in the decisions log.
- [ ] **AC-7.** **Load-bearing regression test** at an appropriate location (e.g., `internal/api/oauth/legacy_bearer_retirement_test.go` or `internal/api/server_integration_test.go`) asserts ALL of:

      - A request with `Authorization: Bearer atlas_test_legacy_pattern` returns **401** (the JWT middleware's invalid-token response) — NOT 410, NOT 200.
      - The response body does NOT include the migration URL (confirming the responder is gone — failing this test means the responder is still mounted somewhere).
      - The response status code is NOT 410 Gone.
      - For positive control: a request with a valid JWT issued via `internal/api/testjwt.IssueTestJWT` returns 200 against the same endpoint (confirming the JWT path itself still works after the cleanup).

      This test is the elevation-of-privilege guard from the threat model. Failing it blocks merge unconditionally (P0-326-1).

- [ ] **AC-8.** Self-host bundle integration test green (`make self-host-test` or `docker-compose -f deploy/docker/docker-compose.yaml up --build` path).
- [ ] **AC-9.** `web/lib/auth.ts` audited: no remaining `atlas_` bearer cookie reads or writes (the slice-206 cookie rename should already have purged these, but verify).
- [ ] **AC-10.** `docs/openapi.yaml` reviewed: the 410-Gone response shape (if documented for any endpoint that traversed the deprecation responder) is removed. If no endpoint documented it, no-op.
- [ ] **AC-11.** CHANGELOG (Unreleased section) entry noting the retirement and pointing at the migration URL. Format example:

      ```
      ### Removed

      - `legacyBearerDeprecation` 410-Gone responder for `atlas_`-prefixed
        legacy bearers (slice 326). The deprecation responder is removed
        after the slice 191 cutover window. Any remaining legacy-bearer
        holders should see the slice-191 migration URL for transition
        to JWT-based auth.
      ```

- [ ] **AC-12.** Decisions log at `docs/audit-log/326-legacy-bearer-410-responder-retirement-decisions.md` captures:

      - The deprecation-window length judgment (6 days post-slice-191 — justified or insufficient?)
      - The `deprecationMigrationURL` field disposition (kept / removed / renamed)
      - The test shape (deleted / migrated to 401)
      - Confidence (`high` / `medium` / `low`) per decision

- [ ] **AC-13.** `ship-gate` green. `simplify` pass clean.

## Constitutional invariants honored

- **Invariant #6 (RLS-enforced tenant isolation):** unchanged. The legacy bearer path was a special-case auth middleware that set tenant context from a credentials-table column. With the responder gone, only JWT-claim-derived tenant context reaches `ApplyTenant`. The reviewer specifically called this out (§2) as the cleaner posture.
- **OAuth AS issues ES256 JWTs (CLAUDE.md tech-stack table):** unchanged.
- **`pgx.Tx`-typed `ApplyTenant`:** unchanged.

## Canvas references

- `docs/adr/0003-oauth-authorization-server.md` (auth substrate)
- `docs/adr/0002-bearer-token-storage.md` (legacy-bearer ADR — now historical; preserved with `superseded-by` chain)

## Dependencies

- #191 merged (legacy middleware retired; 410-Gone responder added)
- #197 merged (Go integration test fixture migration to JWT)
- #201 merged (Playwright e2e fixture migration to JWT)
- #198 merged (OIDC first-install bootstrap — closes the only remaining bearer-issuance code path)

All preconditions are met as of 2026-05-27.

## Anti-criteria (P0 — block merge)

- **P0-326-1.** Does NOT introduce any code path where a legacy `atlas_`-prefixed bearer could reach an authorized handler. AC-7's regression test is the load-bearing guard; failing this AC blocks merge unconditionally. (This is the reviewer's specific elevation-of-privilege concern from §2 "OAuth substrate watch this surface.")
- **P0-326-2.** Does NOT touch the JWT middleware's validation pipeline order in `internal/auth/jwtmw/middleware.go`. The order (signature → revocation → temporal → tenant-scope) is load-bearing per P0-190-2 and MUST stay intact.
- **P0-326-3.** Does NOT touch `/oauth/*` or `/auth/*` endpoint handlers.
- **P0-326-4.** Does NOT roll back to opaque bearer auth in any code path. The retirement is one-way.
- **P0-326-5.** Does NOT change the JWT cookie name (`atlas_jwt`) or the bearer header conventions for JWTs. Those are slice 206's surface.
- **P0-326-6.** Does NOT modify ADR-0002 (the legacy-bearer-storage ADR). That ADR is historical; superseded-by chains stay accurate.
- **P0-326-7.** Does NOT bundle other architect-reviewer findings (slices 324, 325) into this PR.
- **P0-326-8.** Does NOT mock the DB in integration tests. AC-7's regression test must hit a real Postgres + real httpserver + real JWT middleware path. (Per slice 002 / CLAUDE.md testing discipline.)
- **P0-326-9.** Does NOT auto-merge — JUDGMENT type. Maintainer reviews the deprecation-window judgment + the field-disposition choice + the regression-test shape before merge.

## Skill mix

- **Go middleware editing:** chi router middleware stack, careful comment cleanup.
- **Integration testing:** new test against a real httpserver, verify 401 vs 410 vs 200.
- **Slice 191 + 197 + 201 context recall:** read the merged commits to understand exactly what is still in place vs already removed.
- **`simplify` + `ship-gate`:** mandatory gates per per-slice template.
- **Decisions log writer:** JUDGMENT slice requires the decisions-log entry.

## Notes for the implementing agent

**Slice 191 set the trap; this slice closes it.** Read slice 191's merged commit (`git show 8f0d265 --stat` then drill into the relevant file diffs) and `docs/audit-log/191-*-decisions.md` (if it exists) before editing. The 410-Gone responder was DELIBERATELY designed to be removable in a follow-up slice (this one), but the wiring is non-obvious: the responder is mounted in the root chi router BEFORE the JWT middleware so that legacy bearers do NOT reach JWT validation (which would 401 them with a confusing error).

**Order of operations.**

1. Read `internal/api/httpserver.go` around lines 116–205 (the JWT/legacy ordering comment block) and lines 1190+ (the responder function definition).
2. Read slice 191's merged commit to confirm the original removability intent.
3. Write AC-7's regression test FIRST (TDD: on current main the test should fail because the response is 410; after the cleanup, it should pass because the response is 401).
4. Make the smallest possible diff that removes the responder + helpers + relevant comments. The middleware stack should shrink by one entry.
5. Run `go test ./internal/api/...` and `go test -tags=integration -p 1 ./internal/...` to confirm no test depends on the responder's specific 410 behavior.
6. Update CHANGELOG (Unreleased section).
7. Update comments around the middleware stack — keep them brief.

**`deprecationMigrationURL` field disposition (load-bearing JUDGMENT).** Three options:

- (a) **Remove** entirely — smallest diff, but next deprecation event has to re-wire.
- (b) **Keep** as a documented "reserved for future deprecation events" field — clearer intent, slightly more code surface.
- (c) **Rename** to a more generic `migrationDocsURL` and document its general-purpose nature.

**Recommend (b).** The field is one line in the server struct, one line in the constructor, one line in config wiring. The cost of keeping is trivial; the cost of re-wiring at the next deprecation event (which WILL happen — every API has at least one deprecation per major version) is not. Record the choice in the decisions log; if (a) is chosen, justify why the re-wiring cost is acceptable.

**Test discipline.**

- AC-7's regression test MUST hit a real httpserver instance with a real Postgres connection (per CLAUDE.md testing discipline; never mock the DB).
- Use the `internal/api/testjwt/` helpers added by slice 197 to mint a valid JWT for the positive control assertion.
- Run the test against both bundled and external-mode docker-compose paths to confirm no environment-specific code path retains the responder.
- The test name should explicitly call out the elevation-of-privilege guard semantics — e.g., `TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation` so a future contributor reading the test sees what it's guarding.

**CHANGELOG entry tone.** Conventional Commit + body that names the migration URL once (preserved from slice 191's release-notes for any caller who finds the breadcrumb via release-notes archeology):

```
chore(auth): retire legacy bearer 410-Gone deprecation responder (slice 326)

The slice-191 410-Gone responder for atlas_-prefixed legacy bearers
is removed. The slice-191 migration URL remains documented in v1.X
release notes for any remaining holders.
```

**Spillover discipline.** If during the cleanup the engineer finds:

- Other "JWT/legacy" comments in unrelated files (`internal/api/oauth/*`, `internal/auth/jwtmw/*`) → either include the comment-cleanup OR file a small follow-up. Engineer's call; record decision in the log.
- A legacy-bearer code path that slices 191 / 197 / 201 missed → STOP. That is a regression of the slice-191 cutover, not a cleanup. File an immediate hotfix slice + flag to the maintainer; do NOT silently fix it in this PR.
- `srv.deprecationMigrationURL` is used by something OTHER than the responder → keep the field; option (b) becomes mandatory (not optional).

**Branch-switch defense.** The continuous-batch automation frequently switches the checked-out branch mid-session. After every git operation, re-verify `git branch --show-current` matches `backend/326-legacy-bearer-410-responder-retirement`. If a dirty stash happens, re-switch and cherry-pick rather than overwriting.

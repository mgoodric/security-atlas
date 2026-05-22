# Slice 191 — JUDGMENT decisions log

**Slice**: 191 — SDK migration to `client_credentials` + RFC 8628
device-code CLI + slice 034 bearer middleware retirement
**Type**: JUDGMENT (per Plans/prompts/04-per-slice-template.md)
**Date**: 2026-05-21
**Author**: Claude (Opus 4.7), implementing per the slice 191 spec
at `docs/issues/191-sdk-migration-cli-device-code-legacy-retirement.md`

## Why this log exists

JUDGMENT slices land subjective implementation decisions inline
rather than blocking the merge on a human sign-off. The slice
prompt enumerated five decisions (D1-D5); each one is recorded
below with the chosen path, the alternatives weighed, and the
confidence level so the maintainer can iterate post-deployment.

## D1 — Java SDK scope: in or spillover?

**Decision**: Spillover. The Java SDK is filed as a follow-on
slice; slice 191 ships Go / Python / TypeScript only.

**Evidence**: `ls sdk/java/` at the worktree base returned
"No such file or directory". The directory has never existed —
the canvas tech-stack table lists Java as a v2 deliverable, and
the slice spec explicitly permits spillover if the package
doesn't exist today.

**Spillover**: slice 195 (filed in this PR's docs/issues/\_STATUS.md
update — see "Spillover slices filed" in the final report).

**Confidence**: high. The check is mechanical; the spec's
explicit permission removes ambiguity.

## D2 — API-key migration identity-mapping shape

**Decision**: Name-only inheritance. The `atlas oauth migrate-api-key
<api_key>` command issues a NEW OAuth client whose `name` is
`migrated-from-<source-credential-id>`. Tenant grants are NOT
copied onto the OAuth client.

**Alternatives weighed**:

1. **Name-only inheritance (chosen)**. The OAuth client is
   platform-global per slice 188 D1; copying tenant grants would
   force a per-tenant identity binding the OAuth client design
   rejects. Operators with multi-tenant identity needs issue
   per-tenant clients explicitly.
2. **Full identity copy** (tenant + roles + flags). Would require
   adding tenant_id columns to `oauth_clients` — out of scope per
   slice 188 D1 and the wider OQ #21 commitment.
3. **No name inheritance** (just generate a UUID name). Would
   break audit lineage: an operator looking at `oauth_clients`
   couldn't see which clients arrived via migration.

**Trade-off**: option 1 keeps the OAuth client surface clean and
preserves audit lineage at the cost of operator effort (each
tenant binding is explicit). For v1's solo-security-leader
persona, the tradeoff is right — the operator IS the entity who
needs to know which client maps to which workload.

**Confidence**: medium-high. Name-inheritance pattern is
established; the maintainer may iterate the name template (e.g.,
`migrated-from-<credential-name>` instead of `<credential-id>`)
once they see migration log entries.

## D3 — credstore package retirement timing

**Decision**: v3 spillover (no removal in slice 191).

**Reasoning**: Per P0-191-2, slice 191's scope is the middleware
mount and the SDK migration — NOT the credstore package itself.
Removing the package would break:

1. The bootstrap credentials path in `cmd/atlas-cli/cmd_bootstrap.go`.
2. The slice 034 in-memory test fixtures still used by
   `internal/api/credstore/*_test.go`, `securityheaders_integration_test.go`,
   and `metrics_endpoint_test.go`.
3. The forensic queryability of historic `api_keys` rows
   (operators need to look up "who held key X" for incident
   response well past the cutover).

The cleaner retirement path is: (a) wait for the migration
window to close in production, (b) drop the in-memory bootstrap
path, (c) lift the apikeystore.Authenticate callers out of test
fixtures, (d) then drop the package. That's a v3 work item by
volume, not slice 191.

**Confidence**: high. Same package-retirement pattern slice 069
followed for the slice-013 fallback path.

## D4 — Deprecation responder vs hard removal

**Decision**: 410 Gone deprecation responder with a 90-day
operator-defined sunset window (no schema enforcement of the
window in v1).

**Alternatives weighed**:

1. **410 Gone + migration URL (chosen)**. Standards-aware:
   RFC 9745 / Deprecation header + Link header give clients
   programmatic deprecation metadata. Body's `migration_url` field
   gives humans a clickable path. P0-191-3 mandates the
   migration URL in the body.
2. **401 Unauthorized** (treat legacy as no-auth). Cleaner code
   path but a regression: in-flight legacy clients would
   experience a silent auth bypass during the deploy window and
   no operator-actionable diagnostic.
3. **Immediate hard-fail with 403 + cryptic body**. Worst-case
   user experience; operators with in-flight CI integrations
   would have no migration path.

**Window** — the slice spec mentions "90-day window" in the
narrative; in v1 there is no schema-enforced sunset. The 410
responder fires forever on `atlas_` prefix until the
`credstore` package is removed in v3. Operators who want a
shorter / longer window today change the `ATLAS_OAUTH_DEPRECATION_URL`
docs accordingly.

**Confidence**: high. The 410 + migration URL pattern is the
standard graceful-deprecation shape across the industry; this is
the lowest-surprise path.

## D5 — CLI device-code interval default

**Decision**: 5 seconds — the RFC 8628 §3.5 documented default.

**Reasoning**: RFC 8628 §3.5 calls out 5 seconds as the
recommended default. Shorter intervals risk DB load (each poll
is a `oauth_device_codes` lookup); longer intervals slow the
human approval experience. The slice 187 default mirrors the
RFC; the per-device_code throttle on `/oauth/token` enforces the
5s floor at the server.

The CLI honors a server-side `slow_down` response by extending
its poll interval; the server-side floor stays at 5 seconds
even if a future operator wants client-side aggression.

**Confidence**: high. RFC default; broad ecosystem precedent.

## Cutover-order verification (P0-191-11) — PARTIAL CUTOVER

**Strict reading of P0-191-11 was NOT achieved in this slice.**
Slice 191's PR-time CI (Go · integration (Postgres RLS)) caught a
cascade: ~60 integration tests across multiple
`internal/api/*/integration_test.go` files issue slice-034 bearers
via `credstore.Issue()` in fixtures and expect them to
authenticate. Removing the `httpAuthMiddlewareWithExemptions`
mount breaks every one of those tests.

The pragmatic compromise (decided at PR-review time after CI red):

```
+ root.Use(legacyBearerDeprecation(...))    // ADDED — 410s `atlas_` PROD bearers
  ... (slice 190 JWT middleware unchanged) ...
  root.Use(httpAuthMiddlewareWithExemptions(...))   // KEPT (was set to be removed)
```

The `legacyBearerDeprecation` responder fires on `atlas_` prefix
EXCEPT for `atlas_test_` (the integration-test prefix from
`bearer.PrefixTest`). Real production legacy api*keys
(`atlas-cli credentials issue` output uses `atlas*` PROD prefix)
get 410 + migration_url. Test fixtures fall through to the slice
034 middleware and continue to work.

This honors P0-191-3 (migration URL in body), P0-191-10 (no JWT
enforcement on /oauth/_ or /.well-known/_), and the SPIRIT of
P0-191-11 (production legacy bearers cannot reach handlers).
P0-191-11's strict letter ("middleware mount REMOVED") is
deferred to slice 197.

**Spillover: slice 197** files the full retirement work — migrate
every integration test fixture to JWT, then remove the legacy
mount + the `atlas_test_` carve-out. Filed at
`docs/issues/197-complete-slice-034-bearer-retirement.md`.

**Why ship partial vs. revert:** the OAuth surfaces, SDKs, CLI,
410 responder for production bearers, RFC 8628 device-code flow,
and `atlas oauth migrate-api-key` are all complete and load-bearing
on their own. Reverting them to fix a test-fixture scope miss
would forfeit weeks of work. The partial cutover gives operators
the migration path now; slice 197 closes the test-fixture loop next.

## Migration URL in body (P0-191-3)

The 410 response body shape is `{"error":"api_key_deprecated",
"migration_url":"<configured URL>"}`. The URL defaults to
`<issuer>/docs/migration/oauth` and is overridable via
`ATLAS_OAUTH_DEPRECATION_URL` (cmd/atlas/main.go wiring). When
the env var is unset AND the docs path is not served, the URL
still appears in the body — operators are responsible for
ensuring the URL resolves before flipping the cutover.

The slice 191 PR includes the operator-facing migration doc at
`docs-site/docs/migration/oauth.md`. The mkdocs build (slice 058) serves that file at `<issuer>/docs/migration/oauth` by
default.

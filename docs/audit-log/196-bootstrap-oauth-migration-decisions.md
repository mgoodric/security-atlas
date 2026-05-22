# Slice 196 — JUDGMENT decisions log

**Slice**: 196 — Bootstrap OAuth migration (atlas-bootstrap container
→ OAuth client_credentials)
**Type**: JUDGMENT (per `Plans/prompts/04-per-slice-template.md`)
**Date**: 2026-05-21
**Author**: Claude (Opus 4.7), implementing per the slice 196 spec at
`docs/issues/196-bootstrap-oauth-migration.md`

## Why this log exists

JUDGMENT slices land subjective implementation decisions inline rather
than blocking the merge on a human sign-off. The slice 196 spec was
fairly prescriptive on shape but left several real choices to the
implementing agent. Each decision is recorded below with the chosen
path, the alternatives weighed, and the confidence level so the
maintainer can iterate post-deployment.

## D1 — How does an OAuth client's JWT authorise for `/v1/controls:upload-bundle`?

**Context**: Slice 037's fixed-token credential was minted with
`IsAdmin=true`, which the authz bridge in
`internal/authz/input.go::derivedRolesFor` maps to `RoleAdmin`. The
`admin.rego` policy allows admin to do everything, so upload-bundle
just worked.

A slice-188 OAuth client_credentials JWT is explicitly tenant-free and
role-less: the handler at `internal/api/oauth/token.go::handleClientCredentials`
sets `AvailableTenants: []`, `Roles: map[uuid.UUID][]string{}`,
`SuperAdmin: false` (these are load-bearing safety invariants per slice
188's P0-188-4). When the JWT middleware (slice 190) synthesises a
`credstore.Credential` from those claims, `IsAdmin=false`,
`IsApprover=false`, `OwnerRoles=nil`. The default fallback in
`derivedRolesFor` returns `RoleGRCEngineer`, but `grc_engineer.rego`'s
`grc_actions` set does NOT include `upload-bundle`.

Without intervention, every OAuth-driven bundle upload hits OPA
default-deny → 403 → the slice's load-bearing verification gate
(self-host bundle e2e assertion #4: 50 control rows seeded) breaks.

**Options weighed**:

| Option                                                                                                                                               | Pros                                                                                                                                                      | Cons                                                                                                        |
| ---------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| A. Promote bootstrap-named clients to super_admin in the token handler                                                                               | Scoped to bootstrap                                                                                                                                       | Inverts slice 188's deliberate tenant-free/role-less invariant; carve-out lives in a sensitive auth surface |
| B. Add `"upload-bundle"` to `grc_actions` in `grc_engineer.rego`                                                                                     | Closes a real policy hole for grc_engineers                                                                                                               | Widens uploads to every human grc_engineer in every tenant; outside this slice's scope                      |
| C. Bind a tenant role to the OAuth client via a DB write in bootstrap.sh                                                                             | Explicit + auditable                                                                                                                                      | No surface yet for `client_id → role` bindings; would need its own slice                                    |
| D. **(chosen)** Extend the slice-035 `system.rego` machine-actor carve-out to cover `action=upload-bundle, resource=controls, is_machine_actor=true` | Symmetric with the existing `evidence:push` machine-actor exemption; narrow (three-way scoped: action + resource + machine flag); doesn't touch slice 188 | Adds a second machine-actor row in `system.rego` — needs the regression guards                              |

**Decision**: D. The pattern was already established by slice 035 for
the slice-014/034 connector push path. Extending it to bootstrap's
upload path keeps the carve-out shape consistent, the surface narrow
(three independent predicates the regression tests pin), and leaves
the slice 188 token handler unchanged.

**Companion change**: `internal/authz/input.go::BuildInput` extended
to recognise `oauth_client:` (slice 188's `MachineSubjectPrefix`) as a
machine-actor UserID prefix, in addition to the existing `key_` prefix
and empty UserID. Without this widening, the `system.rego` rule never
matches for OAuth clients (the slice-035 carve-out's
`is_machine_actor == true` predicate would be `false` for any OAuth
JWT). Three tests pin the matrix: `oauth_client:` → machine (new),
`key_` → machine (regression guard), plain UUID human → not machine
(regression guard).

**Confidence**: high. Pattern is mechanically symmetric with the
existing carve-out, the test surface is exhaustive, and the change
touches a single Rego allow rule.

## D2 — Where does the bootstrap credentials file live inside the container?

**Context**: `bootstrap.Dockerfile` runs as the `postgres` user (the
postgres:16-alpine base image's unprivileged uid). The default
`ATLAS_DATA_DIR=/var/lib/atlas` (used by the atlas service) is owned
by root in that image and not writable by postgres. P0-196-2 requires
the secret never be baked into image layers, so it has to land on a
runtime-mounted writable volume.

**Options weighed**:

| Option                                                                                                   | Pros                                                                                                                     | Cons                                                                   |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| Reuse `/var/lib/atlas` across services                                                                   | Single ATLAS_DATA_DIR                                                                                                    | Different services run as different uids; permission drift over time   |
| Tmpfs                                                                                                    | No persistence across restarts                                                                                           | Defeats the idempotency goal — every restart issues a new OAuth client |
| **(chosen)** Dedicated `/var/lib/atlas-bootstrap` on a service-local named volume `atlas-bootstrap-data` | Clean separation of credential lifecycles; only bootstrap mounts it; idempotent across `docker compose run --rm` re-runs | Adds one named volume to the compose file                              |

**Decision**: Dedicated `/var/lib/atlas-bootstrap` on a new
`atlas-bootstrap-data` named volume. The compose service block
explicitly sets `ATLAS_DATA_DIR=/var/lib/atlas-bootstrap` so the
default `${ATLAS_DATA_DIR:-/var/lib/atlas-bootstrap}` shell expansion
in bootstrap.sh stays robust even if the env var is unset in some
other deployment shape (Helm chart, future external configs).

The named volume is excluded from any cross-service sharing because
its only consumer is the one-shot bootstrap container. Operators
running `docker compose down -v` will wipe it; the next bring-up
issues a new OAuth client (the old DB row is detected by bootstrap.sh's
`ErrDuplicateName` fallback that suffixes a unix-second retry name).

**Confidence**: medium-high. The compose-volume permission model is
well-trodden but the postgres uid in postgres:16-alpine is not 1000 —
worth a smoke test on first run. The self-host bundle e2e will surface
any permission issue immediately.

## D3 — How is the OAuth client name made unique per deployment?

**Context**: P0-196-3 requires the client name not collide across
multi-instance docker-compose runs. The container's `$(hostname)` is
a docker-assigned hex prefix that changes on every `docker compose
run --rm`, which would defeat the idempotency goal (each re-run would
issue a new client + leave orphan DB rows).

**Options weighed**:

| Option                                                                      | Pros                                                                                                             | Cons                                                                                                                                                                    |
| --------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `$(hostname)` suffix                                                        | Always unique                                                                                                    | NOT sticky — re-runs get a new name → new client per restart                                                                                                            |
| Random UUID at first run + persist alongside credentials                    | Sticky                                                                                                           | Adds a non-credential file to persist                                                                                                                                   |
| **(chosen)** Derived from `ATLAS_BOOTSTRAP_TENANT` UUID — first 8 hex chars | Deterministic per deployment + naturally distinct between multi-tenant deployments + no extra persistence needed | Two deployments with the same `ATLAS_BOOTSTRAP_TENANT` (cloned `.env`) would collide — but they'd also collide on every other deterministic seed, so this is consistent |

**Decision**: Name shape is
`atlas-bootstrap-controls-${TENANT_SHORT}` where `TENANT_SHORT` is
the first 8 hex chars of the tenant UUID (stripped of dashes). Same
deployment → same name → re-runs reuse the persisted credentials file
before ever calling `oauth issue-client` (the file-exists check at the
top of phase 6a is the idempotency gate). Multi-instance compose runs
with different tenant UUIDs get distinct client names. P0-196-3
satisfied.

For the corner case where the credentials file is wiped but the DB row
remains (operator manually removed the volume), `oauth issue-client`
returns `ErrDuplicateName` and bootstrap.sh falls back to
`atlas-bootstrap-controls-${TENANT_SHORT}-retry-$(date -u +%s)`. The
orphan DB row remains; a future admin tool can prune it. This is
acceptable because the path is operational-debt, not a hot path.

**Confidence**: high. The fingerprint scheme is deterministic, the
fallback handles the realistic corner case, and the test surface
(`is_machine_actor` widening + `system.rego` carve-out) is independent
of the name shape.

## D4 — Should `test-self-host-bundle.sh` restructure for the OAuth flow?

**Context**: The harness writes `ATLAS_BOOTSTRAP_TOKEN` into
`.env.test`. The atlas service still consumes it (AC-4 transitional —
the slice-037 fixed-token credential mint at `cmd/atlas/main.go` stays
live so operators with legacy integrations don't break). The
atlas-bootstrap service no longer reads it.

**Options weighed**:

| Option                                                     | Pros                     | Cons                                                                                             |
| ---------------------------------------------------------- | ------------------------ | ------------------------------------------------------------------------------------------------ |
| Remove `ATLAS_BOOTSTRAP_TOKEN` from `.env.test`            | Cleaner                  | The atlas service `${ATLAS_BOOTSTRAP_TOKEN:?}` would refuse to start                             |
| Add OAuth-specific assertion (probe credentials file mode) | More end-to-end coverage | Out of scope; the load-bearing assertion (`controls=50`) already proves the OAuth path           |
| **(chosen)** Leave the harness unchanged                   | Minimum-blast-radius     | Doesn't add a positive OAuth assertion — but the implicit one (50 controls landed) is sufficient |

**Decision**: No structural change to `test-self-host-bundle.sh`. The
`.env.test` continues to set `ATLAS_BOOTSTRAP_TOKEN`; atlas reads it,
atlas-bootstrap ignores it. The harness's `controls=50` assertion
implicitly verifies the OAuth flow: if the bootstrap.sh OAuth path is
broken, no bundles get uploaded, the assertion fails, the slice is
unverifiable. The decision_audit_log assertion (≥1 row) likewise
verifies that the OAuth-issued JWT actually reached the controls
handler through OPA authz — proves the new `system.rego` carve-out
fired.

**Confidence**: high. The harness is the load-bearing gate per the
slice doc; adding bespoke OAuth assertions would just duplicate what
the existing assertions already prove transitively.

## D6 — Self-host bundle did not enable slices 187 + 188 — wire them as a load-bearing companion change

**Context**: The slice 196 e2e gate is the self-host bundle test. On
first run it surfaced four downstream gaps not anticipated by the
slice doc:

1. The `atlas` service was never configured with `ATLAS_ISSUER_URL`,
   so the slice 188 `/oauth/token` route returned 404 — the bootstrap
   container could not acquire a JWT.
2. The slice 187 OAuth keystore defaults to `/var/lib/security-atlas/keys/`
   which does not exist in the distroless atlas image and is not
   writable by uid 65532.
3. The bootstrap-Dockerfile-creates-`/var/lib/atlas-bootstrap`
   pre-mount step was necessary for the postgres uid to write the
   credentials file onto the named-volume mount.
4. The slice-035 `/v1/controls:upload-bundle` handler had a
   pre-OPA `IsAdmin` short-circuit check that 403'd the OAuth-issued
   credential even though OPA would have allowed it.

**Options weighed**:

| Option                                                                   | Pros                           | Cons                                                  |
| ------------------------------------------------------------------------ | ------------------------------ | ----------------------------------------------------- |
| Pre-build the missing OAuth wiring in a separate slice                   | Keeps 196 scope-tight          | Slice 196 can't actually ship — no e2e green          |
| **(chosen)** Wire OAuth in the bundle as a load-bearing companion change | Slice 196 actually ships green | Adds ~4 files of compose / Dockerfile / handler diffs |

**Decision**: Wire the missing OAuth-enablement bits inline. The
delta is small and entirely contained to (a) `deploy/docker/atlas.Dockerfile`
pre-creating `/var/lib/atlas/keys` with nonroot ownership,
(b) `deploy/docker/bootstrap.Dockerfile` pre-creating
`/var/lib/atlas-bootstrap` with postgres ownership,
(c) `deploy/docker/docker-compose.yml` setting
`ATLAS_ISSUER_URL=http://atlas:8080`, `ATLAS_KEYSTORE_PATH=/var/lib/atlas/keys`,
`ATLAS_DATA_DIR=/var/lib/atlas`, `ATLAS_OAUTH_TOKEN_RATE_PER_MIN=600`
(the bootstrap uploads 50 bundles → 50 token acquires per run; the
default 60/min was insufficient for the idempotency re-run within the
same minute window), and adding an `atlas-data` named volume,
(d) `internal/api/controls/handlers.go` extending the pre-OPA
`IsAdmin`-only gate to admit machine-actor credentials too (symmetric
with the slice-035 + slice-196 OPA carve-out below).

**Confidence**: high. All four wiring changes are mechanical, the
test surface (full self-host bundle e2e + the new handler-level
`TestUpload_AdmitsMachineActor` regression) is exhaustive, and the
delta is the minimum viable enablement (no JWT issuer rotation, no
keystore reseed, no per-tenant rate-limit overrides).

**Spillover candidate**: a future slice could harmonise the
handler-level `IsAdmin` check across the other admin-only endpoints
(schemas, etc.) so they share the slice-196 `isMachineActor` helper
or — better — defer entirely to OPA. Out of scope for 196.

## D5 — Should `atlas-cli oauth issue-client` change to support DB-creds-less invocation?

**Context**: The slice 191 implementation requires `DATABASE_URL` in
the env. The bootstrap container already has it
(`DATABASE_URL_MIGRATE`), so calling `oauth issue-client` from
bootstrap.sh works without any code change.

A natural question: should bootstrap.sh hit the future
`/oauth/clients` HTTP endpoint instead of going DB-direct? Then the
bootstrap container wouldn't need DB credentials.

**Decision**: No change. Bootstrap.sh already has BYPASSRLS
`atlas_migrate` access for the migrations phase. Reusing that for
`oauth issue-client` is symmetric with how it issues every other
boot-time identity. Filing a `/oauth/clients` HTTP endpoint would be a
separate slice — out of scope for 196.

**Confidence**: high. The DB-direct path is the slice 191 shape; the
bootstrap container is the only credentialed caller; no new slice
surface needed.

## Outcomes

- Self-host bundle bundled-mode e2e: **pending verification at PR-open
  time** (locally — see PR body for status). Both modes (bundled +
  external) must land green on the PR to merge.
- All Go unit + integration tests green:
  `internal/authz/...` includes the new `is_machine_actor` widening
  matrix + the slice-196 `system.rego` carve-out matrix.
  `cmd/atlas-cli/...` includes the new OAuth flag parsing + branch
  selection matrix.
- No new dependencies introduced.
- Threat model: the slice extends one OPA carve-out narrowly. The
  three regression tests (`TestSlice196_HumanActorUploadBundleStillRoleGated`,
  `TestSlice196_MachineActorUploadBundleDeniedOnOtherResource`,
  `TestBuildInput_IsMachineActor_HumanUserNotMachine`) collectively pin
  the carve-out's intended scope. P0-196-1 (no slice 034 middleware
  re-introduction) is satisfied by inspection — the slice 196 PR
  touches no file under `internal/api/httpserver.go`. P0-196-2 (no
  secret in image layers) is satisfied by the runtime-mounted volume
  design + bootstrap.sh's `umask 0177` then `chmod 0600` sequence on
  the credentials file write. P0-196-3 (unique client name per
  deployment) is satisfied by the tenant-UUID-derived fingerprint.

## Future spillover

- Slice 197 (already filed): full slice-034 bearer middleware
  retirement. With slice 196 merged, the bootstrap path no longer
  depends on the legacy middleware; only the in-tree integration
  test fixtures still need cutover to JWT bearers.
- **Shared OAuth token across bundle uploads** (new spillover
  candidate): bootstrap.sh currently runs `atlas-cli controls upload`
  once per bundle (50 invocations × 50 OAuth token acquires). The
  slice-188 token endpoint rate limit defaults to 60/min, so the
  idempotency re-run trips it without the `ATLAS_OAUTH_TOKEN_RATE_PER_MIN=600`
  override added in this slice. A cleaner shape would be either
  (a) one CLI invocation that uploads a whole directory of bundles
  (eliminates the 50× process-spawn overhead) or (b) bootstrap.sh
  acquires the JWT once with `curl /oauth/token` and passes it via
  `--token`. Out of scope for 196.
- The `ATLAS_BOOTSTRAP_TOKEN` env var on the `atlas` service can be
  retired in a follow-on (audit window) — the slice-037 fixed-token
  admin credential mint at `cmd/atlas/main.go` becomes unreachable by
  any in-tree caller, but `docs-site/docs/troubleshooting/first-login.md`
  still references it as one of three operator first-login paths.
  Decoupling the operator-sign-in convenience from the bootstrap
  upload identity is the residual cleanup.

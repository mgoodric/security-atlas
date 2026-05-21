# 196 — Bootstrap container OAuth migration (atlas-bootstrap → client_credentials)

**Cluster:** Infra / Bootstrap
**Estimate:** 1d
**Type:** AFK
**Status:** `not-ready` (gate: slice 191 merged)

## Provenance

Surfaced during slice 191 (PR #454). The slice 191 cutover removes
slice 034's bearer-token middleware from `internal/api/httpserver.go`.
The atlas-bootstrap one-shot container in
`deploy/docker/docker-compose.yml` uses a fixed `ATLAS_BOOTSTRAP_TOKEN`
to authenticate `atlas-cli controls upload` calls — that token was
issued via `IssueBootstrapFixedAdminCredential` into the slice 034
credstore. After the cutover, the bearer middleware is gone and the
token is no longer recognized; uploads return 403 forbidden.

The slice 191 self-host bundle e2e jobs caught this regression. The
jobs are NOT in `.github/branch-protection.json` required contexts,
so they did not block the slice 191 merge — but they need to land
green again before the next release.

## Narrative

Two paths forward:

1. **Migrate bootstrap to OAuth `client_credentials`.** The
   `atlas-bootstrap` container issues an OAuth client at startup
   via `atlas-cli oauth issue-client`, captures the resulting
   `client_id` + `client_secret`, then drives `atlas-cli controls
upload` with `--client-id` + `--client-secret` flags (a NEW
   pair this slice adds). The token-acquisition happens at the
   CLI layer via the slice 191 OAuth helper.

2. **Make `/v1/controls/upload` admit a fixed-token shortcut.** A
   single env-var-gated bypass keyed on an HMAC of
   `ATLAS_BOOTSTRAP_TOKEN`. Smaller code change but adds a special-
   case auth path that future maintenance has to remember.

The first path is the right long-term shape — it removes the last
slice 034 / credstore consumer from the production hot path. The
second is a v2.5 stopgap if a release needs to ship before the
bootstrap migration lands.

## Acceptance criteria

- **AC-1.** Bootstrap container's `bootstrap.sh` issues an OAuth
  client at startup (via `atlas-cli oauth issue-client
atlas-bootstrap-controls`) and stores the credentials in
  `${ATLAS_DATA_DIR}/oauth-bootstrap-credentials.json` (mode 0600).
- **AC-2.** `atlas-cli controls upload` gains `--client-id` +
  `--client-secret` flags that, when set, drive the slice 191 Go
  SDK's `oauth.NewClient` for bearer acquisition instead of
  reading `--token`.
- **AC-3.** `deploy/docker/docker-compose.yml` removes
  `ATLAS_BOOTSTRAP_TOKEN` from the atlas-bootstrap container's env
  block. (The atlas container's env keeps it during the
  transition — see AC-4.)
- **AC-4.** `cmd/atlas/main.go` keeps `IssueBootstrapFixedAdminCredential`
  intact for self-host operators with non-bootstrap legacy
  integrations. Removal is gated on a separate audit-window slice.
- **AC-5.** `deploy/docker/test-self-host-bundle.sh` updated to
  match the new bootstrap flow.
- **AC-6.** Both self-host bundle e2e jobs (bundled + external)
  pass green.
- **AC-7.** `docs-site/docs/install.md` updated to describe the new
  bootstrap OAuth client lifecycle (operators see two credentials:
  one is the human OIDC sign-in, the other is the bootstrap OAuth
  client).

## Anti-criteria (P0)

- **P0-196-1.** Does NOT re-introduce the slice 034 bearer
  middleware to `internal/api/httpserver.go`. Slice 191's
  retirement is permanent.
- **P0-196-2.** Does NOT persist `client_secret` in image layers
  (the secret is issued at container startup, written to a volume,
  never baked).
- **P0-196-3.** Bootstrap container's OAuth client MUST have a
  unique name per deployment so multi-instance docker-compose runs
  don't collide.

## Dependencies

- **#191** — slice 191 must merge (delivers `oauth-cli issue-client`,
  the Go SDK OAuth helper, the `client_credentials` grant path).

## Skill mix (2-3)

- `tdd`
- `simplify`
- (optional) `ship-gate` — self-host bundle smoke is the gate.

## Notes for the implementing agent

The Go SDK at `pkg/sdk-go/oauth/` already provides the cache-and-refresh
contract. The CLI change is plumbing.

Test discipline: a working self-host bundle e2e (bundled variant) is
the load-bearing verification — without it, the slice is unverifiable.

### Provenance

Filed 2026-05-21 during slice 191 (PR #454) — self-host bundle e2e
caught the regression but is not in branch protection's required
context list, so slice 191 merged with self-host informational fail.

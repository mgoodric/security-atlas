# CLI reference

The release binary is **`security-atlas-cli`** (built from `cmd/atlas-cli`;
the server binary is `security-atlas`). Build it from source with:

```sh
go build -o ./bin/ ./cmd/atlas-cli
```

`just build-go` runs `go build ./...` as a compile check and writes no
binaries — build the target explicitly, or use `go run ./cmd/atlas-cli`.

## Global flags

Every subcommand that talks to a running platform accepts these:

| Flag         | Env var                   | Notes                                     |
| ------------ | ------------------------- | ----------------------------------------- |
| `--endpoint` | `SECURITY_ATLAS_ENDPOINT` | **gRPC** endpoint, e.g. `localhost:50051` |
| `--token`    | `SECURITY_ATLAS_TOKEN`    | Bearer token                              |
| `--insecure` | —                         | Disable TLS. Loopback endpoints only.     |

Commands that write directly to Postgres (`catalog`, `policy seed-stock`,
`demo`, `evidence verify`) take a `--dsn` / `--database-url` instead and read
`DATABASE_URL` or `DATABASE_URL_APP` from the environment. The import
commands need the `atlas_migrate` role; the read/verify paths use `atlas_app`.

## Command tree

| Command                                  | What it does                                                     |
| ---------------------------------------- | ---------------------------------------------------------------- |
| `evidence push`                          | Push one evidence record to the ledger                           |
| `evidence verify`                        | Walk the ledger and re-check each record's stored hash           |
| `credentials issue`                      | Issue an API key (bearer returned once)                          |
| `credentials rotate` / `revoke` / `list` | API-key lifecycle                                                |
| `catalog import-scf <path>`              | Import the SCF JSON catalog into Postgres                        |
| `catalog import-crosswalk <path>`        | Import a framework→SCF crosswalk YAML (alias: `import-soc2`)     |
| `controls validate <path>`               | Validate a control bundle locally (no network call)              |
| `controls test <bundle-dir>`             | Run a bundle's test cases against fixture evidence               |
| `controls upload <path>`                 | Upload a control bundle to the platform                          |
| `policy seed-stock`                      | Seed the 5 stock policies as draft rows for a tenant             |
| `oscal-export`                           | Export the OSCAL audit-handoff bundle for a **frozen** period    |
| `oscal sign` / `verify` / `config-check` | Sign, verify, and inspect OSCAL bundle signing config            |
| `features list` / `set <key> <on\|off>`  | Per-tenant feature-flag toggles (admin only)                     |
| `bootstrap hash-password`                | Read a password from stdin, print its argon2id hash              |
| `oauth issue-client <name>`              | Issue an OAuth `client_credentials` identity                     |
| `oauth add-redirect-uri <id> <uri>`      | Register a redirect URI for an OAuth client                      |
| `oauth migrate-api-key <api_key>`        | Issue an OAuth client mirroring a legacy API key                 |
| `keys list` / `rotate` / `prune`         | JWT signing-key lifecycle                                        |
| `login`                                  | Authenticate via OAuth device code (RFC 8628)                    |
| `demo seed` / `teardown`                 | Demo-dataset management (requires `ATLAS_ENABLE_DEMO_SEED=true`) |
| `version`                                | Print version, commit, and build metadata                        |

Run `security-atlas-cli <command> --help` for the authoritative flag set —
the tables below cover the flags operators reach for most often.

## evidence

`evidence push` writes one record over gRPC. Required: `--kind`,
`--control`, `--scope`, `--observed-at`, `--result`, `--payload`,
`--idempotency-key`, `--actor-id`.

```sh
security-atlas-cli evidence push \
  --kind sast.scan_result.v1 \
  --control <control id> \
  --scope '{"environment":"prod"}' \
  --observed-at "$(date -u +%FT%TZ)" \
  --result pass \
  --payload @./scan-result.json \
  --idempotency-key "ci-$GITHUB_RUN_ID" \
  --actor-id "github-actions"
```

`--scope` is the scope predicate as JSON. `--payload` takes a JSON literal or
`@path/to/file.json`. `--result` is one of `pass | fail | na | inconclusive`.
Optional: `--schema-version` (default `1.0.0`), `--actor-type` (default
`service_account`), `--session-id`, `--payload-uri`.

`evidence verify` re-hashes the ledger and reports mismatches:

```sh
security-atlas-cli evidence verify --tenant <tenant uuid>   # RLS-scoped, as atlas_app
security-atlas-cli evidence verify --all-tenants            # super-admin walk
```

Add `--page-size` to tune the keyset page size (default 1000).

## credentials

```sh
security-atlas-cli credentials issue --tenant <tenant id> \
  --scope '{"environment":"prod"}' --kinds sast.scan_result.v1 --ttl 30d
security-atlas-cli credentials list   --tenant <tenant id>
security-atlas-cli credentials rotate --id <credential id>
security-atlas-cli credentials revoke --id <credential id>
```

The bearer is printed exactly once, at issue time. `--kinds` empty means all
evidence kinds; `--ttl 0` (the default) means no expiry.

`credentials issue --reset-bootstrap --force` is the first-login recovery
path — see [First-time login](troubleshooting/first-login.md).

## catalog

Both importers write directly to Postgres and need `DATABASE_URL` pointed at
the `atlas_migrate` role. Both are idempotent.

```sh
security-atlas-cli catalog import-scf ./scf-catalog.json
security-atlas-cli catalog import-crosswalk data/crosswalks/soc2-tsc-2017.yaml
```

`just import-scf <path>` and `just import-soc2 <path>` wrap these.

To import a full OSCAL catalog (NIST 800-53 and friends), use the separate
`atlas-oscal import-catalog` binary — see
[OSCAL catalog import](oscal-catalog-import.md).

## controls

Control-as-code bundle authoring:

```sh
security-atlas-cli controls validate ./bundles/my-control   # local, no network
security-atlas-cli controls test ./bundles/my-control       # add --json for a machine report
security-atlas-cli controls upload ./bundles/my-control
```

`controls upload` authenticates with `--token`, or with OAuth
`client_credentials` when you pass `--client-id` + `--client-secret`
(and optionally `--issuer`).

## features

```sh
security-atlas-cli features list
security-atlas-cli features set risk.enabled on --reason "enabling risk module"
```

These talk to the platform over HTTP, not gRPC — set `--http-endpoint` or
`ATLAS_HTTP_ENDPOINT` (default `http://localhost:8080`). `--reason` is
recorded in the feature-flag audit log.

## keys

JWT signing-key lifecycle against the keystore directory (`--keystore`, env
`ATLAS_KEYSTORE_PATH`):

```sh
security-atlas-cli keys list
security-atlas-cli keys rotate            # new signing key; prior key kept for overlap
security-atlas-cli keys prune             # dry-run by default
security-atlas-cli keys prune --confirm --overlap 48h
```

`prune` will not remove anything without `--confirm`.

## oscal

```sh
security-atlas-cli oscal config-check          # report the resolved signing mode
security-atlas-cli oscal sign ./bundle-dir
security-atlas-cli oscal verify ./bundle-dir
```

`oscal-export` is a separate top-level command; it refuses to run against a
period that has not been frozen and needs the `oscal-bridge` sidecar:

```sh
security-atlas-cli oscal-export \
  --tenant-id <tenant uuid> \
  --period-id <frozen period uuid> \
  --out ./oscal-bundle/ \
  --bridge-addr 127.0.0.1:50070
```

Optional: `--org-name`, `--system-name`, `--system-description`,
`--requested-by`. `--dsn` defaults to `DATABASE_URL_APP`.

## demo

Gated behind `ATLAS_ENABLE_DEMO_SEED=true` — the exact lowercase string, and
nothing else, enables it. Teardown is destructive; see
[Configuration reference](configuration.md).

```sh
security-atlas-cli demo seed --tenant-slug demo-acme --scale 1.0
security-atlas-cli demo teardown --tenant-slug demo-acme
```

`--scale` accepts 0.1 to 5.0. Both read the BYPASSRLS DSN from
`--database-url` / `DATABASE_URL`; `seed` also uses `--app-database-url` /
`DATABASE_URL_APP` for the post-seed evaluation pass.

## login

Device-code sign-in (RFC 8628) for a human at a terminal:

```sh
security-atlas-cli login --issuer https://atlas.example.com --client-id cli-public
```

Reads `ATLAS_ISSUER` and `ATLAS_OAUTH_CLIENT_ID` when the flags are omitted.
`--timeout` bounds the wait for approval (default 15m). The operator
registers the public client first with `oauth issue-client` — see
[Migrating from API keys to OAuth](migration/oauth.md).

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

| Flag                    | Env var                   | Notes                                                      |
| ----------------------- | ------------------------- | ---------------------------------------------------------- |
| `--endpoint`            | `SECURITY_ATLAS_ENDPOINT` | **gRPC** endpoint, e.g. `localhost:50051`                  |
| `--token`               | `SECURITY_ATLAS_TOKEN`    | Bearer token                                               |
| `--insecure`            | —                         | Disable TLS. Loopback endpoints only.                      |
| `--allow-insecure-http` | —                         | Let REST commands use plain `http://` to any host. Unsafe. |

`--insecure` is refused unless the endpoint's host is `localhost`, an address
in `127.0.0.0/8` (including `::ffff:127.0.0.1`), or `::1`. The host is parsed,
not prefix-matched, so `localhost.example.com` is refused too. The check is
literal and never consults DNS: a name that resolves to loopback, such as
`127.0.0.1.nip.io`, is refused, and so is the fully qualified `localhost.`.
Every connector applies the same rule to its own `--insecure`, on the
registry dial as well as the push path.

### Plain `http://` for REST commands

The REST commands (`controls upload`, `controls import` and its subcommands
including `export`, `features`, `taxonomy`, `mappings`, `credentials
reset-bootstrap`, `policy import`, `login`, and the OAuth token request behind
`controls upload --client-id`) refuse to send to an `http://` URL whose host
is not loopback, by the same rule as `--insecure`. The refusal happens before
anything is sent, so the bearer, the client secret or the device-flow token
never crosses the wire in the clear. It applies to every redirect hop as
well, so an `https://` endpoint that redirects to a non-loopback `http://`
URL is refused at the redirect.

`https://` is unaffected, and so is `http://localhost:8080` for local
development. To send to a non-loopback `http://` URL anyway, for example
inside a private network you trust, pass `--allow-insecure-http`; the CLI
prints a warning on stderr every time it is set.

Commands that write directly to Postgres (`catalog`, `policy seed-stock`,
`demo`, `evidence verify`) take a `--dsn` / `--database-url` instead and read
`DATABASE_URL` or `DATABASE_URL_APP` from the environment. The import
commands need the `atlas_migrate` role; the read/verify paths use `atlas_app`.

## Tenant flags

On a single-tenant install (the default) no command needs a tenant ID. The
tenant flags (`--tenant` on `credentials`, `evidence verify` and the `oauth`
client commands; `--tenant-id` on `policy seed-stock` and `oscal-export`) are
optional there:

- Commands that call the API default to the tenant of the `--token`
  credential.
- Commands that connect straight to Postgres use the install's only tenant
  and print its name on stderr.

On a multi-tenant install, leaving the flag off is an error that lists every
tenant's ID, slug and name, so you never act on the wrong one by accident.
See [Tenant membership](tenant-membership.md) for opting in to multi-tenant.

## Command tree

| Command                                  | What it does                                                     |
| ---------------------------------------- | ---------------------------------------------------------------- |
| `evidence push`                          | Push one evidence record to the ledger                           |
| `evidence verify`                        | Walk the ledger and re-check each record's stored hash           |
| `credentials issue`                      | Issue an API key (bearer returned once)                          |
| `credentials rotate` / `revoke` / `list` | API-key lifecycle                                                |
| `catalog import-scf <path>`              | Import the SCF catalog (JSON or SCF `.xlsx`) into Postgres       |
| `catalog import-crosswalk <path>`        | Import a framework→SCF crosswalk YAML (alias: `import-soc2`)     |
| `catalog converge-crosswalks <dir>`      | Re-import each crosswalk YAML in `<dir>` already imported        |
| `controls validate <path>`               | Validate a control bundle locally (no network call)              |
| `controls test <bundle-dir>`             | Run a bundle's test cases against fixture evidence               |
| `controls upload <path>`                 | Upload a control bundle to the platform                          |
| `policy seed-stock`                      | Seed the 5 stock policies as draft rows for a tenant             |
| `oscal-export`                           | Export the OSCAL audit-handoff bundle for a **frozen** period    |
| `oscal sign` / `verify` / `config-check` | Sign, verify, and inspect OSCAL bundle signing config            |
| `features list` / `set <key> <on\|off>`  | Per-tenant feature-flag toggles (admin only)                     |
| `bootstrap hash-password`                | Read a password from stdin, print its argon2id hash              |
| `oauth issue-client <name>`              | Issue an OAuth `client_credentials` identity bound to a tenant   |
| `oauth bind-client <client_id>`          | Bind an existing OAuth client to a tenant                        |
| `oauth unbound-rows [--purge]`           | List (or purge) rows left in the all-zeros tenant                |
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
`--control` (except for a kind routed by evidence type, below), `--scope`,
`--observed-at`, `--result`, `--payload`, `--idempotency-key`, `--actor-id`.

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
`--control` takes the control's UUID or its bundle ID; a bundle ID links the
record to the tenant's active version of that bundle. Any other value is kept
as a free-form reference and does not link to a control.
The receipt's `linkedControlId` is the UUID of the control the record linked
to, and is absent when it linked to none. When `--control` is not a UUID and
the record did not link, the CLI also prints a warning on stderr, which is
usually a mistyped bundle ID. A bundle ID cannot be UUID-shaped: the push
reads a UUID as a control ID, so `controls upload` rejects such a bundle.

Connector kinds such as `github.repo_protection.v2` or
`osquery.host_posture.v1` are routed by evidence type: the record links to no
one control and reaches every control whose evidence queries declare its kind.
The full list is in `internal/evidence/routed`. For these kinds `--control` is
optional; a value you pass is kept as provenance only, `linkedControlId` is
always absent, and the CLI prints a note on stderr instead of the warning.
Optional: `--schema-version` (default `1.0.0`), `--actor-type` (default
`service_account`), `--session-id`, `--payload-uri`.

The gRPC endpoint accepts API keys only, not a UI session token; issue one
with `credentials issue`. [Push a first manual attestation](first-manual-attestation.md)
walks the whole path.

### Pushing a SAST scan with findings

No bundled connector emits `sast.scan_result.v1`; a CI job pushes each scan
itself. Three stock controls read the findings, from schema version 1.1.0
on (#2831):

| Control                              | Fails when the repository's latest scan…                                                                                           |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| `soc2_cc7_1_vulnerability_detection` | is 168 hours old or older, or has an unsuppressed `critical` finding                                                               |
| `soc2_pi1_2_input_completeness`      | has an unsuppressed finding under `injection.sql`, `injection.command`, `deserialization.unsafe` or `input.schema_violation`       |
| `soc2_pi1_3_processing_validity`     | has an unsuppressed finding under `authz.missing_check`, `authz.idor`, `authz.privilege_escalation` or `authz.broken_object_level` |

The payload names the `repository` and lists every finding, suppressed ones
included, as `rule_id`, `severity` (`critical`, `high`, `medium`, `low` or
`info`) and `suppressed`:

```json
{
  "tool": "semgrep",
  "tool_version": "1.90.0",
  "findings_count": 2,
  "repository": "acme/api",
  "report_uri": "https://ci.example.com/runs/1234/sarif",
  "findings": [
    { "rule_id": "injection.sql", "severity": "high", "suppressed": false },
    {
      "rule_id": "python.flask.debug-enabled",
      "severity": "low",
      "suppressed": true
    }
  ]
}
```

- **Rule ids.** pi1_2 and pi1_3 match the platform rule ids above exactly.
  Your scanner has its own, so the CI job maps the rules you treat as SQL
  injection, IDOR and so on onto them before pushing. Any other rule id is
  kept as sent and is read only by cc7_1, for its severity.
- **Severity.** Map the scanner's levels onto the five values. A SARIF
  `error` is not necessarily `critical`; decide which of your levels is.
- **Every finding.** Send the whole list for each scan: the latest scan of a
  repository replaces the earlier one. An empty `findings` array is a clean
  scan. A scan without `findings` (a 1.0.0 record) leaves all three
  controls inconclusive.
- **One push per control.** `sast.scan_result.v1` is bound to the control
  named in `--control`, so a scan reaches only that control. Push the same
  payload once for each control that should judge it, each with its own
  idempotency key.
- **Size.** An inline payload is capped at 1 MiB, roughly ten thousand
  findings. Put the full report at `report_uri`.

```sh
for control in soc2_cc7_1_vulnerability_detection \
               soc2_pi1_2_input_completeness \
               soc2_pi1_3_processing_validity; do
  security-atlas-cli evidence push \
    --kind sast.scan_result.v1 \
    --schema-version 1.1.0 \
    --control "$control" \
    --scope '{"environment":"prod"}' \
    --observed-at "$(date -u +%FT%TZ)" \
    --result inconclusive \
    --payload @./scan-result.json \
    --idempotency-key "sast-$GITHUB_RUN_ID-$control" \
    --actor-id "github-actions"
done
```

`--result` is still required, but these three controls ignore it and decide
on the findings.

`evidence verify` re-hashes the ledger and reports mismatches:

```sh
security-atlas-cli evidence verify                 # RLS-scoped, as atlas_app
security-atlas-cli evidence verify --all-tenants   # super-admin walk
```

Add `--page-size` to tune the keyset page size (default 1000).

## credentials

```sh
security-atlas-cli credentials issue \
  --scope '{"environment":"prod"}' --kinds sast.scan_result.v1 --ttl 720h
security-atlas-cli credentials list
security-atlas-cli credentials rotate --id <credential id>
security-atlas-cli credentials revoke --id <credential id>
```

The bearer is printed exactly once, at issue time. `--kinds` empty means all
evidence kinds; `--ttl` takes a Go duration (`24h`, `90m`) or whole days
(`30d`), and `--ttl 0` (the default) means no expiry. So does any zero
duration, such as `0d` or `0h`.

These commands need an admin `--token`, and they act only in that
credential's tenant: a `--tenant` naming another tenant is refused, and
`rotate` or `revoke` of another tenant's key reports it as unknown.

`credentials issue --reset-bootstrap --force` is the first-login recovery
path — see [First-time login](troubleshooting/first-login.md). It mints a tenant admin key that expires (24h by default, `--ttl` up to `168h`) and refuses `--scope` and `--kinds`; revoke the key once you have signed in another way. An admin API key `--token` (such as `ATLAS_BOOTSTRAP_TOKEN`, single-tenant installs only) resets over gRPC at `--endpoint`; a super_admin JWT resets over HTTP at `--http-endpoint`.

## catalog

Both importers write directly to Postgres and need `DATABASE_URL` pointed at
the `atlas_migrate` role. Both are idempotent.

```sh
security-atlas-cli catalog import-scf ./scf-catalog.json
security-atlas-cli catalog import-crosswalk data/crosswalks/soc2-tsc-2017.yaml
```

`just import-scf <path>` and `just import-soc2 <path>` wrap these.

The JSON catalog's shape is documented in
[`docs/SELF_HOSTING.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/SELF_HOSTING.md),
with `internal/api/scfimport/catalog.go` as the contract. Each control may
carry an optional `legacy_scf_id`, its code in the previous SCF release;
the anchor API returns it as `legacy_scf_id`.

After importing a new release, `import-scf` re-binds controls that are
still on the previous release's anchors. A control moves only when the new
anchor has the same code and the same title. Every other control is printed
as `control needs re-anchoring`, with a note saying where its concept went
(see the [upgrade guide](upgrade.md#4-verify-the-upgrade)).

`import-scf` also reads SCF's published Excel workbook directly, with no
conversion step. Download it from the
[SCF website](https://securecontrolsframework.com/) or the
[SCF GitHub repository](https://github.com/securecontrolsframework/securecontrolsframework),
which carries files such as `secure-controls-framework-scf-2026-3.xlsx`:

```sh
security-atlas-cli catalog import-scf ./secure-controls-framework-scf-2026-3.xlsx
```

It reads the `SCF <release>` sheet (`SCF #`, `SCF Control`, and the
`Secure Controls Framework (SCF) Control Description` column), the same sheet
`import-crosswalk` reads from that workbook. The release version comes from the
sheet name (`2026.3`); `--release-version` overrides it. The workbook carries
no release date; pass `--release-date YYYY-MM-DD` to record one. Family is the `SCF #`
prefix (`IAC` for `IAC-37`), as in the JSON catalog.

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

## oauth

`oauth issue-client` and `oauth bind-client` connect straight to Postgres
through `DATABASE_URL`. Leave out `--tenant` and they read every tenant, so
use the BYPASSRLS `atlas_migrate` connection.

```sh
security-atlas-cli oauth issue-client ci-pipeline          # bound to your organisation
security-atlas-cli oauth issue-client cli-public --unbound # public device-flow client
security-atlas-cli oauth bind-client <client_id>           # bind an existing client
```

An OAuth `client_credentials` client is **bound to a tenant**: its tokens act
in that tenant and its writes land there. `issue-client` binds the new client
to the install's only tenant unless you pass `--tenant`. A token from an
unbound client can still read, but every write is refused with
`403 tenant_unbound`, which names `bind-client` as the fix. Use `--unbound`
only for a public client used by the device-code or authorization-code flow,
where the token carries the signed-in user's tenant.

Upgrading binds every existing unbound client on a single-tenant install. On
a multi-tenant install nothing is bound for you; bind each client with
`security-atlas-cli oauth bind-client <client_id> --tenant <tenant id>`. A
client already bound to another tenant is refused: issue a new one. See
[Migrating from API keys to OAuth](migration/oauth.md) for auditing public clients after
the upgrade.

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
security-atlas-cli demo seed                 # your organisation, single-tenant install
security-atlas-cli demo teardown             # removes exactly what that seed added
security-atlas-cli demo seed --tenant-slug demo-acme --scale 1.0   # separate tenant
security-atlas-cli demo teardown --tenant-slug demo-acme
```

Without `--tenant-slug`, `seed` loads into the install's only tenant, and only
while it holds nothing beyond the stock bootstrap seed; otherwise it refuses.
With `--tenant-slug` it creates a separate demo tenant, which needs
`ATLAS_ALLOW_MULTI_TENANT=true` when a tenant already exists. See
`docs/getting-started/demo-seed.md`.

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

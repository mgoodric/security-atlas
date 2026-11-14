# Push a first manual attestation

This page takes a fresh install from "signed in" to "one manual attestation
in the evidence ledger" with the CLI. It covers three steps:

1. Issue an API key scoped to `manual.attestation.v1`.
2. Find the ID of the control you are attesting to.
3. Push a `manual.attestation.v1` record at schema version `1.1.0`.

The UI's **Attest** button on a manual control does the same job without a
key. Use the CLI path when the attestation comes from a script, a scheduled
job, or another system.

## Why you need an API key

`evidence push` talks to the platform over **gRPC**, and the gRPC endpoint
accepts **API keys only**. The session you get from signing in to the UI is
not an API key, so pushing with it fails:

```text
error: sdk: push: rpc error: code = Unauthenticated desc = invalid or revoked bearer token
```

You issue the API key with `credentials issue`, and that call needs an
**admin** bearer. On a new install the admin bearer is the bootstrap token.

## Before you start

- A running install. See [Install](install.md).
- The CLI. Build it from the repo root:

  ```sh
  go build -o ./bin/security-atlas-cli ./cmd/atlas-cli
  ```

- The bootstrap token. It is either the `ATLAS_BOOTSTRAP_TOKEN` value from
  your `.env`, or the one the server generated at first start. The server
  prints the generated one in its log, and writes it to the bootstrap-token
  file until the first sign-in deletes that file:

  ```sh
  docker compose -f deploy/docker/docker-compose.yml logs atlas 2>&1 | grep 'bootstrap credential issued'
  docker compose -f deploy/docker/docker-compose.yml exec atlas cat /var/lib/atlas/bootstrap-token
  ```

  The first sign-in deletes the file but does not revoke the token. If
  you no longer have it, [First-time login](troubleshooting/first-login.md)
  covers minting a recovery admin key.

The examples below assume the docker-compose bundle on the same host:
gRPC on `localhost:50051` and HTTP on `localhost:8080`. `--insecure` turns
off TLS and is accepted for loopback endpoints only. Drop it, and use your
real hostname, when the platform is behind TLS.

```sh
export SECURITY_ATLAS_ENDPOINT=localhost:50051
export ADMIN_TOKEN=<the bootstrap token>
```

## Step 1: issue a scoped API key

Issue a key that can push only `manual.attestation.v1` and that expires in
30 days:

```sh
./bin/security-atlas-cli credentials issue --insecure \
  --token "$ADMIN_TOKEN" \
  --kinds manual.attestation.v1 \
  --ttl 720h
```

`--tenant` is optional on a single-tenant install; the key goes to the
tenant of the `--token` credential. On a multi-tenant install, pass
`--tenant <tenant uuid>`.

```json
{
  "handle": {
    "id": "key_1a7f4e35-623c-4cda-a757-dec4558316e8",
    "tenantId": "00000000-0000-4000-8000-000000000001",
    "kinds": ["manual.attestation.v1"],
    "ttl": "2592000s",
    "issuedAt": "2026-11-14T17:12:26.951328Z",
    "last4": "508e"
  },
  "bearerToken": "<64 hex characters>"
}
```

`handle.id` is what `credentials revoke` takes. The `bearerToken` is shown
**once** and cannot be retrieved again. Store it in your secret manager,
then:

```sh
export SECURITY_ATLAS_TOKEN=<bearerToken from the response>
```

Notes on the flags:

- `--kinds` takes a comma-separated list. Leave it empty to allow every
  evidence kind; keep it narrow for a key that does one job.
- `--ttl` takes a Go duration, so the largest unit is hours: `720h` is 30
  days. `30d` is rejected. `--ttl 0` (the default) means the key never
  expires.
- `--scope '<json>'` further limits which scope the key may push to.

## Step 2: find the control ID

Push links a record to a control by the control's **UUID**. A bundle ID
such as `soc2_pi1_5_storage_integrity` is not enough on its own: the
record is stored, but it is not linked to the control and does not count
toward the control's state.

The quickest way to get it is the UI: open the control, and the UUID is
the last segment of the page URL (`/controls/<id>`).

To look it up from a script, use `GET /v1/controls`. The REST API takes an
OAuth access token, **not** an API key: the key from Step 1, and the
bootstrap token, both get a 401 there. Issue an OAuth client once (this
writes to Postgres, so it needs `DATABASE_URL` for the `atlas_migrate`
role), then exchange it for a token:

```sh
./bin/security-atlas-cli oauth issue-client control-lookup
# client_id: e3d5e434-…
# client_secret: …        (printed once)
# tenant_id: 00000000-0000-4000-8000-000000000001

ACCESS_TOKEN=$(curl -fsS http://localhost:8080/oauth/token \
  --data-urlencode grant_type=client_credentials \
  --data-urlencode "client_id=<client_id>" \
  --data-urlencode "client_secret=<client_secret>" | jq -r .access_token)

curl -fsS -H "Authorization: Bearer $ACCESS_TOKEN" http://localhost:8080/v1/controls \
  | jq '.controls[] | select(.bundle_id == "soc2_pi1_5_storage_integrity") | {id, bundle_id, title}'
```

```json
{
  "id": "46e80fc7-41bc-4351-bd23-eed3cd83b3ac",
  "bundle_id": "soc2_pi1_5_storage_integrity",
  "title": "Storage retention and integrity controls documented per data class"
}
```

The UUID differs on every install. See [OAuth grants](oauth-grants.md) for
the other ways to get an access token.

```sh
export CONTROL_ID=<the id>
```

The seeded SOC 2 bundles with `implementation_type: manual_attested` are the
natural first targets. To list them:

```sh
grep -l 'implementation_type: manual_attested' controls/soc2/*/control.yaml
```

## Step 3: push the attestation

`manual.attestation.v1` at schema version `1.1.0` has two required fields
and two optional ones:

| Field              | Required | What it holds                                                            |
| ------------------ | -------- | ------------------------------------------------------------------------ |
| `attestor`         | yes      | Who is making the attestation                                            |
| `statement`        | yes      | What they attest is true                                                 |
| `supporting_uri`   | no       | A link to the supporting document                                        |
| `attestation_data` | no       | Control-specific fields, shaped by the bundle's `manual_evidence_schema` |

No other top-level fields are allowed. Write the payload to a file:

```sh
cat > attestation.json <<'EOF'
{
  "attestor": "jane.doe@example.com",
  "statement": "Retention durations are documented for every data class and integrity mechanisms are in place for restricted data.",
  "supporting_uri": "https://wiki.example.com/security/data-retention",
  "attestation_data": {
    "attested_at": "2026-11-14T15:00:00Z",
    "attestor": "jane.doe@example.com",
    "retention_per_class": { "restricted": "7 years" },
    "integrity_mechanisms": [
      { "system": "evidence-ledger", "mechanism": "sha256_content_hash" }
    ]
  }
}
EOF
```

Then push it. `--schema-version` defaults to `1.0.0`, so set `1.1.0`
explicitly; `attestation_data` is rejected at `1.0.0`.

```sh
./bin/security-atlas-cli evidence push --insecure \
  --kind manual.attestation.v1 \
  --schema-version 1.1.0 \
  --control "$CONTROL_ID" \
  --scope '{"environment":"prod"}' \
  --observed-at "$(date -u +%FT%TZ)" \
  --result pass \
  --payload @attestation.json \
  --idempotency-key "attest-$CONTROL_ID-$(date -u +%F)" \
  --actor-type human \
  --actor-id jane.doe@example.com
```

The flags that need a decision:

- `--scope` is the scope cell the attestation covers. The bootstrap seeds
  `{"environment":"prod"}`.
- `--result` is `pass`, `fail`, `na` or `inconclusive`.
- `--idempotency-key` makes a retry safe: the same key with the same
  record returns the original receipt instead of storing a second copy.
  Reusing a key with different content is rejected, so use a new key for
  each new attestation.
- `--actor-type` is `human` for a person, `service_account` (the default)
  for a script acting on its own.

The CLI prints the receipt:

```json
{
  "recordId": "d505aed3-6eb3-4481-ad52-1b975df7e2ec",
  "hash": "5eff697623684823257ae045299e65eb1eacb03fc0c3831fc1224dbbb0ddc4ce",
  "ingestedAt": "2026-11-14T17:12:26.985795Z",
  "credentialId": "key_1a7f4e35-623c-4cda-a757-dec4558316e8"
}
```

`hash` is the record's sha256 content hash. The record is now in the
ledger, linked to the control, and listed on the control's **Evidence**
tab.

What the common mistakes look like:

| Mistake                                             | Result                                                                                 |
| --------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `attestation_data` without `--schema-version 1.1.0` | Rejected: `additional properties 'attestation_data' not allowed`                       |
| Payload without `attestor` or `statement`           | Rejected: `missing properties 'attestor', 'statement'`                                 |
| Same `--idempotency-key`, different payload         | Rejected: `AlreadyExists desc = ingest: idempotency_key reused with different content` |
| Bundle ID instead of the UUID in `--control`        | Accepted, but the record is not linked to the control                                  |

## Validation on push

Push validates the payload against the `manual.attestation.v1` `1.1.0`
schema: `attestor` and `statement` present and non-empty, and no unknown
top-level fields. It does **not** check `attestation_data` against the
control's `manual_evidence_schema`. The UI's **Attest** form does that
check. If the per-control shape matters, build `attestation_data` from the
bundle's `manual_evidence_schema` (in its `control.yaml`) yourself.

## Afterwards

- Rotate or revoke the bootstrap token once you have real admin
  credentials. Revoke the attestation key when its job ends:

  ```sh
  ./bin/security-atlas-cli credentials list   --insecure --token "$ADMIN_TOKEN"
  ./bin/security-atlas-cli credentials revoke --insecure --token "$ADMIN_TOKEN" --id <credential id>
  ```

- [CLI reference](cli.md) has every `evidence` and `credentials` flag.
- [Evidence](primitives/evidence.md) explains the ledger the record lands in.

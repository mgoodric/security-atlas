# 1Password connector

Emits one evidence kind:

| Kind                      | Profile | Source                                 |
| ------------------------- | ------- | -------------------------------------- |
| `1password.org_policy.v1` | pull    | `GET /v1/account` (1Password Business) |

Slice 046 is **pull-only by design**. Canvas §4.2 marks 1Password as `Query`-only — 1Password Business does not expose a webhook surface for org-policy state, and adding one would violate the canvas. The connector emits one record per run capturing the org's password-policy posture: `org_id`, `two_factor_required`, `minimum_password_length`, `domain_restrictions_enabled`, `active_members`.

## Auth — least-privilege Service Account scopes

Slice 046 authenticates exclusively via 1Password **Service Account** tokens. A Service Account is a non-human identity with **per-vault** grants — never an admin or org-wide identity. The connector requires only the read grants below; the `atlas-1password scopes` subcommand prints this list at runtime, and the `DocumentedScopes` unit test rejects any write/manage/admin keyword in the registered set.

| Token kind      | Permission         | Access | Gates                                                                               |
| --------------- | ------------------ | ------ | ----------------------------------------------------------------------------------- |
| Service Account | `vault:read_items` | Read   | `1password.org_policy.v1` (org id, 2FA-required, min password length, domain rules) |
| Service Account | `account:read`     | Read   | `1password.org_policy.v1` (account metadata — `org_id`, `active_members` count)     |

**Banned grants:** `write_items`, `manage_vault`, any admin-class Service Account. The DocumentedScopes registry rejects these at the test level.

The Service Account token is read **only** from `$ONEPASSWORD_SERVICE_ACCOUNT_TOKEN` or, as a fallback, the `--token` flag. The env path is preferred so the secret never appears in shell history. The `opauth.Credential` type holds the bearer privately; `String()` and `%v` formatting both redact the value and reveal only its byte length.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-1password register \
  --endpoint platform.example.com:443 \
  --platform-token "$SECURITY_ATLAS_TOKEN"

# Pull org-policy state and push the evidence record.
ONEPASSWORD_SERVICE_ACCOUNT_TOKEN="$YOUR_SERVICE_ACCOUNT_TOKEN" \
atlas-1password run \
  --endpoint platform.example.com:443 \
  --platform-token "$SECURITY_ATLAS_TOKEN" \
  --environment prod

# Print the documented least-privilege Service Account scopes.
atlas-1password scopes
```

The `--platform-token` flag carries the **security-atlas** bearer; the 1Password Service Account is a separate secret that arrives via env or `--token`. They never share a flag.

## Idempotency

Each run derives `idempotency_key = sha256("1password.org_policy" | org_id | hour_truncated_observed_at)`. Two runs within the same hour for the same org collapse to one ledger row; runs that cross an hour boundary write a new row. This matches the slice 044 convention.

## Anti-criteria (P0)

- Admin / org-wide Service Account → REJECTED. Documented scopes are per-vault read only.
- Service Account token in logs → REJECTED. `Credential.String()` redacts; `--token` text never logged.
- Service Account token in CLI help → REJECTED. The flag help describes the variable and prefers the env path.
- Push without `idempotency_key` → REJECTED. The builder derives it from `kind|org_id|hour`.
- Mutate 1Password data → REJECTED. The connector has no write code path; the only HTTP method invoked is `GET`.
- Webhook subcommand → REJECTED by design. Canvas §4.2 is `Query` only; a stub would invite divergence.

## Tests

```sh
go test ./connectors/1password/...
```

Tests use `httptest.NewServer` to replay realistic 1Password Business `/v1/account` responses. The unit tests pin:

- `idem.OrgPolicyKey` — stable within hour, rotates across hour boundary, distinct per org, 64-hex-char output.
- `opauth.Resolve` — env + flag paths, fails on missing token, `Credential.String` redacts under `%s` and `%v`.
- `opauth.DocumentedScopes` — every documented grant is `Read`; no `write`/`manage`/`admin`/`delete` keyword; covers `1password.org_policy.v1`.
- `opaccount.Inspect` — pass on strong policy, fail on `two_factor_required=false`, fail on length < 12, hard error on HTTP 500, hard error on empty org id, hard error on negative member count.

The integration test exercises the bufconn platform end-to-end:

- `TestRegister_ListsConnector` — AC-1.
- `TestRun_PushesOrgPolicy` — AC-2/3/4 (scope tags, actor_id format, record receipt).
- `TestRun_OrgPolicyDedupes` — anti-criterion P0 (same hour → same `record_id`).
- `TestRun_OrgPolicyRotatesAcrossHour` — freshness guarantee.

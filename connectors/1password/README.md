# 1Password connector

Emits one evidence kind:

| Kind                      | Profile | Source                                                                             |
| ------------------------- | ------- | ---------------------------------------------------------------------------------- |
| `1password.org_policy.v2` | pull    | Events API `GET /api/v2/auth/introspect` + Users API `GET /v1/accounts/{id}/users` |

The connector is **pull-only by design**. Canvas §4.2 marks 1Password as `Query`-only, and adding a webhook surface would violate the canvas. Each run emits one record:

| Field                  | Source                                                                                        |
| ---------------------- | --------------------------------------------------------------------------------------------- |
| `org_id`               | introspect `account_uuid`                                                                     |
| `audit_events_enabled` | introspect `features` contains `auditevents` (the integration is authorized for audit events) |
| `active_members`       | users with `state: ACTIVE`                                                                    |
| `suspended_members`    | users with `state: SUSPENDED`; users in any other state are in neither count                  |

**What it cannot report (#2968).** v1 emitted `two_factor_required`, `minimum_password_length` and `domain_restrictions_enabled` from `GET /v1/account`, an endpoint no 1Password documentation describes. No documented 1Password API reports an account's two-factor requirement or password rules, so v2 drops those fields. The record's verdict is `fail` when the audit-event stream is off and `inconclusive` otherwise. It never passes. See `docs/audit-log/2968-1password-documented-sources-decisions.md`.

## Auth: two credentials, environment only

| Variable                          | What it is                                                                                        | Grant                    |
| --------------------------------- | ------------------------------------------------------------------------------------------------- | ------------------------ |
| `ONEPASSWORD_EVENTS_TOKEN`        | Bearer token of an Events Reporting integration (Integrations > Events Reporting)                 | the audit events feature |
| `ONEPASSWORD_USERS_CLIENT_ID`     | Client ID of a Users API OAuth application (Integrations > OAuth Application, client credentials) | `users.view` only        |
| `ONEPASSWORD_USERS_CLIENT_SECRET` | That application's client secret, shown once at creation                                          | —                        |

No flag carries a 1Password secret, so none reaches `ps` or shell history. Every request refuses a redirect that changes host, port or scheme, so the bearer and the client secret stay on the origin they were sent to. `opauth.Credentials` redacts all three values under every format verb.

**Banned grants:** `users.suspend`, `users.reactivate`, any write or admin grant. `atlas-1password scopes` prints the documented set, and `TestDocumentedScopes_NoWriteOrManageOrAdmin` rejects any write keyword in it.

The Users API needs 1Password Business or Enterprise Password Manager (not Teams). 1Password's docs say it isn't currently supported for accounts that use automated provisioning.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-1password register \
  --endpoint platform.example.com:443 \
  --platform-token "$SECURITY_ATLAS_TOKEN"

# Read the account state and push the evidence record.
export ONEPASSWORD_EVENTS_TOKEN=...          # from your secret manager
export ONEPASSWORD_USERS_CLIENT_ID=...
export ONEPASSWORD_USERS_CLIENT_SECRET=...
atlas-1password run \
  --endpoint platform.example.com:443 \
  --platform-token "$SECURITY_ATLAS_TOKEN" \
  --environment prod

# Print the documented least-privilege grants.
atlas-1password scopes
```

For an account outside 1password.com, set both base URLs for its region: `--events-base-url https://events.1password.eu --users-base-url https://api.1password.eu`. The other regions are `.ca` and `ent.1password.com`.

## Idempotency

Each run derives `idempotency_key = sha256("1password.org_policy.v2" | org_id | hour_truncated_observed_at)`. Two runs within the same hour for the same account collapse to one ledger row. Runs that cross an hour boundary write a new row. The `.v2` prefix keeps a v2 record from colliding with a v1 record from the same hour.

## Anti-criteria (P0)

- A 1Password secret on a flag → REJECTED. `TestRun_NoSecretFlags`.
- A secret in logs → REJECTED. `Credentials.String()` redacts.
- A redirect to another origin → REJECTED. `opauth.CheckRedirect`.
- Push without `idempotency_key` → REJECTED. The builder derives it from `kind|org_id|hour`.
- Mutating 1Password data → REJECTED. The only requests are two GETs and the OAuth token POST. The Users API's suspend and reactivate calls are never wired.
- A field without a documented source → REJECTED (#2968).
- Webhook subcommand → REJECTED by design. Canvas §4.2 is `Query` only.

## Tests

```sh
go test ./connectors/1password/...
```

Tests replay 1Password's documented response shapes from `internal/opaccount/testdata/`. Its README lists each fixture's source page. None was recorded from a live account, so the first live run should capture real responses and replace the fixtures.

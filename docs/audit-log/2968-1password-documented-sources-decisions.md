# #2968 — 1Password `1password.org_policy.v2` from documented APIs (decisions log)

Follows [#2830](https://github.com/mgoodric/security-atlas/issues/2830). Its
read of 1Password's docs, on 2026-11-22, found that the connector's only
source, `GET https://api.1password.com/v1/account`, is not in any
1Password documentation. ADR 0028 decision 8: the fix is a new schema
version.

- detection_tier_actual: manual_review
- detection_tier_target: contract

The v1 connector's tests replayed a response shape that 1Password never
published. Fixtures taken from the published API shape are the
contract-tier net, and this PR adds them
(`connectors/1password/internal/opaccount/testdata/`).

## Sources, and what is not verified

Read at www.1password.dev on 2026-11-22. developer.1password.com now
redirects there. Nothing here was checked against a live 1Password
account.

| Fact                                                                                                                                                                                                              | Source                                                                                                     |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| No documented endpoint returns `GET /v1/account`, an org's two-factor requirement, minimum password length, domain restrictions or a member count                                                                 | Users API, Events API, Connect server API, Partnership API and Service Accounts pages. None documents one. |
| `GET /api/v2/auth/introspect` on `events.1password.com` (`.ca`, `.eu`, `events.ent.1password.com`) returns `uuid`, `issued_at`, `features[]` and `account_uuid`                                                   | `events-api/reference/introspect.md`                                                                       |
| `features` lists "events (features) a bearer token is authorized to access"; the values shown are `auditevents`, `itemusages` and `signinattempts`                                                                | the same page                                                                                              |
| The Events API bearer is the JWT of an Events Reporting integration                                                                                                                                               | `events-api/authorization.md`                                                                              |
| Users API token: `POST /v1/oauth/token`, form `grant_type=client_credentials`, HTTP Basic client auth recommended; the response has `access_token` (`op_o_c_…`), `token_type` `Bearer` and `expires_in` 900       | `users-api/request-access-token.md`, `users-api/authorization.md`                                          |
| Users API scopes are `users.view` (read), `users.suspend` and `users.reactivate`; scopes are fixed when the OAuth application is created                                                                          | `users-api/authorization.md`                                                                               |
| `GET /v1/accounts/{account}/users`: `max_page_size` up to 1000; `next_page_token` is omitted on the last page; an empty page is `{}`; user `state` is `ACTIVE` or `SUSPENDED`, and is omitted for any other state | `users-api/list-users.md`, `users-api/conventions.md`                                                      |
| `{account}` is "a 26-character uppercase ID"; a mismatched one returns 403 "Account UUID does not match token"                                                                                                    | `users-api/list-users.md`                                                                                  |
| The Users API needs 1Password Business or Enterprise Password Manager (not Teams), and isn't supported alongside automated provisioning                                                                           | `users-api.md`                                                                                             |
| A 1Password SCIM bridge API                                                                                                                                                                                       | **Not documented.** The only SCIM in the docs is SaaS Manager's, which is a different product.             |

## D1 — The fields v2 keeps, and the ones it drops

| v1 field                      | v2                                     | Documented source                            |
| ----------------------------- | -------------------------------------- | -------------------------------------------- |
| `org_id`                      | kept, required                         | introspect `account_uuid`                    |
| `two_factor_required`         | **dropped**                            | none                                         |
| `minimum_password_length`     | **dropped**                            | none                                         |
| `domain_restrictions_enabled` | **dropped**                            | none                                         |
| `active_members`              | kept, required                         | Users API: count of `state: ACTIVE`          |
| —                             | `suspended_members`, required (new)    | Users API: count of `state: SUSPENDED`       |
| —                             | `audit_events_enabled`, required (new) | introspect `features` contains `auditevents` |

Users with `state` omitted (pending confirmation and other states) are in
neither count. `org_id` changes meaning: v1 took it from an undocumented
`id`, and v2 takes it from the account UUID. The kind name stays
`org_policy` so the registry lineage is one kind, although v2 holds no
policy setting. Renaming the kind would leave v1 records orphaned under
another name. Removing required fields is a major bump, so it is 2.0.0.
`1.0.0.json` stays registered, so old v1 records keep validating.

## D2 — What `audit_events_enabled` means, exactly

It is true when the Events Reporting integration whose token the
connector holds is authorized for audit events. It doesn't say the
account has no other integration reading audit events. Introspect
describes only the calling token. So `false` means the account's
audit-event stream doesn't reach this deployment through the
integration it was configured with. For a GRC tool that is a failure in
its own right: the audit trail the program claims to collect isn't being
collected here. The schema's field description says this, and so does
the cc6_3 description.

The connector does not page through `/api/v2/auditevents` to prove events
actually flow. Introspect answers the question from one call. Reading
events is a separate surface, and no check needs it yet.

## D3 — Two credentials, environment only, behind a redirect guard

The two APIs authenticate differently, so the connector needs two
credentials, held in three variables:

- `ONEPASSWORD_EVENTS_TOKEN`, the Events Reporting bearer;
- `ONEPASSWORD_USERS_CLIENT_ID` and `ONEPASSWORD_USERS_CLIENT_SECRET`, the
  Users API OAuth application. Grant it only `users.view`.

All three are read from the environment only. The v1 `--token` flag and
`ONEPASSWORD_SERVICE_ACCOUNT_TOKEN` are removed, because a 1Password
Service Account has no documented role in either API.
`TestRun_NoSecretFlags` checks that `run --token` is an unknown flag.
`--platform-token` (the security-atlas bearer) is unchanged, as in the
Datadog connector (#2935). Taking it off the command line is a
cross-connector change and out of scope here.

The connector requests one access token per run. Tokens last 900 seconds,
and one run makes a handful of calls. It doesn't revoke the token
afterwards.

Every request goes through `opauth.CheckRedirect`, which refuses a
redirect that changes host, port or scheme and stops after 10. It is the
same guard as the Confluence (#2947) and Datadog (#2935) connectors use.
Two tests, `TestClient_RefusesRedirectToAnotherHost` and
`TestDoRun_RefusesRedirectToAnotherHost`, assert that the other host
receives no request. I ran both with the guard removed, and both fail.

`{account}` in the Users API path is the introspect `account_uuid`. The
docs call the path value "a 26-character uppercase ID", and the 403 for a
mismatch says "Account UUID does not match token". That is the basis for
treating them as one identifier. It is not verified live. If the two
differ, the Users API returns 403, the run stops, and nothing is pushed.

Any failed call stops the run with no record. A record built from half the
sources would report a count or a stream state nobody read. The user
listing returns every page or an error. It is bounded at 1000 pages of
1000 users, and refuses a repeated `next_page_token`.

## D4 — The connector's verdict never passes

The per-record verdict is `fail` when `audit_events_enabled` is false and
`inconclusive` otherwise. v1 passed on 2FA plus a password length of at
least 12, and neither field has a documented source. Nothing v2 carries
can show that an account's sign-in policy is sound.

## D5 — cc6_3 fails on audit events off and is otherwise inconclusive (Matt, 2026-11-22)

Matt's call on this ticket, relayed by the dispatcher: cc6_3 must fail
when audit events are off and be inconclusive otherwise. It must never
pass, because 2FA and password rules can't be verified.

`soc2_cc6_3_privileged_account_management` moves from a Rego roll-up of
v1 verdicts to a SQL payload check on `1password.org_policy.v2` (ADR 0028
decision 1). It takes the latest in-window snapshot per `org_id` and
returns:

- `fail` if any account's latest snapshot has `audit_events_enabled`
  false;
- `inconclusive` otherwise, and also on zero rows.

Zero rows is the state of a deployment whose connector still pushes only
v1, and it is not a fail (#2901 D5). The bundle test
`v1-snapshot-is-not-read` pins this.

The stock-check guard `TestStockSQLChecksShipBundleTests` required a pass
case of every SQL check. It gains `stockSQLChecksWithoutPass`, the mirror
of the existing `stockSQLChecksWithoutFail`. A listed bundle needs a fail
case and an empty-ledger case, and it fails the test if it ever gains a
pass case. cc6_3 is its only entry.

The description says what runs, why the check can't pass, and what is not
checked: vault membership, the sharing default and per-vault audit
events (#2969), and the privileged-role member bound (#2834). The member
counts are recorded and not checked.

The query's `language`, `evidence_kind` and `expression` change, so on
the next boot the bundle supersedes (#1908). This PR carries the
`supersedes-stock` label (#2891). `stock_manifests_gen.go` gains one hash
and loses none (#2963).

**Upgrade note:** an existing deployment gets a new cc6_3 version on its
next boot. Owners, evidence, exceptions and risk links stay on the old
row, so re-assign and move them by hand. Until the 1Password connector is
upgraded and given its two new credentials, cc6_3 receives no v2 records
and is inconclusive. A deployment where cc6_3 passed on v1 now sees it
inconclusive at best. That is the honest state, not a regression.

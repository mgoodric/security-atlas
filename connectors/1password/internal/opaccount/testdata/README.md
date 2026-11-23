# 1Password API fixtures

These are shaped from 1Password's published API documentation, read
2026-11-22 for #2968. None was recorded from a live account. The first live
run should capture real responses and replace them.

| File                             | Documented shape                                                                                                                                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `introspect.json`                | The example response of `GET /api/v2/auth/introspect`, verbatim: <https://www.1password.dev/events-api/reference/introspect.md>                                                                   |
| `introspect_no_auditevents.json` | The same, with `auditevents` removed from `features` (an integration not granted audit events)                                                                                                    |
| `token.json`                     | `TokenResponse` of `POST /v1/oauth/token`: access tokens begin `op_o_c_`, `token_type` is always `Bearer`, `expires_in` is 900. <https://www.1password.dev/users-api/request-access-token.md>     |
| `users_page1.json`               | The example response of `GET /v1/accounts/{account}/users`, with the account id in `path` changed to the introspect example's `account_uuid`: <https://www.1password.dev/users-api/list-users.md> |
| `users_page2.json`               | A last page with no `next_page_token` ("Omitted when there are no more results"), and one user with `state` omitted, which the docs say happens for any state other than `ACTIVE` or `SUSPENDED`  |

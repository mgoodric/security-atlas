# #1766 — Operator and user docs for single-tenant by default: decisions log

detection_tier_actual: manual_review
detection_tier_target: contract

## D1 — `atlas-oscal import-*` gains the tenant default

The OSCAL catalog import page is in the ticket's list, but
`atlas-oscal import-catalog`, `import-profile` and
`import-component-definition` still required `--tenant-id`. #1762 made the
`atlas-cli` tenant flags optional and left this second binary out. A docs
change alone could not stop the page asking for a tenant ID, so the three
commands now apply the #1762 rule through `cmd/atlas-oscal/tenant.go`. The
flag wins when set. Without it, the install's only tenant is used and named
on stderr, and several tenants are refused with the list. It resolves with
`tenancy.ResolveImplicitTenantAnyRole`, so the documented `atlas_app` DSN
works. The helper copies `atlas-cli`'s `chooseTenant` rather than sharing
it: the two are separate `main` packages, and a shared package for about 25
lines is not worth the extra seam.

## D2 — Two broken examples fixed while touching them

Running each changed example against a single-tenant install found two
faults that had nothing to do with tenants. Both are fixed in place:

- `credentials issue --ttl 30d`: Go durations have no `d` unit, so the
  command exits 1. The example now uses `--ttl 720h`, and the page says the
  value is a Go duration. The flag's own help text still says `30d`; that
  is a CLI fix and is left for its own ticket.
- `docs/getting-started/first-evidence.md` passed the HTTP URL
  (`http://localhost:8080`) as the gRPC `--endpoint`, which fails with
  `too many colons in address`. It now passes `localhost:50051 --insecure`.

## D3 — Found, not fixed (out of scope)

- The recovery command on `troubleshooting/first-login.md` passes
  `--token "$ATLAS_BOOTSTRAP_TOKEN"` with `--reset-bootstrap --force`. It
  returns `401` from the HTTP endpoint with or without `--tenant`. The HTTP
  API takes JWTs, not API keys, so the bootstrap token is refused. With a
  super-admin JWT, the command works without `--tenant`. Reported for its
  own ticket. The page still names the token, as before; only the tenant
  flag was removed.
- `/auth/oidc/login?idp=primary` resolves the tenant on a single-tenant
  install, then answers `400 unknown IdP` even after `PATCH /v1/admin/sso`.
  The server wires the no-op `localModeIdpResolver`. Tracked in #2034.
- `demo seed` in place accepted an organisation holding imported OSCAL
  catalogs. The documented rule names evidence, risks, tenant-private
  framework versions, and non-stock controls and policies; imported
  catalogs are none of those. The docs state that rule as it is.

# 2596 — Free-text role labels derive nothing at OPA: decisions log

Issue: mgoodric/security-atlas#2596, from the review of #2595 (#2507).
Matt's decision (2026-11-17): a free-text role label on a credential whose
label is its grant (an unbound API key, a client_credentials machine token,
a legacy credstore credential) grants **nothing**. #2595 made the handler
gates refuse it. `derivedRolesFor` still turned any label that was not
viewer/auditor-only into `control_owner`, so OPA admitted those callers to
the control, evidence and activity reads the handler refused (#2507 D2, the
recorded split). This change makes OPA agree.

In this log a credential's "label" is its `OwnerRoles`: an API key's
`owner_roles`, a JWT's role claim for its current tenant.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The split was found in the
  #2595 review and pinned by `TestReadGates2507_FreeTextLabelOPAAdmitsHandlerRefuses`.
- **detection_tier_target:** unit. `TestFreeTextLabels2596_HandlerAgreesWithOPA`
  runs every shape through authzmw (`BuildInput` + `Decide` on the real
  bundle) and the route's handler gate for every #2428 route. It failed on
  the old derivation (OPA refused the canonical-labelled shapes the handler
  admitted, and admitted the free-text ones it refused) and passes now.

## Decisions

### D1: a label-is-grant credential derives exactly the canonical roles its label names

`derivedRolesFor`, after the flags (`IsAdmin` → admin, `IsApprover` →
grc_engineer), a user's session (nothing, #2496) and a key bound to a user
(nothing, D4):

- a non-empty label derives the canonical roles in it, in label order, each
  once (`labelRoles`). Free-text entries are dropped. A label with no
  canonical entry derives **nothing**; it does not fall through to the
  grc_engineer default.
- an empty label keeps the grc_engineer default (D3).

This replaces three cases: the machine-token viewer/auditor case (#2381,
which derived nothing), the unbound-key viewer/auditor case (#2402, which
derived the label), and the `len(OwnerRoles) > 0 → control_owner` fallback.

### D2: shape inventory, old roles → new roles

Roles `BuildInput` hands OPA before the resolver merges live `user_roles`.
Pinned row by row in `TestDerivedRoles2596_Inventory`.

| Shape                                          | Old                 | New                          |
| ---------------------------------------------- | ------------------- | ---------------------------- |
| `IsAdmin` flag, any label                      | admin               | admin (unchanged)            |
| `IsApprover` flag, any label                   | grc_engineer        | grc_engineer (unchanged)     |
| User session (`jwt:`, user subject), any claim | nothing             | nothing (unchanged, #2496)   |
| Unbound key, no label                          | grc_engineer        | grc_engineer (unchanged, D3) |
| Unbound key, `viewer` / `auditor`              | viewer / auditor    | unchanged                    |
| Unbound key, `control_owner`                   | control_owner       | unchanged                    |
| Unbound key, `grc_engineer`                    | control_owner       | **grc_engineer** (widens)    |
| Unbound key, `admin`                           | control_owner       | **admin** (widens)           |
| Unbound key, free text only                    | control_owner       | **nothing**                  |
| Unbound key, free text + canonical             | control_owner       | the canonical part           |
| Machine token, no claim                        | grc_engineer        | grc_engineer (unchanged, D3) |
| Machine token, `viewer` / `auditor`            | nothing (#2381)     | **viewer / auditor**         |
| Machine token, `control_owner`                 | control_owner       | unchanged                    |
| Machine token, `grc_engineer` / `admin`        | control_owner       | **that role** (widens)       |
| Machine token, free text only                  | control_owner       | **nothing**                  |
| Legacy credential, no label                    | grc_engineer        | grc_engineer (unchanged, D3) |
| Legacy credential, `viewer`                    | control_owner       | **viewer**                   |
| Legacy credential, free text only              | control_owner       | **nothing**                  |
| Legacy credential, no tenant                   | nothing             | nothing (unchanged)          |
| Key with empty UserID, `viewer`                | nothing             | **viewer** (D5)              |
| Key with empty UserID, free text               | control_owner       | **nothing**                  |
| Key bound to a user, any label                 | nothing (+ ceiling) | unchanged (D4)               |

### D3: a bare credential keeps grc_engineer (decided, not changed)

An unbound key, machine token or legacy credential with no flag and no
label still derives grc_engineer. It is the connector-push and bootstrap
path: `system.rego` admits evidence push for a machine actor holding
grc_engineer, and a client_credentials token is minted with an empty
`Roles` map (`internal/api/oauth/token.go`), so every machine token is
bare. Taking it away would break every push connector.

The handler gates refuse a bare credential (`HasAnyTenantRole`,
`IsTenantRiskReader` and the others count no role for it), as slice 067 and
#2556 D2 intended. The handler is the narrower layer there and nothing
reaches a handler that OPA refused. `TestFreeTextLabels2596_HandlerAgreesWithOPA`
covers free-text and canonical-label shapes; a bare credential is outside
its shapes on purpose.

### D4: bound keys unchanged; the ceiling keeps free text as control_owner (Matt, 2026-11-20)

A key bound to a user already derived nothing from its label (#2431). Its
roles are its owner's live `user_roles`, capped by `boundKeyCeiling`, and
`Decide` stamps those capped roles on `cred.TenantRoles` (`Decision.LiveRoles`),
so the handler gates read the same set. That is the "owner's live roles
only" the ticket asks for: no label-derived role reaches OPA for a bound
key.

The ceiling still maps a free-text entry to control_owner. A ceiling only
narrows what the owner holds live, so it grants nothing; dropping free text
from it would leave a `{platform-eng}` key with an empty ceiling and no
roles at all, which breaks the free-text policy-ack and attest labels #2541
kept. Matt kept it as it is.

### D5: one "label is the grant" predicate for every reader

The agreement test found the handler disagreeing with itself:

- `ownerLabelIsGrant` (read by `IsTenantAdmin`, `IsTenantApprover`,
  `IsTenantControlOwner`) refused every JWT's label, a machine token's
  included, but counted the label of a key with an empty UserID.
- `roleLabelIsGrant` (read by `TenantRoleLabels`, so the viewer, auditor and
  any-role gates) counted a machine token's label and refused the
  empty-UserID key's.

A machine token labelled `admin` therefore passed OPA as admin while
`IsTenantApprover` refused it. `ownerLabelIsGrant` is gone. Every reader,
and through `derivedRolesFor` OPA, uses `roleLabelIsGrant`: true unless the
credential is a user's session or a key bound to a user. A key with an
empty UserID counts as unbound, as `boundKey` and `is_machine_actor` already
treat it. credstore never mints one (`UserID` is the key's own id or the
owner's), so it only affects hand-built credentials.

### D6: the widening, admin and grc_engineer labels (Matt, 2026-11-20) — Breaking / Security

D1 maps a canonical label to its role, so an unbound key or machine token
labelled `admin` or `grc_engineer` now holds that role at OPA, where it held
control_owner. This is OPA catching up to the handler, not passing it:
`IsTenantAdmin` and `IsTenantApprover` have counted an unbound key's label
since #2428 ("An API key's owner_roles label keeps counting"). The
alternative was to narrow both layers and stop counting those labels
anywhere. Matt chose agreement with the handler.

The invariant: **a label is the role.** An unbound key or machine token
labelled `admin` is admin at OPA on every route, and one labelled
`grc_engineer` is grc_engineer on every route, exactly as a user holding
that live role would be. The list below is not that set. It is only the
routes in the `TestDecide2402_APIKeyRoleMatrix` matrix (`routes2381`, real
Postgres) that flip from deny to allow at OPA for an unbound key:

- label `admin`: `POST /v1/admin/users`, `GET /v1/admin/audit-log/unified`,
  `POST /v1/risks`, `POST /v1/controls`, `POST /v1/policies/{id}/approve`,
  `POST /v1/policies/{id}/publish`, `GET /v1/samples`, `GET /v1/dashboard`,
  `GET /v1/me/audit-periods`, `POST /v1/audit-notes`, `GET /v1/risks`;
- label `grc_engineer`: the same list without `POST /v1/admin/users`.

The same holds for a machine token with such a claim. It is **latent on
HTTP today**: the HTTP router authenticates JWTs only, and API keys reach the
platform over gRPC alone, which calls no gate (#2541 D5); and no
client_credentials token carries a role claim (D3). The change matters the
day an HTTP key path or a role-carrying machine token lands.

Routes in the same matrix that flip from allow to deny (the free-text
narrowing), for an unbound key whose label is free text only: `GET /v1/controls`,
`GET /v1/policies`, `GET /v1/framework-scopes`, `GET /v1/evidence`,
`GET /v1/evidence/{id}`, `GET /v1/evidence/freshness`,
`GET /v1/artifacts/{id}`, `GET /v1/walkthroughs`, `GET /v1/calendar`,
`POST /v1/evidence`, `POST /v1/evidence:push`, `POST /v1/artifacts:upload`,
`POST /v1/walkthroughs`. A `{viewer, platform-eng}` key loses the
control_owner writes and evidence reads and gains the viewer risk read.

### D7: where OPA admits whatever the roles

Two #2428 routes are admitted by OPA for a credential holding no role, so
the agreement test lets the handler be narrower there:
`GET /v1/frameworks/posture` (a catalog read, `defaults.rego`) and
`POST /v1/controls:upload-bundle` for a machine actor (`system.rego`, slice
196). Neither depends on the label, and neither is changed here.

## Verification

```
$ go test ./internal/...
(all ok)
$ go test -tags=integration -p 1 -count=1 ./internal/authz/... ./internal/api/authzmw/... \
    ./internal/api/adminauditlog/... ./internal/api/controldetail/... ./internal/api
ok  internal/authz, internal/api/authzmw, internal/api/adminauditlog,
    internal/api/controldetail, internal/api
```

Run against a `scripts/scratch-pg.sh` cluster with `$DATABASE_URL_APP` set.

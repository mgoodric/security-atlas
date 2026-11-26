# #2901 — Okta `okta.mfa_policy.v2`: Everyone, WebAuthn, status (decisions log)

Follows [#2821](https://github.com/mgoodric/security-atlas/issues/2821)
(PR #2897), whose review found that the Okta connector's MFA-policy
snapshot could not support cc6_1's check on a real tenant. ADR 0028
decision 8: the fix is a new schema version.

- detection_tier_actual: manual_review (connector defects); unit (D5, the v1-only window)
- detection_tier_target: contract

The three defects (group ids reported where cc6_1 matched the name
`everyone`, no `webauthn` from Identity Engine policies, policy status
dropped) were caught in code review of #2897. Each was a mismatch between
the connector's test fixtures and Okta's real response shape. A fixture
taken from Okta's published API shape is the contract-tier net, and this
PR adds those fixtures.

## Sources, and what is not verified

Nothing here was checked against a live Okta tenant.

| Fact                                                                                                                                                               | Source                                                                                                                                                                                                                                                                       |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `MFA_ENROLL` list response: `status`, `priority`, group **ids** in `conditions.people.groups.include`, `settings.type: AUTHENTICATORS` with `authenticators[].key` | `list-mfa-enroll-policy-response` example in `okta/okta-management-openapi-spec` `dist/current/management-minimal.yaml`                                                                                                                                                      |
| Enrollment-policy authenticator keys (`webauthn`, `okta_verify`, `okta_password`, …) and `enroll.self` values                                                      | same spec, `AuthenticatorEnrollmentPolicyAuthenticatorType` / `...Status` enums                                                                                                                                                                                              |
| Classic and pre-upgrade policies use a `settings.factors` map, mutually exclusive with `authenticators`; `fido_webauthn` maps to authenticator key `security_key`  | Okta developer guide "Understand authenticator enrollment policy API changes after the upgrade" (`okta/okta-developer-docs`, `guides/oie-upgrade-mfa-enroll-policy`)                                                                                                         |
| Classic factor names `fido_webauthn`, `fido_u2f`, `okta_otp`, `okta_push`, …                                                                                       | `okta/terraform-provider-okta` `sdk/policy.go` (`PolicyFactorsSettings`). The current OpenAPI spec no longer documents the Classic factor map.                                                                                                                               |
| `GroupType` `BUILT_IN` ("managed by Okta and can't be modified"); List all groups recommends `search`; Everyone is hidden from a Group Administrator               | same OpenAPI spec, `GroupType` enum and `listGroups` parameters                                                                                                                                                                                                              |
| The Everyone group is the `BUILT_IN` group with `profile.name` `Everyone`                                                                                          | **Not stated by Okta's docs.** BUILT_IN groups are Okta-managed and Everyone is Okta's default group. The spec has no Everyone example. Check this on a live tenant before relying on it; an org where it doesn't hold stops the pull (D1) rather than reporting false data. |

## D1 — Everyone is resolved by lookup, and its absence stops the pull

The ticket allowed resolving the id or emitting a boolean. The connector
does both. `applies_to_groups` keeps Okta's group ids verbatim, and
`applies_to_everyone` is true when they include the Everyone group's id.
The connector finds that id with
`GET /api/v1/groups?search=type eq "BUILT_IN"`, picking the group named
`Everyone`. An admin-made `OKTA_GROUP` that happens to be named Everyone is
not taken.

If no such group comes back, the pull returns `ErrEveryoneNotFound` and
pushes no MFA-policy records. Okta hides Everyone from a Group
Administrator token, and in that case reporting `applies_to_everyone:
false` for every policy would be false evidence. A failed run is visible;
a false `false` is not. The lookup needs `okta.groups.read`, which the
connector's Read-only Administrator token already holds for app
assignments. The scope's documented `Gates` now names both uses.

## D2 — One canonical key for FIDO; the password is not a factor

Both settings shapes are parsed. `settings.authenticators[]` is read when
present or when `settings.type` is `AUTHENTICATORS`, and the
`settings.factors` map is read otherwise. Keys are lower-cased.
`fido_webauthn`, `fido_u2f`, `webauthn` and `security_key` all become
`webauthn`:

- `fido_webauthn` (Classic) and `webauthn` (enrollment-policy authenticator)
  are the ticket's named pair.
- `security_key` is what Okta's own factor-to-authenticator mapping table
  gives for `fido_webauthn`. It is not in the enrollment-policy enum, but
  normalising it costs nothing if an org returns it.
- `fido_u2f` is a FIDO security key and phishing-resistant, so it counts.
  The ticket said "(also `fido_u2f`)". The alternative, a separate
  canonical `u2f`, would make every consumer list both.

Other keys pass through unchanged: `okta_push`, `okta_otp`, `okta_verify`
and the rest. Mapping Classic `okta_push`/`okta_otp` to `okta_verify` would
be tidy, but no check needs it yet.

`okta_password` is dropped from `factors_allowed` and from `mfa_required`.
Identity Engine policies list the password as a REQUIRED authenticator (it
is in Okta's own example). Under v1's rule that would set `mfa_required`
true for a policy requiring only a password. A password is the first
factor, so requiring it is not MFA. The schema description says so.

`factors_allowed` is now sorted. v1 built it by ranging over a Go map, so
its order changed between runs.

## D3 — Status and priority are emitted; inactive policies are `na`

The ticket offered "emit `status`, or drop non-ACTIVE policies". Status is
emitted, so an auditor can see that an inactive policy exists. The
connector's per-record verdict for a non-ACTIVE policy is `na`: Okta
doesn't apply it, so it neither passes nor fails an MFA requirement, and a
tenant roll-up over `result` isn't failed by a leftover draft. cc6_1
ignores the verdict and filters on `status = 'ACTIVE'` itself.

`priority` is emitted when Okta reports one (omitted rather than 0, since
the schema sets `minimum: 1`). cc6_1 does not use it yet. Its description
still says a higher-priority policy for a subset of users isn't seen.
Using priority correctly needs per-user group membership, which is the
inventory design (#2834).

## D4 — v2 is a breaking major; the connector stops emitting v1

v2 adds required fields (`status`, `applies_to_everyone`, and the two
lists, which were optional), and it changes what `factors_allowed` and
`mfa_required` mean (D2). That is a major bump by the registry's
discipline (`schemas/README.md`), not an additive 1.1.0. `1.0.0.json`
stays registered. Old v1 records keep validating, and the 90-day removal
floor (slice 179) applies if anyone ever removes it.

The connector emits only v2. Emitting both would double the records, and
v1 is the shape that is wrong. Its `SupportedKinds` and documentation now
list v2. The demo seed writes v2 rows too, so demo data matches what real
ingest stores (#2863).

## D5 — Supersedes the stock bundle `soc2_cc6_1_logical_access_baseline`

cc6_1's query moves to `evidence_kind: okta.mfa_policy.v2`. It requires
`status` ACTIVE and `applies_to_everyone` true, and drops the
case-insensitive `everyone` name match (#2821 D4), which never matched a
real tenant. The `jsonb_array_elements_text` subquery goes with it. A
`jsonb_typeof(... ) = 'array'` guard stays on `factors_allowed`, so a
malformed payload can't make `?` test a string. The latest snapshot per
policy still decides (#2821 D2).

One shape change from #2821 D1: the query returns a result string, not
`coalesce(bool_or(...), false)`. It returns `inconclusive` when it receives
no v2 snapshot. The engine makes an empty window `inconclusive` on its own,
but a window holding only v1 records is not empty. The kind filter (#2818)
hands the query zero rows, and the old shape turned that into `fail`. That
is the state of every deployment that upgrades the platform before its Okta
connector, and "no v2 evidence yet" isn't a failed control. The bundle test
`v1-snapshot-is-not-read` caught this: it expected `inconclusive` and got
`fail`. The pattern for later checks: a payload check whose kind can
arrive alongside an older version returns `inconclusive` on zero rows
itself.

The description drops the "Until #2901 lands" caveat and the "inactive
policy counts" line, and says v1 snapshots are not read. A new bundle test,
`v1-snapshot-is-not-read`, pins that. Another,
`group-named-everyone-is-not-the-everyone-group`, pins that the name match
is gone.

The query's `evidence_kind` and `expression` change, so on the next boot
the bundle supersedes (#1908). The new control version has a new control
id, and owners, evidence, evaluations, exceptions and risk links stay on
the old row. This PR carries the `supersedes-stock` label (#2891).

**Upgrade note:** an existing deployment gets a new cc6_1 version on its
next boot. Re-assign its owner and move exceptions and risk links by hand,
as for #2821. Until the Okta connector is upgraded, cc6_1 reads no records
and is inconclusive, because old connectors emit only v1. Upgrade the
connector with the platform. The connector also needs its token to see
the Everyone group (D1).

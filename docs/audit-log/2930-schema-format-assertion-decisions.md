# #2930 — Schema registry `format` assertion: per-version opt-in (decisions log)

Found in the #2920 review (PR #2929): `compileSchema` never called
`AssertFormat()`, so `format` keywords in evidence schemas were
annotation-only for every kind.

- detection_tier_actual: manual_review
- detection_tier_target: unit

## Inventory (main at f78730a, 2026-11-22)

25 of the 65 embedded schema versions (19 kinds) declare `format`:
`date-time` x24, `uuid` x9, `uri` x7, `date` x2, `email` x2. Kinds:
change.approval, change.verification, datadog.siem_signal,
github.audit_event, github.scim_user, hris.worker_lifecycle,
incident.postmortem, jira.ticket_evidence (1.0.0, 1.1.0),
k8s.secret_inventory, manual.attestation (1.0.0-1.2.0),
monitoring.alert_firing, okta.user_lifecycle, osquery.host_posture
(1.1.0-1.3.0), pagerduty.incident_summary, pagerduty.postmortem_summary,
pagerduty.response_metrics, policy.acknowledgment, sast.scan_result
(1.0.0, 1.1.0), security_awareness.training_completion.

## Conformance trial with assertion on

The trial was a throwaway edit, reverted and never committed (Matt allowed
it, 2026-11-22): an unconditional `c.AssertFormat()` in `compileSchema`.

- `go test -count=1` over `./connectors/{osquery,jira,github,okta,aws}/...`,
  `./internal/demoseed/...` and `./internal/api/schemaregistry/...`: all 27
  packages `ok`.
- Covered format-bearing kinds: jira.ticket_evidence, osquery.host_posture,
  github.audit_event, github.scim_user, manual.attestation,
  okta.user_lifecycle, sast.scan_result (connector conformance tests plus
  demoseed).
- To check that the trial edit took effect, a temporary test validated
  `last_seen_at: "not-a-date"` under osquery.host_posture 1.2.0. With the
  edit it was refused: `jsonschema validation failed`. Before it, the same
  payload is accepted.
- Not covered: the other 12 format-bearing kinds have no conformance test
  that pushes real connector output. Third-party and tenant-registered
  pushers can't be measured from this repo.

## D1 — Per-version opt-in via `x-assert-format: true` (Matt's decision)

The options were enforcing for all, opting in per version, or documenting
the gap only. Matt chose opt-in. Published versions are contracts, and
turning enforcement on for all of them would refuse payloads that third-party
or tenant pushers send today without a version bump. Documenting only would
keep per-check regex guards (cc6_6) in place. Ingest-time validation never
touches the stored ledger, so existing records are unaffected either way.
No embedded version opts in with this PR. `TestEmbeddedSchemas_AssertFormatOptIns`
pins that, and the first version to opt in adds itself to the list.

## D2 — A non-boolean flag fails compilation

`"x-assert-format": "true"` (a string) could have been read as off, which
would hide the typo. It is refused at compile time instead, the same as any
other malformed schema. `false` and absent are equivalent.

## D3 — Not applied to `internal/risk` or `internal/control/attestation`

Matt asked for the same opt-in in the other two compilers if they load
registry schemas. Neither does. `risk/methodology.go` compiles built-in
inline constants that contain no `format` keyword. `control/attestation`
compiles each control manifest's `manual_evidence_schema`, which control
authors write. That is a separate surface, and any change to it is its own
decision.

## D4 — Enforced: opting in needs a new major or a new kind

Required by review of PR #2981. `CheckAdditiveOver` gains a fourth rule. A
minor whose `next` sets `x-assert-format: true` while `prev` does not
(absent or `false`) is refused with "x-assert-format turned on; enforcing
format narrows acceptance, so opting in needs a new major version or a new
kind". If both opt in, it passes. Turning the flag off widens acceptance and
passes. The registry runs this check on every tenant minor registration
(`service.go`, `Register`). `TestCheckAdditiveOver_AssertFormat` covers
absent->on and false->on (both refused) and on->on, off->off and on->off
(all pass). Only a literal `true` counts as on, the same as in
`compileSchema`.

## D5 — Version resolution: ingest never resolves to a later minor

Checked by reading the code at this head:

- `missingField` (`internal/evidence/ingest/ingest.go`) refuses a record
  with an empty `schema_version` ("schema_version is required"). The REST
  path does the same (`internal/api/evidence/service.go`).
- `ValidatePayload` -> `lookupCompiled` (`service.go`) looks up the exact
  `kind|semver` key: tenant cache, then global cache, then the tenant row by
  exact semver. If none matches it returns `ErrUnknownKind`. There is no
  major-only or "latest" fallback.
- A grep for latest-version resolvers in `internal`, `cmd` and `pkg` finds
  only SCF-anchor queries (`dbx/scf_anchors.sql.go`), none on the evidence
  path.

So a record pushed under `1.2.0` is only ever validated against `1.2.0`,
and a later opted-in version cannot reach it. This is why D4's rule is
enough.

## D6 — Stored tenant schemas that already carry `x-assert-format`

Before this PR the key meant nothing, so a tenant could have registered a
schema carrying it. After this PR:

- A stored tenant row with a non-boolean value (for example `"true"`) no
  longer compiles. Tenant rows compile lazily on lookup, not at boot, so the
  platform still starts. Pushes for that tenant's kind and version are
  refused with `compile tenant schema: x-assert-format must be a boolean`,
  which fails closed. Global rows come only from the embedded bundle, and
  `TestEmbeddedSchemas_AssertFormatOptIns` shows none carries the key.
- A stored tenant row with `true` starts enforcing `format`. This is the
  same narrowing D4 blocks for new minors, applied to a row already
  registered.

I did not check any deployed database for such rows; I have no access to
one from this session. The query to run against a deployment, as the
migrate role or with RLS context per tenant (written, not executed):

```sql
SELECT tenant_id, kind, semver, schema_json->'x-assert-format' AS flag
FROM evidence_kind_schemas
WHERE schema_json ? 'x-assert-format';
```

No rows means neither case applies.

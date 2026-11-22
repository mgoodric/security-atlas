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

## D4 — The additive check does not detect an opt-in

`CheckAdditiveOver` inspects only `properties`, `required` and
`additionalProperties`, so a minor that adds `x-assert-format: true`
passes as additive even though it narrows acceptance. That is tolerable:
records validate against the version they were pushed under, and the
narrowing only reaches pushers who pick the new version. It is documented
in the schemas README for reviewers. Teaching the additive check about it is
left as a possible follow-up, not done here.

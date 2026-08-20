# 625 - Incident register decomposition decisions

**Issue:** OPENENGINE-625 - security-atlas incident management
**Date:** 2026-07-30
**Type:** JUDGMENT

## D1 - Incident is the general primitive

**Decision.** Model the security incident register as the general in-app
primitive. The incident owns the lifecycle from `detected` to `triaged` to
`contained` to `resolved` to `closed`, the append-only timeline, severity,
affected systems, linked controls/risks/vendors, and post-mortem evidence.

**Rationale.** The IR plan from slice 372 is a governance document today. The
product gap is not a privacy-specific breach workflow; it is the absence of a
tenant-scoped operational register that can track any security incident and
generate evidence mapped to IR controls.

## D2 - Breach notification is triggered, not duplicated

**Decision.** A confirmed incident classified as a personal-data breach should
trigger the OE-507 privacy breach-notification workflow. The incident module
must not re-implement OE-507's 72-hour clock, notification-target register, or
privacy state machine.

**Rationale.** OE-507 is explicitly the privacy specialization and is still
`not-ready`, gated by the breach-notification ADR/privacy-v0 work. The incident
register should expose a narrow handoff seam: incident id, tenant id,
classification, confirmed timestamp, and privacy-allowed references to
`evidence.id` / `policy.id`. It must not pass `controls.id` across the
sibling-module boundary.

## D3 - Severity accepts tier input and floors gracefully

**Decision.** Incident severity should be stored explicitly but computed through
a resolver that can floor severity from affected-system criticality once OE-624
lands. Until tiering exists, the resolver should accept optional tier inputs and
fall back to the operator-selected severity.

**Rationale.** OE-625 should not block on OE-624. The schema/API should leave
room for affected-system tier evidence without inventing the final app-tiering
model. That keeps incident severity stable now while preserving the future rule:
critical affected systems raise the minimum incident severity.

## D4 - Decompose implementation into child OEs

**Decision.** This request crosses schema, lifecycle rules, RLS, evidence,
privacy handoff, web UI, and calendar surfaces. It is too large for one fire, so
the implementation is split into vertical child OEs:

1. Incident register backend: schema, lifecycle API, append-only audit/timeline,
   links, RLS tests, and post-mortem evidence generation.
2. Breach handoff wiring: trigger OE-507 when available without duplicating it
   or crossing the sibling seam.
3. Web incident workspace: list, detail, timeline, lifecycle actions, and linked
   controls/risks/vendors/evidence.
4. Calendar events: open incident and post-mortem due events through the existing
   compliance calendar JSON/ICS path.

**Rationale.** The backend slice keeps the DB contract and first writer/reader in
one unit. The breach, UI, and calendar surfaces depend on that contract and can
be verified independently after it lands.

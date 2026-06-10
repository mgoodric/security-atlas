# 654 — Validate schema `x-default-scf-anchors` against the bundled catalog (+ fix existing dangling anchors)

**Cluster:** Catalog
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (the remap target per dangling anchor + the guard's catalog source)
**Status:** `ready` — deps `internal/api/schemaregistry` + the SCF catalog fixture both on `main`.

## Narrative

Surfaced during slice 535, captured as follow-up per continuous-batch policy.

Every evidence-kind schema may carry an `x-default-scf-anchors` hint (the default
control-mapping suggestion an operator approves once). `internal/api/schemaregistry/embed.go`
PARSES the hint and `service.go` STORES it (`evidence_kind_schemas.default_scf_anchors`),
but **nothing validates that those anchors resolve to real anchors in the bundled
SCF catalog.** The slice-068 anchor-drift guard validates _control-bundle_ anchors,
not schema `x-default-scf-anchors` — so a schema can ship a hint that points at an
anchor absent from the bundled catalog, silently. The cost: the suggested mapping
resolves to nothing, so the evidence-kind appears to map to a control that the
deployment's catalog does not contain — a silent coverage-suggestion gap an
operator can't see until they try to approve the mapping.

**Confirmed dangling on `main` (verified `grep -c` against `migrations/fixtures/scf-sample.json`):**

| Schema                                  | Anchor hints       | Dangling (absent from bundled catalog) |
| --------------------------------------- | ------------------ | -------------------------------------- |
| `github.audit_event/1.0.0.json`         | `MON-01`, `MON-02` | **`MON-02`**                           |
| `pagerduty.incident_summary/1.0.0.json` | `IRO-02`, `IRO-09` | **`IRO-02`**                           |
| `pagerduty.response_metrics/1.0.0.json` | `IRO-02`, `MON-02` | **`IRO-02` + `MON-02`**                |

(`MON-01`, `IRO-09` are present. `IRO-02`/`MON-02` are real SCF anchors but are NOT
in the bundled sample fixture the seed/tests use.) Slices 636 and 535 already hit
this and navigated around it at author time — 636/535 chose `IRO-09` over `IRO-02`,
535 used the present `MON-01` — so the remap targets below match established
precedent.

## Threat model

Not a security surface — this is evidence-mapping **integrity / correctness**.
No STRIDE element materially applies (read-only validation of an in-tree fixture;
no tenant data, no new scope, no wire change). The one honest risk is a maintainer
trusting a mapping hint that resolves to nothing; this slice removes that risk.

## Acceptance criteria

- [ ] **AC-1.** A guard (a fast Go unit test in `internal/api/schemaregistry`, no
      Postgres — the pure-Go-pre-DB convention) asserts that EVERY embedded
      schema's `x-default-scf-anchors` resolves to an anchor present in the bundled
      SCF catalog fixture. It fails on a deliberately-dangling fixture (negative test).
- [ ] **AC-2.** Remap the 4 dangling references to present, semantically-closest
      anchors — `IRO-02` (Incident Handling) → `IRO-09` (Incident Reporting, present);
      `MON-02` (Continuous Monitoring) → `MON-01` (present) — in all three schemas,
      de-duplicating if the remap target is already listed (e.g. `pagerduty.incident_summary`
      `[IRO-02, IRO-09]` → `[IRO-09]`). Document each remap in the decisions log.
- [ ] **AC-3.** Decide + document (decisions log) whether the guard's catalog source
      is the seed fixture (`scf-sample.json`) or a fuller catalog — and whether the
      right long-term fix is instead to EXPAND the bundled sample to carry these
      anchors. (Default: validate against the bundled fixture and remap; expanding
      the catalog is a separate catalog-governance call.)
- [ ] **AC-4.** No schema `semver` bump where only the anchor-hint list changes
      AND no record consumed the old hint (these are author-time defaults, not
      record data) — OR bump + document if the registry treats the hint as part of
      the immutable schema identity. Make the call and record it.

## Anti-criteria (P0)

- Does NOT change any evidence-record shape or any schema's data fields — only the
  `x-default-scf-anchors` hint list + a new validation test.
- Does NOT touch the slice-068 control-bundle guard (separate surface).
- Does NOT expand the SCF catalog fixture as a side effect (that is AC-3's explicit,
  separately-decided option, not an incidental change).

## Dependencies

- `internal/api/schemaregistry` (embed + DefaultSeed) — on `main`.
- The SCF catalog fixture (`migrations/fixtures/scf-sample.json`) — on `main`.

## Notes

Process note (for the audit trail): slice 539's reconcile marker (batch 237)
recorded its anchors `IRO-02 + MON-02` as "verified real + in-repo" — that claim
was not actually grep-verified at the time and is FALSE against the bundled
fixture; this slice is the corrective. Subsequent slices (636/525/652/535) DID
grep-verify their anchors, which is how this gap surfaced.

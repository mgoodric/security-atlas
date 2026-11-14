# 1738 — Anchor coverage warning on bundle upload: decisions log

Issue #1738: several best-fit SCF anchors (NET-26.5, IRO-17, TDA-15.1, VPM-18,
RSK-12, TPM-01) have no SOC 2 mapping, so a control on one of them earns no SOC 2
credit and nothing says so. The upload now returns a non-fatal `anchor_warning`.

- detection_tier_actual: none
- detection_tier_target: none

No defect surfaced during the build.

## Decisions made

- **D1.** "Enabled framework" = a framework version the tenant has an `activated` FrameworkScope for. A tenant with none activated counts every `current` framework version. There is no per-tenant enablement table; the activated scope is the closest signal. Without one, the dashboard credits every current version (`dashboard.sql`), so that is what "earns credit" means for that tenant.
- **D2.** Only live edges count: the reverse-traversal query already drops `no_relationship` and `rejected` edges. Reuses `ListRequirementsForAnchor`, the same filter every coverage read applies. No new SQL, no sqlc regen.
- **D3.** The suggestion is the parent anchor, found by cutting the last dotted segment (`NET-26.5` -> `NET-26`), and only if the parent itself reaches an enabled framework. Otherwise the message points at a mapped anchor in the same family. `scf_anchors` has no parent column; SCF sub-control codes are dotted by convention. The issue names the parent as the usual nearest mapped anchor. A broader nearest-anchor search would guess at semantics.
- **D4.** Warn, never block. The check runs after the control is persisted, and a failed lookup is logged rather than turning a successful upload into a 500. Choosing the precise anchor is correct for a unified framework (invariant #1/#7); the control is valid, it just earns no credit in the enabled set.
- **D5.** Upload only, not `atlas-cli controls validate`. `validate` is local and has no catalog, the same reason it does not check that the anchor exists. The CLI's `upload` prints the warning on stderr.
- **D6.** A separate JSON field `anchor_warning`, not reuse of `gate_warning`. `gate_warning` belongs to the test-gate; a client can act on each independently.

## Revisit once in use

- If tenants routinely run without activated scopes, consider whether "every
  current framework" is too permissive (an anchor mapped only to HIPAA silences
  the warning for a SOC 2-only shop).
- Surfacing the same signal in the controls list / control detail UI.

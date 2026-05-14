# Control-kit review log — slice 010

> Pre-merge HITL review record for `controls/soc2/` — the stock SOC 2
> control library bundled in v1. Required by slice 010 AC-6.

## Provenance

- **Slice:** 010 (SCF-anchored SOC 2 control kit · 50 bundles)
- **Branch:** `control-as-code/010-soc2-control-kit`
- **PR:** TBD — opened with this log
- **Authored by:** Claude (agent), 2026-05-13
- **TSC source:** AICPA Trust Services Criteria 2017 (with 2022 points-of-focus)
- **SCF crosswalk source:** `data/crosswalks/soc2-tsc-2017.yaml` (slice 007)
- **SCF anchor catalog:** `migrations/fixtures/scf-sample.json` (slice 006)

## Verification summary (machine-checked)

Run via `go run ./cmd/scripts/coverage-check`:

| Gate                                               | Threshold | Actual          | Pass |
| -------------------------------------------------- | --------- | --------------- | ---- |
| AC-1: bundle count                                 | 50        | 50              | yes  |
| AC-1: every bundle parses + applicability_expr OK  | 100%      | 50 / 50         | yes  |
| AC-2: bundles with evidence_queries[]              | ≥ 50%     | 26 / 50 (52%)   | yes  |
| AC-3: TSC requirements covered via SCF traversal   | ≥ 80%     | 43 / 43 (100%)  | yes  |
| AC-4: owner_role + freshness_class set             | 100%      | 50 / 50         | yes  |
| AC-5: manual bundles have manual_evidence_schema   | 100%      | 24 / 24         | yes  |
| Invariant 7: every scf_anchor_id exists in catalog | 100%      | 35 / 35 anchors | yes  |

## Reviewer signoff

| Field        | Value                                                                                       |
| ------------ | ------------------------------------------------------------------------------------------- |
| Reviewer     | Matt Goodrich (`matt@mattgoodrich.com`)                                                     |
| Review date  | _Pending — to be filled on HITL signoff_                                                    |
| Review scope | Spot-check the 8 bundles in the table below; spot-check the SCF-anchor mapping for accuracy |
| Decision     | _approve / approve-with-fixes / reject_                                                     |
| Notes        |                                                                                             |

## Spot-check table (pre-filled for the reviewer)

The reviewer should evaluate each of these 8 bundles against the SOC 2 TSC
criterion text and check the four columns to the right. The sample
deliberately spans automated + manual, simple + complex, and the two
highest-judgment-call bundles (CC1.4 weak anchor + CC6.7 transmission
scoping).

| #   | Bundle ID                                   | TSC   | SCF anchor | Impl type       | TSC accuracy (Y/N) | Evidence query sensible (Y/N/n-a) | Freshness sensible (Y/N) | manual_evidence_schema (Y/N/n-a) | Reviewer notes |
| --- | ------------------------------------------- | ----- | ---------- | --------------- | ------------------ | --------------------------------- | ------------------------ | -------------------------------- | -------------- |
| 1   | `soc2_cc1_4_competence_training`            | CC1.4 | HRS-04     | automated       |                    |                                   |                          | n-a                              |                |
| 2   | `soc2_cc1_5_accountability_acknowledgment`  | CC1.5 | HRS-01     | automated       |                    |                                   |                          | n-a                              |                |
| 3   | `soc2_cc6_3_access_modification_revocation` | CC6.3 | IAC-07     | automated       |                    |                                   |                          | n-a                              |                |
| 4   | `soc2_cc6_7_encryption_in_transit`          | CC6.7 | CRY-08     | automated       |                    |                                   |                          | n-a                              |                |
| 5   | `soc2_cc7_1_vulnerability_remediation`      | CC7.1 | VPM-04     | automated       |                    |                                   |                          | n-a                              |                |
| 6   | `soc2_cc7_4_incident_response`              | CC7.4 | IRO-04     | semi_automated  |                    |                                   |                          |                                  |                |
| 7   | `soc2_cc9_2_vendor_risk_assessment`         | CC9.2 | TPM-04     | manual_periodic |                    | n-a                               |                          |                                  |                |
| 8   | `soc2_pi1_5_storage_integrity`              | PI1.5 | DCH-03     | manual_attested |                    | n-a                               |                          |                                  |                |

**Rubric for each column:**

- **TSC accuracy** — does the bundle title + description faithfully express the TSC criterion text? Y/N.
- **Evidence query sensible** — does the Rego / SQL actually capture the assertion? Will it pass on a compliant org and fail on a non-compliant one? Y/N/n-a (n-a = manual control with no query).
- **Freshness sensible** — does the chosen `freshness_class` match the auditor expectation for this control? (e.g. daily for endpoint posture, quarterly for access reviews, annual for tabletop exercises). Y/N.
- **manual_evidence_schema** — for manual bundles only: does the schema capture what an auditor would need to see? Y/N/n-a.

## Judgment calls surfaced for reviewer attention

The agent recorded the following deliberate authoring choices that diverge
from the most-literal reading of the TSC text. The reviewer should validate
or override each.

### 1. CC6.7 transmission protection — scoped to encryption-at-rest

**Choice:** The `soc2_cc6_7_encryption_in_transit` bundle's evidence query
targets `aws.s3.bucket_encryption_state` (encryption **at rest**), with the
description noting that prong (a) of CC6.7 — TLS-only public endpoints —
is covered by deployment-level controls outside this bundle.

**Alternative considered:** Author a separate bundle anchored to CRY-09
(Encryption At Rest) and re-anchor `soc2_cc6_7_encryption_in_transit` to a
TLS-config evidence_kind that doesn't yet exist (e.g. `aws.elb.tls_policy`).

**Rationale for the chosen path:** v1 evidence_kind registry does not
include a TLS-config kind (`internal/api/schemaregistry/schemas/` has no
`tls.policy.v1` or similar). Shipping a bundle that references an
unregistered kind would fail slice 014's validation. The at-rest evidence
is the strongest signal we can automate today; the document_classification
sibling control (`soc2_cc6_7_data_classification`) covers the policy
intent of CC6.7.

**Reviewer action:** confirm this scoping or instruct the agent to add a
separate `evidence_kind` schema (out-of-slice work) and re-anchor.

### 2. CC1.4 weak SCF anchor (HRS-04 strength 0.5)

**Choice:** Anchored `soc2_cc1_4_competence_training` to HRS-04 (Security
Awareness Training) — the slice-007 crosswalk records this mapping at
strength 0.5 with the rationale "Security awareness training covers part of
competence; broader HR competence is out of scope. LOW CONFIDENCE."

**Alternative considered:** Author CC1.4 as `manual_attested` covering the
broader competence concept (hiring rigor, performance reviews) rather than
the narrower training slice. Or omit CC1.4 entirely (with 43/43 mapped
TSC codes the threshold would still pass).

**Rationale for the chosen path:** Auditors test CC1.4 by sampling
training-completion evidence in practice — the broader "competence"
language in the TSC text resolves at audit-time to "did your people
complete annual security training". HRS-04 is the closest SCF concept and
the bundle's narrative is honest about the scoping.

**Reviewer action:** approve the narrow scoping, or request a second
manual_attested bundle for the broader concept.

### 3. CC6.3 has three bundles (split across access lifecycle)

**Choice:** CC6.3 ("authorizes, modifies, or removes access") is satisfied
by three bundles anchored to IAC-07 + IAC-15 + IAC-21 — covering
provisioning, periodic review, and privileged-account management.

**Rationale:** SOC 2 auditors sample CC6.3 along three axes (revocation
SLA, periodic review cadence, privileged-account vaulting); a single
bundle obscures which axis is failing when one is.

**Reviewer action:** approve the split, or request consolidation.

### 4. PI1 family heavily anchored to SEA-05 (Secure Software Development)

**Choice:** PI1.2, PI1.3, PI1.4 all anchor to SEA-05. The slice-007
crosswalk records these mappings at strength 0.4–0.5 with the note "PI is
weak in SCF coverage."

**Rationale:** The SCF catalog has no native processing-integrity concept;
SEA-05 is the closest match because processing integrity for a software
product manifests as application correctness, which secure development
controls assert. Bundles narrate this honestly.

**Reviewer action:** approve, or de-scope the PI1 family from v1 (would
drop coverage to 38/38 mapped codes, still 100% of the residual universe).

### 5. PRI-\* family deliberately omitted

**Choice:** The SCF catalog includes PRI-01 and PRI-04 (Privacy Governance,
Data Subject Rights). No bundle references them because SOC 2 v2017's
optional "Privacy" trust service criterion is **not** in scope for v1 (it
expands the audit substantially and most security-product startups elect
Security + Availability + Confidentiality only).

**Reviewer action:** confirm the omission. Privacy bundles ship in a later
slice if/when a customer's SOC 2 attestation includes the Privacy
criterion.

## Bundle inventory by TSC family (for cross-reference)

| TSC family | Bundles                                                               |
| ---------- | --------------------------------------------------------------------- |
| CC1 (5)    | cc1_1, cc1_2, cc1_3, cc1_4, cc1_5                                     |
| CC2 (3)    | cc2_1, cc2_2, cc2_3                                                   |
| CC3 (4)    | cc3_1, cc3_2, cc3_3, cc3_4                                            |
| CC4 (2)    | cc4_1, cc4_2                                                          |
| CC5 (3)    | cc5_1, cc5_2, cc5_3                                                   |
| CC6 (11)   | cc6_1, cc6_2, cc6_3 (×3), cc6_4, cc6_5, cc6_6 (×2), cc6_7 (×2), cc6_8 |
| CC7 (6)    | cc7_1 (×2), cc7_2, cc7_3, cc7_4, cc7_5                                |
| CC8 (2)    | cc8_1 (×2)                                                            |
| CC9 (3)    | cc9_1, cc9_2 (×2)                                                     |
| A1 (3)     | a1_1, a1_2, a1_3                                                      |
| C1 (2)     | c1_1, c1_2                                                            |
| PI1 (5)    | pi1_1, pi1_2, pi1_3, pi1_4, pi1_5                                     |
| **Total**  | **50**                                                                |

## Implementation-type distribution

| Type              | Count | Notes                                                              |
| ----------------- | ----- | ------------------------------------------------------------------ |
| `automated`       | 23    | Rego query over ledger; freshness ≤ daily                          |
| `semi_automated`  | 3     | Automated freshness check + manual_evidence_schema for human input |
| `manual_periodic` | 11    | Owner uploads evidence on cadence                                  |
| `manual_attested` | 13    | Roleholder asserts state digitally on cadence                      |

## Signoff procedure

1. Reviewer (Matt) opens this file, opens 1–2 bundles from the spot-check table at random, validates the four columns.
2. Reviewer fills in the spot-check table cells.
3. Reviewer either:
   - **Approves**: fills the "Decision" row above with `approve`, commits the updated review log, the PR can merge.
   - **Approves-with-fixes**: leaves the agent specific change requests in the "Reviewer notes" column for each affected bundle; the agent makes the fixes; reviewer re-checks the diff; updates to `approve`.
   - **Rejects**: the slice goes back to the agent for substantial rework with the failure reasons captured in this file.

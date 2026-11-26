# #2302 — Demo seed SCF anchors match their SCF 2026.3 titles: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

The demo seed's `demoSCFAnchors` (`internal/demoseed/writers.go`) synthesizes anchors for, or adopts anchors from, SCF `DemoSCFRelease` (2026.3). Several rows paired a code with a title that 2026.3 gives to a different code. When the seed adopts a loaded 2026.3 catalog, those rows bind to the wrong control. A review of #2282 caught it; nothing tested the pairs. Titles below are verbatim from `internal/api/scfseed/testdata/scf-pin.json`.

## D1 — Every `demoSCFAnchors` row, checked against the pin

Three of the five rows the ticket listed were already fixed by the family-group PRs: `CRY-05`→`CRY-07` (#1976), `IRO-04`→`IRO-06` (#1980), `TPM-03`→`TPM-04` (#1975 D4). The current state of all eight rows:

| Old code | New code | Demo title before                   | 2026.3 title of the old code                    | 2026.3 title of the new code (verbatim)      | Change      |
| -------- | -------- | ----------------------------------- | ----------------------------------------------- | -------------------------------------------- | ----------- |
| `IAC-37` | `IAC-37` | Multi-Factor Authentication         | Multi-Factor Authentication (MFA)               | Multi-Factor Authentication (MFA)            | title only  |
| `CRY-07` | `CRY-07` | Encrypting Data At Rest             | Encrypting Data At Rest                         | (same)                                       | none        |
| `BCD-24` | `BCD-24` | Data Backups                        | Data Backups                                    | (same)                                       | none        |
| `TDA-09` | `TDA-05` | Secure Coding                       | Product Tampering and Counterfeiting (PTC)      | Secure Software Development Practices (SSDP) | code, title |
| `TPM-04` | `TPM-04` | Supply Chain Risk Management (SCRM) | Supply Chain Risk Management (SCRM)             | (same)                                       | none        |
| `MON-02` | `MON-02` | Continuous Monitoring               | Continuous Monitoring                           | (same)                                       | none        |
| `VPM-02` | `VPM-06` | Vulnerability Remediation           | Vulnerability & Patch Management Program (VPMP) | Vulnerability Remediation Process            | code, title |
| `IRO-06` | `IRO-06` | Incident Handling                   | Incident Handling                               | (same)                                       | none        |

**`TDA-09` → `TDA-05`.** 2026.3 has no control titled "Secure Coding". The anchor stands for the secure-development family on the posture tile, and `TDA-05` Secure Software Development Practices (SSDP) is the parent control for that concept (its children are threat modeling, toolchain, design review). `TDA-17.1` Static Code Analysis is narrower; it suits the SAST question in D2 but not a family-level demo requirement. Confidence: high.

**`VPM-02` → `VPM-06`.** `VPM-06` Vulnerability Remediation Process is the title match. Confidence: high.

**`IAC-37`.** The code was right; the title dropped "(MFA)". It now carries the verbatim title so the pair test can be exact.

Both new codes are also in `migrations/fixtures/scf-sample.json`, so a sample-only install adopts them too.

## D2 — What hangs off the anchors

- **Demo controls.** `writeFrameworkPostureSpine` assigns controls to anchors by position (round-robin over the first six), not by code, so no control mapping is orphaned. The demo requirements (`DEMO-NN`) take the anchor's title, so they follow automatically. The anchor order is unchanged, so the same two requirements (`VPM-06`, `IRO-06`) stay the deliberate gaps.
- **Demo questionnaire** (`buildQuestionnaire`, `fixtures.go`). Anchors there are free-form `scf_id` strings and are chosen per question, not mirrored from the anchor list:
  - `AIS-04` "Is static analysis (SAST) run on every commit?" moves `TDA-09` → `TDA-17.1` Static Code Analysis, the exact concept.
  - `TPM-05` "Do you perform due-diligence reviews on critical vendors?" moves `TPM-03` (Acquisition Strategies, Tools & Methods) → `TPM-10` Third-Party Risk Assessments & Approvals. That is the vendor review concept; `TPM-04` SCRM is broader than a per-vendor review.
  - `BCR-03` "Do you test disaster recovery at least annually?" stays on `BCD-24` Data Backups, where #2282 put it; its narrative is about restore drills. Not re-chosen here.
- **Stock Vendor Management Policy** (`policies/stock/vendor-management-policy.md`) linked `TPM-03`, which it meant in the legacy sense (supply chain). It now links `TPM-04` Supply Chain Risk Management (SCRM), the same move #1975 D4 made for the demo row; `TPM-02` and `TPM-10` already cover the rest. `TPM-04` is not in the sample, so on a sample-only install the link is reported missing and dropped (the seed's links are best-effort), as `TPM-03` was not in the sample either. `GLOSSARY.md` follows.
- **Left as is:** ISO A.5.21 and NIST GV.SC supply-chain crosswalk edges land on `TPM-10`. `TPM-04` is arguably closer, but it is not in the sample, so moving them is a sample-catalog decision outside this ticket.

## D3 — The guard

`TestDemoSCFAnchors_MatchPinnedRelease` (`internal/demoseed/scfpin_test.go`) reads the pin, checks its release equals `DemoSCFRelease`, and fails on any `demoSCFAnchors` row whose code is absent or whose title differs. Run against the pre-change rows it failed on `IAC-37`, `TDA-09` and `VPM-02`. `TestDemoQuestionnaireAnchors_InPinnedRelease` checks every anchored demo question names a code in the pin; question anchors carry no title, so that check is existence only.

## D4 — Which log held the stale wording

The ticket names "the 1717 decisions log D2". That D2 is the ratchet decision and does not mention the demo seed. The stale code list is in `682-demo-posture-decisions.md` D1 option (b), which listed the original legacy codes (`IAC-06`, `CRY-05`, `BCD-11`, `TDA-09`, `TPM-03`, `MON-01`, `VPM-02`, `IRO-04`). That passage gains a note with the current set and a pointer here; the original list is kept as the record of what 682 shipped.

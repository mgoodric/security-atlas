# Issue 1717: sample SCF anchors pinned to SCF 2026.3 (ratchet): decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/1717
**Guard:** `internal/api/scfseed/scfpin_test.go` (`TestSCFSampleMatchesPinnedRelease`)
**Pin:** `internal/api/scfseed/testdata/scf-pin.json`

- **`detection_tier_actual`: `manual_review`.** Found by hand during a first-run adopter setup (2026-11-12), by tracing sample IDs through the real workbook.
- **`detection_tier_target`: `unit`.** A check of the fixture against a pinned release is a pure-Go comparison with no services. That check is what this change adds.

## The defect

`migrations/fixtures/scf-sample.json` presents 77 rows as SCF anchors. In SCF 2026.3, only 4 of them name the real control with that ID and title: `IAC-01`, `AST-01`, `RSK-08` and `VPM-01`. Three IDs (the `AAA` family) don't exist in any SCF release. The other 70 point at a different control or a renamed one. For example, the sample's `CRY-04` "Encryption At Rest" is "Integrity of Data In Transit" in 2026.3.

The stock SOC 2 controls, the five bundled crosswalks, the connector default anchors and the tests all inherit those anchors: 2,280 references across 448 files outside `docs/issues` and `docs/audit-log`.

## Decisions

### D1: pin SCF 2026.3. **Decided by:** Matt, 2026-11-13.

2026.3 renumbered most of the catalog and added a `Legacy SCF #` column. 2026.2 was the last release on the old numbering.

2026.3 was chosen anyway because it is what an adopter downloads today. A sample pinned to 2026.2 would collide with it: 2026.3 reuses old IDs for different controls (`GOV-01` is now the governance _policy_, and the program moved to `GOV-02`).

The pinned workbook:

| Field   | Value                                                                                                                  |
| ------- | ---------------------------------------------------------------------------------------------------------------------- |
| Release | SCF 2026.3, sheet `SCF 2026.3`, 1,591 controls                                                                         |
| Source  | https://github.com/securecontrolsframework/securecontrolsframework/blob/main/secure-controls-framework-scf-2026-3.xlsx |
| sha256  | `5a89bf2d3c106a9a87d4b6e3d62dd3e147d0e960d4c07473045a10aa8a7df697`                                                     |
| Fetched | 2026-11-13                                                                                                             |

### D2: ratchet, not one atomic PR. **Decided by:** Matt, 2026-11-13.

The guard ships with a `knownMismatch` allowlist that waives the 73 rows mismatching today. It fails in three cases:

- a new mismatch;
- a waived row whose title was edited without being fixed;
- a waived row that now matches (a stale entry).

So the list can only shrink. The per-family follow-ups below each remap their rows across the repo and delete the matching entries. This is the same shape as `KNOWN_UNENROLLED` in `scripts/audit-integration-enrolment.sh`.

### D3: licensing. Vendor IDs and titles only, with attribution. **Confidence: HIGH on the license, MEDIUM on the policy reading.**

- The SCF is licensed under **CC BY-ND 4.0**. Sources: https://securecontrolsframework.com/terms-conditions/ (SCF EULA) and the upstream repo README ("free via Creative Commons licensing"). The workbook itself carries no license text.
- CC BY-ND 4.0 §2(a)(1)(A) permits reproducing and sharing the licensed material "in whole or in part". The ND restriction applies to _Adapted Material_, so a verbatim, unmodified excerpt is permitted, with attribution under §3(a).
- The repo's own policy is stricter: `docs/RELEASE_READINESS.md` OQ #1 says the project "does NOT bundle pre-built SCF data in release artifacts".
- So the pin carries **only `scf_id` and `title`**, verbatim and unmodified, which is the minimum the guard needs. Descriptions and every other column are left out.
- The file lives under a Go `testdata/` directory, so it is not in any built artifact: no container image, binary or release bundle carries it. It does travel in the GitHub source archives and the Go module zip. That is fine under CC BY-ND 4.0, because the excerpt is verbatim and attributed.
- It carries the attribution, the license link, the source URL and the sha256 in its header.

Left open, and flagged for Matt: the issue's fix says to regenerate the _sample_ with titles verbatim. The sample is seeded by the docker bootstrap (`deploy/docker/bootstrap/bootstrap.sh`), so verbatim SCF titles there arguably are bundled SCF data under OQ #1. The sample already ships SCF-shaped titles today, so this PR doesn't change that exposure. The follow-ups should keep sample descriptions as our own paraphrase rather than SCF text, or OQ #1 should be revisited explicitly.

### D4: the guard lives in `internal/api/scfseed` as a unit test. **Confidence: HIGH.**

`scfseed` already owns the sample's path. A plain `go test` file runs in the required `Go · build + test` check with no new CI job, no new package and no coverage-floor row.

The pin is regenerated by the same file: `SCF_WORKBOOK=<xlsx> go test -run TestRegenerateSCFPin ./internal/api/scfseed/`. The test skips when the env var is unset, and refuses a workbook whose sha256 differs from the pinned one. It uses `excelize`, which is already a module dependency.

### D5: the concept-to-2026.3 remap. **Confidence: per row, below.**

Each sample row is mapped to the 2026.3 control that carries the _concept the sample meant_: its title, its description and how the repo uses it. That is not always the control the sample's ID points at. The follow-ups apply this table.

How to read the Basis column:

- **exact:** the ID and title already match 2026.3. No change.
- **legacy:** the 2026.3 row whose `Legacy SCF #` is the sample ID carries the sample's concept. It is a rename or renumber.
- **concept:** the sample ID points at an unrelated control, and the target was found by title and description.
- **low:** a judgment call with no clean SCF equivalent. A reviewer should check these five rows.

| Sample ID | Sample title                                       | 2026.3 ID  | 2026.3 title (verbatim)                                               | Basis   | Note                                                                                                                                              |
| --------- | -------------------------------------------------- | ---------- | --------------------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `IAC-01`  | Identification & Authentication Policy             | `IAC-01`   | Identification & Authentication Policy                                | exact   | 2026.3 added a dedicated policy control with this exact title                                                                                     |
| `IAC-06`  | Multi-Factor Authentication (MFA)                  | `IAC-37`   | Multi-Factor Authentication (MFA)                                     | legacy  |                                                                                                                                                   |
| `IAC-07`  | User Provisioning & Lifecycle                      | `IAC-04`   | User Provisioning & De-Provisioning                                   | legacy  |                                                                                                                                                   |
| `IAC-10`  | Authenticator Management                           | `IAC-14`   | Authenticator Management                                              | legacy  |                                                                                                                                                   |
| `IAC-15`  | Account Review                                     | `IAC-12`   | Periodic Review of Individual & Service Account Privileges            | concept | sample concept is periodic access review; legacy IAC-15 is Account Management (IAC-08)                                                            |
| `IAC-21`  | Privileged Account Management                      | `IAC-10`   | Privileged Account Management (PAM)                                   | concept | sample concept is PAM; legacy IAC-21 is Least Privilege (IAC-30)                                                                                  |
| `IAC-24`  | Emergency Access (Break-Glass) Accounts            | `IAC-11`   | Emergency Accounts                                                    | concept | sample concept is break-glass; legacy IAC-24 is Session Lock                                                                                      |
| `IAC-25`  | Session Termination                                | `CFG-04.5` | Session Termination                                                   | legacy  | moved out of IAC into CFG                                                                                                                         |
| `AAA-01`  | Auditing & Accountability                          | `MON-04`   | System Generated Event Logs & Security-Relevant Telemetry             | low     | no AAA family in SCF; System Generated Event Logs is the nearest auditing anchor                                                                  |
| `AAA-10`  | Event Log Retention                                | `MON-13`   | Event Log & Security-Relevant Telemetry Retention                     | concept | no AAA family in SCF                                                                                                                              |
| `AAA-12`  | Audit Log Review                                   | `MON-05.1` | Centralized Event Log & Security-Relevant Telemetry Review & Analysis | concept | no AAA family in SCF                                                                                                                              |
| `AST-01`  | Asset Management Policy                            | `AST-01`   | Asset Management Policy                                               | exact   | 2026.3 added a dedicated policy control with this exact title                                                                                     |
| `AST-03`  | Asset Inventory                                    | `AST-04`   | Asset Inventories                                                     | concept | sample concept is inventory; legacy AST-03 is Ownership Assignment                                                                                |
| `AST-09`  | Asset Disposal                                     | `AST-22`   | Secure Disposal, Destruction or Re-Use of Equipment                   | legacy  | 2026.3 legacy cell lists AST-09 and TDA-11.2                                                                                                      |
| `BCD-02`  | Business Continuity Plan                           | `BCD-04`   | Business Continuity & Disaster Recovery (BC/DR) Plans                 | concept | sample concept is the BC plan; legacy BCD-02 is Identify Critical Assets                                                                          |
| `BCD-09`  | Backups                                            | `BCD-24`   | Data Backups                                                          | concept | sample concept is backups; legacy BCD-09 is Alternate Processing Site                                                                             |
| `BCD-11`  | Backup Testing                                     | `BCD-32`   | Testing for Reliability & Integrity                                   | concept | sample concept is backup testing; legacy BCD-11 is Data Backups (BCD-24)                                                                          |
| `CFG-02`  | Secure Baseline Configurations                     | `CFG-04`   | Secure Baseline Configurations                                        | legacy  |                                                                                                                                                   |
| `CFG-04`  | Configuration Change Control                       | `CHG-03`   | Configuration Change Control                                          | concept | sample concept is configuration change control; legacy CFG-04 is Software Usage Restrictions                                                      |
| `CHG-02`  | Change Control Process                             | `CHG-02`   | Change Management Program                                             | low     | sample concept is the change control process; 2026.3 CHG-02 is Change Management Program. Collides by ID with a different legacy control (CHG-01) |
| `CLD-01`  | Cloud Infrastructure Governance                    | `CLD-02`   | Cloud Services                                                        | legacy  |                                                                                                                                                   |
| `CLD-09`  | Cloud Service Provider Risk Management             | `TPM-10`   | Third-Party Risk Assessments & Approvals                              | low     | no cloud-provider-risk control in SCF; third-party risk assessment is the nearest                                                                 |
| `CPL-01`  | Compliance Management                              | `CPL-02`   | Statutory, Regulatory & Contractual Compliance                        | legacy  |                                                                                                                                                   |
| `CPL-03`  | Internal Audit Function                            | `CPL-07.1` | Internal Audit Function                                               | concept | sample concept is internal audit; legacy CPL-03 is Conformity Monitoring (CPL-05)                                                                 |
| `CRY-01`  | Use of Cryptographic Controls                      | `CRY-02`   | Use of Cryptographic Controls                                         | legacy  |                                                                                                                                                   |
| `CRY-04`  | Encryption At Rest                                 | `CRY-07`   | Encrypting Data At Rest                                               | concept | sample concept is encryption at rest; legacy CRY-04 is Integrity of Data In Transit                                                               |
| `CRY-08`  | Encryption In Transit                              | `CRY-05`   | Encrypting Data In Transit                                            | concept | sample concept is encryption in transit; legacy CRY-08 is PKI                                                                                     |
| `CRY-09`  | Cryptographic Key Management                       | `CRY-10`   | Cryptographic Key Management                                          | legacy  |                                                                                                                                                   |
| `CRY-11`  | Cryptographic Integrity Verification               | `CRY-14`   | Cryptographic Hash                                                    | low     | sample concept is hash/signature integrity; Cryptographic Hash is the nearest                                                                     |
| `CRY-12`  | Transmission Integrity                             | `CRY-06`   | Integrity of Data In Transit                                          | concept | sample concept is transmission integrity                                                                                                          |
| `DCH-01`  | Data Classification & Handling                     | `DCH-04`   | Data & Asset Classification                                           | concept | sample concept is classification; legacy DCH-01 is Data Protection (DCH-02)                                                                       |
| `DCH-03`  | Data Retention                                     | `DCH-27`   | Media & Data Retention                                                | concept | sample concept is retention; legacy DCH-03 is Media Access                                                                                        |
| `DCH-05`  | Data Integrity                                     | `MON-27`   | File Integrity Monitoring (FIM)                                       | low     | sample concept is detecting unauthorized modification of stored data; File Integrity Monitoring is the nearest                                    |
| `DCH-07`  | Media Transportation & Custody                     | `DCH-16`   | Media Transportation                                                  | legacy  |                                                                                                                                                   |
| `END-04`  | Endpoint Security                                  | `END-03`   | Endpoint Protection Mechanisms                                        | concept | sample concept is endpoint security broadly; 2026.3 END-04 is Antimalware                                                                         |
| `END-07`  | Malicious Code Protection                          | `END-04`   | Malicious Code Protection (Antimalware)                               | concept | sample concept is malicious code protection                                                                                                       |
| `GOV-01`  | Information Security Governance                    | `GOV-02`   | Security, Compliance & Resilience Program (SCRP)                      | legacy  |                                                                                                                                                   |
| `GOV-02`  | Publishing Security Documentation                  | `GOV-04`   | Publishing Security, Compliance & Resilience Documentation            | legacy  |                                                                                                                                                   |
| `GOV-03`  | Periodic Review & Update of Security Documentation | `GOV-04.1` | Periodic Review & Update of Security, Compliance & Resilience Program | legacy  |                                                                                                                                                   |
| `GOV-04`  | Information Security Roles & Responsibilities      | `GOV-05`   | Assigned Security, Compliance & Resilience Responsibilities           | legacy  |                                                                                                                                                   |
| `HRS-01`  | Human Resources Security                           | `HRS-02`   | Human Resources Security Management                                   | legacy  |                                                                                                                                                   |
| `HRS-04`  | Security Awareness Training                        | `SAT-04`   | Security, Compliance & Resilience Awareness Training                  | concept | sample concept is awareness training; legacy HRS-04 is Personnel Screening                                                                        |
| `HRS-07`  | Personnel Sanctions                                | `HRS-10`   | Personnel Sanctions                                                   | legacy  |                                                                                                                                                   |
| `HRS-09`  | Personnel Termination Process                      | `HRS-12`   | Personnel Termination                                                 | legacy  |                                                                                                                                                   |
| `HRS-12`  | Rules of Behavior (Acceptable Use)                 | `HRS-06.1` | Rules of Behavior                                                     | concept | sample concept is rules of behavior; legacy HRS-12 is Incompatible Roles                                                                          |
| `IRO-04`  | Incident Response Plan                             | `IRO-08`   | Incident Response Plan (IRP)                                          | legacy  |                                                                                                                                                   |
| `IRO-09`  | Incident Reporting                                 | `IRO-16`   | Incident Stakeholder Reporting                                        | concept | sample concept is incident reporting; legacy IRO-09 is Situational Awareness                                                                      |
| `MNT-02`  | Controlled Maintenance                             | `MNT-03`   | Controlled Maintenance                                                | legacy  |                                                                                                                                                   |
| `MON-01`  | Continuous Monitoring                              | `MON-02`   | Continuous Monitoring                                                 | legacy  |                                                                                                                                                   |
| `MON-08`  | Anomalous Behavior Detection                       | `MON-16`   | Anomalous Behavior                                                    | concept | sample concept is anomalous behavior; legacy MON-08 is Protection of Event Logs                                                                   |
| `NET-01`  | Network Security                                   | `NET-02`   | Network Security Controls (NSC)                                       | legacy  |                                                                                                                                                   |
| `NET-04`  | Boundary Protection                                | `NET-03`   | Boundary Protection                                                   | concept | sample concept is boundary protection; legacy NET-04 is ACL data flow enforcement                                                                 |
| `NET-06`  | Network Segmentation                               | `NET-06`   | Network Segmentation (macrosegmentation)                              | legacy  | title is now Network Segmentation (macrosegmentation)                                                                                             |
| `NET-08`  | Wireless Network Security                          | `NET-24`   | Wireless Networking                                                   | concept | sample concept is wireless; legacy NET-08 is NIDS/NIPS                                                                                            |
| `PES-04`  | Physical Access Control                            | `PES-08`   | Physical Access Control                                               | concept | sample concept is physical access control                                                                                                         |
| `PRI-01`  | Privacy Governance                                 | `PRI-02`   | Data Privacy Program                                                  | legacy  |                                                                                                                                                   |
| `PRI-04`  | Data Subject Rights                                | `PRI-39`   | Data Subject Rights Management                                        | concept | sample concept is data subject rights                                                                                                             |
| `RSK-01`  | Risk Management Program                            | `RSK-02`   | Risk Management Program                                               | legacy  |                                                                                                                                                   |
| `RSK-04`  | Risk Assessment                                    | `RSK-13`   | Risk Assessment                                                       | legacy  |                                                                                                                                                   |
| `RSK-08`  | Business Impact Analysis (BIA)                     | `RSK-08`   | Business Impact Analysis (BIA)                                        | exact   |                                                                                                                                                   |
| `SEA-01`  | Secure Engineering Principles                      | `SEA-05`   | Secure Engineering Principles                                         | legacy  |                                                                                                                                                   |
| `SEA-05`  | Secure Software Development                        | `TDA-05`   | Secure Software Development Practices (SSDP)                          | concept | sample concept is secure software development; legacy SEA-05 is Information In Shared Resources                                                   |
| `TDA-01`  | Technology Development & Acquisition               | `TDA-02`   | Technology Development & Acquisition                                  | legacy  |                                                                                                                                                   |
| `THR-01`  | Threat Intelligence Program                        | `THR-02`   | Threat Intelligence Program                                           | legacy  |                                                                                                                                                   |
| `THR-02`  | Indicators of Exposure (IOE)                       | `THR-11`   | Indicators of Exposure (IOE)                                          | legacy  |                                                                                                                                                   |
| `THR-03`  | Threat Intelligence Feeds                          | `THR-04`   | Threat Intelligence Feeds                                             | legacy  |                                                                                                                                                   |
| `THR-04`  | Threat Hunting                                     | `THR-13`   | Threat Hunting                                                        | concept | sample concept is threat hunting; legacy THR-04 is Insider Threat Program (THR-15)                                                                |
| `THR-05`  | Insider Threat Program                             | `THR-15`   | Insider Threat Program                                                | concept | sample concept is insider threat program                                                                                                          |
| `THR-06`  | Insider Threat Awareness                           | `THR-16`   | Insider Threat Awareness                                              | concept | sample concept is insider threat awareness                                                                                                        |
| `THR-07`  | Vulnerability Disclosure Program (VDP)             | `THR-12`   | Vulnerability Disclosure Program (VDP)                                | concept | sample concept is VDP                                                                                                                             |
| `THR-09`  | Threat Catalog                                     | `THR-06`   | Threat Catalog                                                        | legacy  |                                                                                                                                                   |
| `THR-10`  | Threat Analysis                                    | `THR-08`   | Threat Analysis                                                       | legacy  |                                                                                                                                                   |
| `TPM-01`  | Third-Party Management                             | `TPM-02`   | Third-Party Management                                                | legacy  |                                                                                                                                                   |
| `TPM-04`  | Third-Party Risk Assessments                       | `TPM-10`   | Third-Party Risk Assessments & Approvals                              | concept | sample concept is third-party risk assessment; legacy TPM-04 is Third-Party Services                                                              |
| `TPM-05`  | Third-Party Contract Requirements                  | `TPM-14`   | Third-Party Contract Requirements                                     | legacy  |                                                                                                                                                   |
| `VPM-01`  | Vulnerability & Patch Management Policy            | `VPM-01`   | Vulnerability & Patch Management Policy                               | exact   | 2026.3 added a dedicated policy control with this exact title                                                                                     |
| `VPM-04`  | Vulnerability Remediation Process                  | `VPM-06`   | Vulnerability Remediation Process                                     | concept | sample concept is the remediation process; legacy VPM-04 is Continuous Remediation Activities                                                     |

Hazards for the follow-ups:

- **Permutations.** Several targets are themselves sample IDs: `IAC-21`→`IAC-10` while `IAC-10`→`IAC-14`, and similarly for `CFG-02`/`CFG-04`, `END-07`/`END-04`, `GOV-01`/`02`/`04`, `HRS-09`/`HRS-12`, `SEA-01`/`SEA-05`, and `THR-01`/`03`/`09`. Apply each group's substitution simultaneously, never one `sed` per ID. The grouping below keeps every such chain inside one group: zero cross-group collisions, checked mechanically.
- **Merge.** `CLD-09` and `TPM-04` both map to `TPM-10`. The CLD+CPL+TPM follow-up merges the two sample rows, or picks a different anchor for `CLD-09`.
- **Prose and string references.** Crosswalk rationales, CHANGELOG lines and test names quote the old IDs. CHANGELOG and `docs/audit-log/` are history and stay as they are.

## Follow-up tickets (one per family group)

Each ticket covers the following for its group:

1. Regenerate the group's sample rows: `scf_id` and title verbatim from the pin, with the description in our own words per D3.
2. Re-anchor every reference: `controls/soc2/*`, `data/crosswalks/*.yaml`, connector default anchors, `internal/**` code and tests, `web/e2e`, `isa/probes` and `oscal-bridge/tests`.
3. Delete the group's `knownMismatch` entries.

| Group                 | Sample IDs                              | Refs | Notes                                                              |
| --------------------- | --------------------------------------- | ---- | ------------------------------------------------------------------ |
| IAC                   | IAC-01 06 07 10 15 21 24 25             | 745  | IAC-06 (MFA) alone has 455 refs; IAC-21/IAC-10 chain               |
| AAA + MON             | AAA-01 10 12, MON-01 08                 | 140  | AAA has no SCF family; AAA-01 is a low-confidence pick             |
| AST + BCD             | AST-01 03 09, BCD-02 09 11              | 86   |                                                                    |
| CFG + CHG             | CFG-02 04, CHG-02                       | 157  | CFG-02/CFG-04 chain; CHG-02 is a low-confidence pick               |
| CLD + CPL + TPM       | CLD-01 09, CPL-01 03, TPM-01 04 05      | 85   | CLD-09/TPM-04 both map to TPM-10                                   |
| CRY                   | CRY-01 04 08 09 11 12                   | 148  | CRY-11 is a low-confidence pick                                    |
| DCH                   | DCH-01 03 05 07                         | 65   | DCH-05 is a low-confidence pick                                    |
| END + NET + PES       | END-04 07, NET-01 04 06 08, PES-04      | 192  | END-07/END-04 chain                                                |
| GOV + HRS             | GOV-01 02 03 04, HRS-01 04 07 09 12     | 212  | GOV and HRS chains                                                 |
| IRO + MNT + RSK       | IRO-04 09, MNT-02, RSK-01 04 08         | 160  | RSK-08 already matches                                             |
| PRI + SEA + TDA + VPM | PRI-01 04, SEA-01 05, TDA-01, VPM-01 04 | 106  | SEA-01/SEA-05 chain; VPM-01 already matches                        |
| THR                   | THR-01 02 03 04 05 06 07 09 10          | 184  | THR chain; also re-check the THR anchor seeds (slices 635/641/651) |

Out of scope here, and worth a separate look: SCF IDs the repo cites that are _not_ sample rows (the finer anchors in the full-catalog seed path, slice 754/567). They carry the same old-numbering risk against 2026.3.

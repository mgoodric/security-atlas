# #1753 — SOC 2 Privacy criteria (P1.1-P8.1) in the bundled crosswalk: decisions log

detection_tier_actual: unit
detection_tier_target: unit

`data/crosswalks/soc2-tsc-2017.yaml` gains the 18 Privacy criteria, bringing it to 61 (33 CC, 3 A, 2 C, 5 PI, 18 P). The SCF sample gains the 17 anchors they need. The only failure seen was expected: `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label `test-2026.3` was re-pinned to the hash it printed.

## D1 — Licensing

AICPA SOC 2 text is never quoted (`data/crosswalks/README.md`, #2404; #1732 closed on that policy). Each P row carries the AICPA identifier, a short title of our own and a one-line body of our own. Bodies were written from the concept each criterion covers (notice, consent, collection, use, retention, disposal, access, correction, disclosure, breach records, vendor duties, accounting, quality, complaints and monitoring), not from TSC wording, and use different vocabulary and sentence shape from it. The file header now says the P rows follow the existing posture and names this log.

## D2 — Anchors

The candidates in the issue's prep comment were used where they fit. Titles in the table are verbatim from the 2026.3 pin.

| Criterion | Anchor(s) (strength)                                                                                                       | Confidence |
| --------- | -------------------------------------------------------------------------------------------------------------------------- | ---------- |
| P1.1      | PRI-21 Data Privacy Notice (0.9)                                                                                           | high       |
| P2.1      | PRI-24 Data Subject Consent (0.9); PRI-27 Data Subject Opt-In Consent (0.6)                                                | high       |
| P3.1      | PRI-31 Personal Data (PD) Collection Methods (0.8); PRI-12 Authority To Collect, Process, Store & Share PD (0.7)           | high       |
| P3.2      | PRI-27 Data Subject Opt-In Consent (0.8)                                                                                   | high       |
| P4.1      | PRI-29 Usage Restrictions of Personal Data (PD) (0.9)                                                                      | high       |
| P4.2      | PRI-30 Personal Data (PD) Retention & Disposal (0.8)                                                                       | high       |
| P4.3      | PRI-30 (0.8); AST-22 Secure Disposal, Destruction or Re-Use of Equipment (0.5)                                             | high / low |
| P5.1      | PRI-39 Data Subject Rights Management (0.8); PRI-38 Data Subject Authentication (0.6)                                      | high       |
| P5.2      | PRI-39 (0.7); PRI-08 Updating Personal Data (PD) Process (0.7)                                                             | high       |
| P6.1      | PRI-16 Information Sharing With Third Parties (0.8); PRI-17 Limiting Personal Data (PD) Disclosures (0.7)                  | high       |
| P6.2      | PRI-17.1 Accounting of Disclosures (0.9)                                                                                   | high       |
| P6.3      | IRO-22 Data Breach (0.6)                                                                                                   | low        |
| P6.4      | PRI-14 Data Privacy Requirements for Contractors & Service Providers (0.9); TPM-14 Third-Party Contract Requirements (0.6) | high       |
| P6.5      | PRI-14 (0.6); TPM-14 (0.5)                                                                                                 | medium     |
| P6.6      | IRO-22 Data Breach (0.8)                                                                                                   | high       |
| P6.7      | PRI-17.1 Accounting of Disclosures (0.8)                                                                                   | high       |
| P7.1      | PRI-35 Personal Data (PD) Accuracy & Integrity (0.9); PRI-08 (0.6)                                                         | high       |
| P8.1      | PRI-11 Personal Data (PD) Control Testing, Training & Monitoring (0.6); PRI-37 Data Subject Communications (0.5)           | low        |

Departures from the prep comment's list:

- **PRI-17.1** (Accounting of Disclosures) for P6.2 and P6.7. It names the record both criteria are about; the comment's PRI-16/17 cover disclosure itself, so they stay on P6.1.
- **IRO-22** (Data Breach) for P6.3 and P6.6. No PRI control covers breach records or breach notice; IRO-22 is the 2026.3 control for both. IRO-16 (Incident Stakeholder Reporting, already in the sample) is the fallback for P6.6 if IRO-22 is not wanted.
- **AST-22** as a secondary for P4.3: already in the sample, covers media disposal.
- **TPM-14** as a secondary for P6.4 and P6.5: already in the sample, the contractual vehicle.

The low-confidence calls are P6.3 (recording a breach is a narrow part of breach handling) and P8.1 (SCF has no single control for privacy complaints plus compliance monitoring; PRI-11 and PRI-37 each cover half). Their rationales carry `LOW CONFIDENCE — review`, the file's existing convention.

**Matt's decision (2026-11-16, relayed by the dispatcher):** P6.3 → IRO-22 (0.6), and P8.1 → PRI-11 (0.6) + PRI-37 (0.5), keeping both. These are Matt's picks, not the agent's. The other departures above (PRI-17.1, AST-22, TPM-14) were confirmed as well. The Privacy control kit (D5) is a follow-up Matt is filing.

## D3 — Sample rows

17 rows are added to `migrations/fixtures/scf-sample.json`: PRI-08, PRI-11, PRI-12, PRI-14, PRI-16, PRI-17, PRI-17.1, PRI-21, PRI-24, PRI-27, PRI-29, PRI-30, PRI-31, PRI-35, PRI-37, PRI-38 and IRO-22. Titles are verbatim from the pin (`TestSCFSampleMatchesPinnedRelease` passes with no new `knownMismatch` entry; the ceiling stays at 9). Descriptions are our own, in the sample's "Mechanisms exist to ..." form; the pin carries no SCF descriptions to copy. The sample stays on the open working label `test-2026.3`, so the release-label rule in `fixturepin_test.go` allows the re-pin.

## D4 — Reaching existing installs

The bootstrap re-imports the bundled crosswalk on every boot (#1884) into framework version `2017`, which is frozen (`current`). The immutability trigger (`enforce_framework_requirement_immutability`) fires on UPDATE only. The importer inserts a requirement code it has not seen, so the 18 P rows and their 28 edges are added to the existing `2017` version; the 43 stored rows are untouched and none is reported as "requirement kept" (no title or body changed). `requirement_count` becomes 61.

Not a new `framework_version`: the P criteria were always part of TSC 2017, nothing already stored changes, and a new version would push every tenant through the version-migration review queue for a pure addition.

**Effect on tenants:** SOC 2 coverage denominators grow by 18. A tenant whose SOC 2 scope excludes Privacy sees its SOC 2 coverage percentage fall until it marks P1.1-P8.1 `excluded` in its framework scope's Statement of Applicability; the dashboard leaves excluded requirements out of the denominator (`internal/db/queries/dashboard.sql`). The changelog fragment says so.

Test: `TestImport_FrozenSOC2VersionGainsPrivacyCriteria` imports the bundled file without the P rows, confirms the version is `current`, re-imports the full file into it, and asserts 18 created / 43 unchanged / 0 updated / 0 differed, the 28 new edges, and `requirement_count = 61`.

## D5 — Stock controls: none added

No stock control under `controls/soc2/` anchors to the new PRI rows, so `go run ./cmd/scripts/coverage-check` (not wired into CI) now reports 44 / 61 TSC criteria reachable from a stock bundle, below its 80% gate (it was 43 / 43). A Privacy control kit is a separate piece of work; the ticket scopes the crosswalk only.

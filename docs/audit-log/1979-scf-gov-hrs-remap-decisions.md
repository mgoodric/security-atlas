# #1979 — SCF 2026.3 remap, GOV + HRS family group: decisions log

detection_tier_actual: integration
detection_tier_target: unit

The ninth #1717 family group. It uses the same mechanics as #1976, #1977 and #1978 (`1978-scf-end-net-pes-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration. The failures seen were:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, as expected, so the open label's hash was updated.
- `TestSCFSampleMatchesPinnedRelease` flagged the nine `knownMismatch` entries as stale, as expected.
- `TestStockManifestHashes_CoverControlsDir` flagged the nine re-anchored stock controls, as expected, so `scripts/gen-stock-control-hashes.sh` appended their hashes.
- Not expected: the first integration run failed in tests built on the synthetic `scfreleasetest` releases A and B, and in `TestImport_SCFWorkbookCreatesCommunityDraftEdges`. D2 covers both. Unit tests could not see either, so the target tier is recorded as `unit`: the release-fixture tests could reject a code that is missing from their release before any database is involved.

## D1 — The remap

| Sample ID | 2026.3 ID  | 2026.3 title (verbatim)                                               | Basis in `remaps.json` |
| --------- | ---------- | --------------------------------------------------------------------- | ---------------------- |
| `GOV-01`  | `GOV-02`   | Security, Compliance & Resilience Program (SCRP)                      | legacy                 |
| `GOV-02`  | `GOV-04`   | Publishing Security, Compliance & Resilience Documentation            | legacy                 |
| `GOV-03`  | `GOV-04.1` | Periodic Review & Update of Security, Compliance & Resilience Program | legacy                 |
| `GOV-04`  | `GOV-05`   | Assigned Security, Compliance & Resilience Responsibilities           | legacy                 |
| `HRS-01`  | `HRS-02`   | Human Resources Security Management                                   | legacy                 |
| `HRS-04`  | `SAT-04`   | Security, Compliance & Resilience Awareness Training                  | concept                |
| `HRS-07`  | `HRS-10`   | Personnel Sanctions                                                   | legacy                 |
| `HRS-09`  | `HRS-12`   | Personnel Termination                                                 | legacy                 |
| `HRS-12`  | `HRS-06.1` | Rules of Behavior                                                     | concept                |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`, and none of them is a low-confidence pick there. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words and are unchanged, because each one still describes its 2026.3 title.

Two layout changes in the sample:

- `SAT-04` takes family `SAT`, its 2026.3 family, and moves out of the HRS block to its alphabetical place after `RSK`.
- The HRS block is re-sorted by code (`HRS-02`, `HRS-06.1`, `HRS-10`, `HRS-12`), because `HRS-06.1` would otherwise sort after `HRS-12`.

Nothing reads the row order or a per-family count. This is the first group whose move crosses families.

## D2 — One substitution over two chains

The GOV chain is three deep (`GOV-01`→`GOV-02`→`GOV-04`→`GOV-05`), plus `GOV-03`→`GOV-04.1`. HRS has `HRS-09`→`HRS-12`→`HRS-06.1`. All nine moves were applied as one Perl `s///g` pass that maps each match through a lookup table, never one `sed` per ID. A single pass never re-reads its own output, so an old `GOV-01` becomes `GOV-02` and stops there. The bounded pattern is `(?<![A-Za-z0-9.-])(GOV-0[1-4]|HRS-0[1479]|HRS-12)(?![0-9]|\.[0-9])`. Its lookahead rejects any `GOV-04` followed by `.<digit>`, so a new `GOV-04.1` cannot be taken by the `GOV-04`→`GOV-05` rule even in a second run. The existing dotted children (`HRS-07.1` in the mapping-overlay tests, `GOV-03.1` in `anchor_coverage_test.go`) are synthetic and were left alone.

**Each reference landed exactly once.** A check script compared every changed file against `HEAD`. It took every SCF-shaped token in the old file, applied the table, and required the result to equal the token sequence in the new file, in order. All 66 substituted files matched right after the pass. Four of them were then restored to `HEAD` as synthetic (see below). Eight were hand-edited on purpose afterwards, so they differ from the pure permutation:

- the sample: titles, `SAT` family, order;
- the two lifecycle kinds and two READMEs (D3);
- the historical error string restored in `scfseed.go` and `scfseed_integration_test.go`;
- the release-A cases restored in `seed_integration_test.go`.

The remaining 54 still match the permutation exactly.

**Target codes checked before the change:** `GOV-05`, `GOV-04.1`, `HRS-10`, `SAT-04` and `HRS-06.1` appeared nowhere outside `scf-pin.json`. `HRS-02` appears only in `docs-site/docs/framework-setup.md`, as prose ("nearby governance and HR controls (GOV-09, HRS-02)"). That still fits its 2026.3 meaning, so it was left. `HRS-06` in `web/e2e/audited-under.spec.ts` is a mocked "Confidentiality agreements" anchor, distinct from `HRS-06.1`. Nothing needed moving first.

**Out of the pass: synthetic examples that keep their meaning.** These are not sample references:

- the `scfreleasetest` releases A and B and every test built on them: `internal/scfrelease/**`, `internal/control/scf_release_rebind_integration_test.go`, `scf_rebind_record_integration_test.go`, `internal/api/mappingoverlays/audited_under_integration_test.go`, `internal/api/ucfcoverage/release_default_integration_test.go`, the release-A cases in `internal/policy/seed/seed_integration_test.go`, and `cmd/atlas-cli/cmd_controls_validate_test.go`. Release A holds `GOV-01` and not `GOV-02`. The first integration run caught the mapping-overlay and coverage tests, which were then restored.
- in-memory workbooks and crosswalk YAML that only exercise parsing: `internal/api/scfimport/workbook_test.go`, and in `internal/api/soc2import/` the files `scf_release_test.go`, `titles_test.go`, `points_of_focus_test.go` and `points_of_focus_helpers_test.go`. `scf_workbook_test.go` is the exception. Its `writeWideSCFWorkbook` feeds `TestImport_SCFWorkbookCreatesCommunityDraftEdges`, which imports against the sample, so it moved. The first integration run caught it.
- "Personnel Screening" mocks, which use the legacy SCF meaning of `HRS-04`: `cmd/atlas-cli/cmd_controls_import_show_test.go`, `internal/controlimport/suggest_test.go`, `web/e2e/control-import-review.spec.ts` and `web/e2e/control-detail-graph-layout.spec.ts`. Also the `HRS-01` background-check question mock in `web/e2e/questionnaire-review-queue.spec.ts`.
- other mocked API payloads: `web/e2e/audited-under.spec.ts`, `web/e2e/controls-anchor-fit.spec.ts` and `web/app/(authed)/catalog/scf/view.test.ts`.
- token examples and quotes of history: `internal/checklist/`, the `internal/policy/seed/seed.go` comments, the slice 461 error string `scf_anchor "GOV-01" not found` (in `scfseed.go`, `scfseed_integration_test.go`, `import_test.go`, `thr_anchor_integration_test.go` and `scripts/check-integration-seed-order-independence.sh`), and the comment in the applied migration `20260906000000_framework_requirement_points_of_focus.sql`.
- not left out, though they are quotes of history: two slice 567 re-point rationales in `data/crosswalks/hipaa-security-rule.yaml` (§164.308(a)(1)(ii)(C) and §164.316(b)(2)(iii)). They used to read "was HRS-01 @ 0.60" and "was GOV-01 @ 0.60". The pass rewrote them to "was HRS-02" and "was GOV-02", the 2026.3 codes of the same anchors. Slice 567's decisions log D6 rows 1 and 21 keep the original codes. Found in the review of #2398 and recorded by #2405. The rewritten text was left as it is, because each code still names the anchor the requirement moved away from.
- by convention, the existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, `Plans/_archive/` and `scf-pin.json`.

`scfseed.SentinelAnchor` moves to `GOV-02`, the governance program, which is still in the sample. Its comment keeps the historical error string and says the code moved.

`isa/ISA.md` names the GOV anchor of the five SOC 2 controls in the ISC-2 duplicate-definition note. That note is an answered open question, not claim text. Only the code changed, and the probe's self-test fixtures followed, so the shape it describes is still true.

The pin guard's `knownMismatch` entries for the nine codes were removed by hand rather than by the pass.

Crosswalk rationales that quoted an old title now quote the 2026.3 title: Information Security Governance, Information Security Roles & Responsibilities, Human Resources Security, Security Awareness Training, Personnel Termination Process, Publishing Security Documentation, and Periodic Review & Update of Security Documentation.

## D3 — HRIS and Okta lifecycle default: Matt's pick

`hris.worker_lifecycle.v1` and `okta.user_lifecycle.v1` defaulted to `["IAC-04", "HRS-04"]`. The BambooHR and Rippling READMEs called `HRS-04` "Personnel security", its legacy SCF meaning. In the sample, though, `HRS-04` was always Security Awareness Training, so joiner/mover/leaver evidence was anchored on awareness training: a weak fit that predates this group. Moving it by the permutation would have kept that fit on `SAT-04`.

The candidates sent to Matt through the dispatcher:

- `HRS-02` Human Resources Security Management, the HR security program;
- `SAT-04`, the plain permutation;
- `HRS-12` Personnel Termination, which covers only the leaver facet, and IAC-22 already covers that on the access side.

**Matt picked `HRS-02`** (2026-11-15). Both kinds now default to `["IAC-04", "HRS-02"]`. Both READMEs name `HRS-02` with its 2026.3 title.

## D4 — Other defaults move by meaning

| Default                                                                                                        | Old          | New          | Concept kept                      |
| -------------------------------------------------------------------------------------------------------------- | ------------ | ------------ | --------------------------------- |
| manual connector `--control-id` (`local`, `s3`, `sftp`)                                                        | `scf:GOV-04` | `scf:GOV-05` | security roles & responsibilities |
| `manual.upload`, `manual.attestation` (1.0.0, 1.1.0), `manual.control_state_override`, `policy.acknowledgment` | `GOV-04`     | `GOV-05`     | security roles & responsibilities |
| `jira.ticket_evidence`                                                                                         | `GOV-04`     | `GOV-05`     | security roles & responsibilities |
| `security_awareness.training_completion`, `internal/securityawareness` coverage filter                         | `HRS-04`     | `SAT-04`     | awareness training                |
| `personnel_security.workflow`                                                                                  | `HRS-01`     | `HRS-02`     | HR security                       |

The permutation and the meaning agree for every row here. Two fits are weak, and both predate this group. This PR keeps their concept and does not re-choose them, as #1978 D4 did:

- Generic manual evidence and policy acknowledgments default to the security roles and responsibilities control. A policy acknowledgment would fit `GOV-02` or a policy-dissemination control better.
- ISO A.6.4, the disciplinary process, sits on `HRS-02` Human Resources Security Management, not on `HRS-10` Personnel Sanctions.

## D5 — Upgrade effect

Nine stock SOC 2 controls change anchor code, so on the next boot each is superseded, not re-bound:

- `soc2_cc1_1_code_of_conduct`, `soc2_cc1_2_board_oversight`, `soc2_cc2_1_information_quality`, `soc2_cc5_3_policies_procedures`, `soc2_pi1_1_processing_objectives` (`GOV-01`→`GOV-02`);
- `soc2_cc1_3_roles_responsibilities`, `soc2_cc2_2_internal_communication` (`GOV-04`→`GOV-05`);
- `soc2_cc1_4_competence_training` (`HRS-04`→`SAT-04`);
- `soc2_cc1_5_accountability_acknowledgment` (`HRS-01`→`HRS-02`).

Owners, evidence, exceptions and risk links stay with the old version. `scripts/gen-stock-control-hashes.sh` appended the nine manifest hashes. No stock control in this group keeps its code with a changed title, so no title-only re-bind applies.

A tenant control on any of the nine moved codes in `test-2026.1` is flagged with the note from `remaps.json`. One on old `GOV-02`, `GOV-04` or `HRS-12` is not re-bound onto the new holder of that code, because the titles differ. Its note names the new code.

The stock Information Security Policy (`scf_release: "2026.3"`) now links `GOV-02`, `GOV-05` and `RSK-01`. Stock policies are seeded only by `atlas-cli policies seed`, so a tenant that already has the policy keeps the links it was seeded with.

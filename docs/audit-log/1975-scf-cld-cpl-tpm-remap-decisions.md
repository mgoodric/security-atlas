# #1975 — SCF 2026.3 remap, CLD + CPL + TPM family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The fifth #1717 family group. It follows #1974 (`1974-scf-cfg-chg-remap-decisions.md`). The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and crosswalk edge retirement from #1999 do the upgrade work. No migration. The only failures seen were the expected ones: `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated, and the two new many-to-one tests failed before the `CLD-09` row was added.

## D1 — The remap

| Sample ID | 2026.3 ID  | 2026.3 title (verbatim)                        | Basis in `remaps.json` |
| --------- | ---------- | ---------------------------------------------- | ---------------------- |
| `CLD-01`  | `CLD-02`   | Cloud Services                                 | legacy                 |
| `CLD-09`  | `TPM-10`   | Third-Party Risk Assessments & Approvals       | concept (merged; D3)   |
| `CPL-01`  | `CPL-02`   | Statutory, Regulatory & Contractual Compliance | legacy                 |
| `CPL-03`  | `CPL-07.1` | Internal Audit Function                        | concept                |
| `TPM-01`  | `TPM-02`   | Third-Party Management                         | legacy                 |
| `TPM-04`  | `TPM-10`   | Third-Party Risk Assessments & Approvals       | concept                |
| `TPM-05`  | `TPM-14`   | Third-Party Contract Requirements              | legacy                 |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`. Titles are taken verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words. Two were reworded to fit their new titles: `CLD-02` ("govern the cloud services the organization uses") and `TPM-10` ("assess the risk a third party poses and to approve it before engagement"). The others were unchanged.

## D2 — One substitution, no chain

None of the six target codes (`CLD-02`, `CPL-02`, `CPL-07.1`, `TPM-02`, `TPM-10`, `TPM-14`) was in use before this change outside `scf-pin.json` and a synthetic `duplicateIDs` test value. So no code is both a source and a target, and no reference can move twice. The six moves were still applied as one Perl pass that maps each match through a lookup table. The pattern is bounded, `(?<![A-Za-z0-9.-])(CLD-01|CPL-01|CPL-03|TPM-01|TPM-04|TPM-05)(?![0-9]|\.[0-9])`, so dotted children (`TPM-04.1` and the like) are untouched. The README demo's `scf-tpm-04` id follows the same move.

Left as written:

- existing CHANGELOG entries, `docs/audit-log/`, `docs/issues/`, and `scf-pin.json`;
- the HIPAA crosswalk's history notes ("was TPM-04 @ 0.60" and the like), which record what an edge used to be;
- the demo seed questionnaire question code `TPM-05`. It is a question label in the CAIQ style, not an SCF anchor.

Crosswalk rationales that quoted an old title now quote the 2026.3 title: "Third-Party Risk Assessments" becomes "Third-Party Risk Assessments & Approvals", "Cloud Infrastructure Governance" becomes "Cloud Services", and "Compliance Management" becomes "Statutory, Regulatory & Contractual Compliance". The "Third-Party Management" and "Internal Audit Function" titles are unchanged in 2026.3.

## D3 — CLD-09 merged into TPM-10 (low pick, Matt confirmed)

SCF 2026.3 has no cloud-provider-risk control, and D5 maps `CLD-09` to `TPM-10` with `low` confidence. `TPM-04` takes `TPM-10` too, and the pin guard rejects a duplicate `scf_id`. The candidates sent to Matt were:

- **(a) merge into `TPM-10`** (recommended);
- (b) `CLD-03` Cloud Infrastructure Onboarding;
- (c) `CLD-10` Sensitive Data In Public Cloud Providers;
- (d) `TPM-09` Third-Party Criticality Assessments.

Matt chose (a) on 2026-11-15. The `CLD-09` sample row is deleted. Nothing else in the repo referenced it: no stock control, crosswalk, connector, policy or test. `remaps.json` gains `CLD-09 -> TPM-10 (concept)`. This is the table's first many-to-one row. `scfremap.Parse` keys on the source code, so two rows with one target are valid. `TestManyToOneRemapResolvesEachSource` (in `data/scfremap`) and two new `TestReanchorNote` cases (in `internal/api/scfimport`) show that a tenant control on either `CLD-09` or `TPM-04` is pointed at `TPM-10`. The sample's CLD family is now `CLD-02` alone.

## D4 — Demo seed: SCRM anchor

`internal/demoseed/writers.go`'s `demoSCFAnchors` draws from the real 2026.3 catalog, not the sample, so the remap does not touch it. A title check found `{"TPM-03", ..., "Supply Chain Risk Management"}`. In 2026.3, `TPM-03` is Acquisition Strategies, Tools & Methods, and Supply Chain Risk Management (SCRM) is `TPM-04`. The row now reads `TPM-04` with the verbatim title. The dispatcher approved this here. #2302 covers the other demo pairings. Among them is the demo questionnaire's vendor due-diligence question, `fixtures.go`, which is anchored on `TPM-03` and left for #2302.

The stock Vendor Management Policy's `linked_control_ids` also lists `TPM-03`. That code is not in this group's remap and not in the sample, so it is left as it is.

## D5 — Upgrade effect

Two stock SOC 2 controls change anchor code, so on the next boot they are superseded, not re-bound: `soc2_cc9_2_vendor_management` (`TPM-01`→`TPM-02`) and `soc2_cc9_2_vendor_risk_assessment` (`TPM-04`→`TPM-10`). Owners, evidence, exceptions and risk links stay with the old version. No stock control in this group keeps its code with a changed title, so the #2321 title-only re-bind gap does not apply here. A tenant control on any of the seven old codes in `test-2026.1` is flagged with the note from `remaps.json`.

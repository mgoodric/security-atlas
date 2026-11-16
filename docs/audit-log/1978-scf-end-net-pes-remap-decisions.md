# #1978 — SCF 2026.3 remap, END + NET + PES family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The eighth #1717 family group. It follows #1976 (`1976-scf-cry-remap-decisions.md`) and #1977 (`1977-scf-dch-remap-decisions.md`) and uses the same mechanics. The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration. Two failures were seen, both expected:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.
- `TestStockManifestHashes_CoverControlsDir` flagged the three re-anchored stock controls, so `scripts/gen-stock-control-hashes.sh` appended their hashes.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)                  | Basis in `remaps.json` |
| --------- | --------- | ---------------------------------------- | ---------------------- |
| `END-04`  | `END-03`  | Endpoint Protection Mechanisms           | concept                |
| `END-07`  | `END-04`  | Malicious Code Protection (Antimalware)  | concept                |
| `NET-01`  | `NET-02`  | Network Security Controls (NSC)          | legacy                 |
| `NET-04`  | `NET-03`  | Boundary Protection                      | concept                |
| `NET-06`  | `NET-06`  | Network Segmentation (macrosegmentation) | none (title only; D3)  |
| `NET-08`  | `NET-24`  | Wireless Networking                      | concept                |
| `PES-04`  | `PES-08`  | Physical Access Control                  | concept                |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`, and none is a low-confidence pick, so no candidates went to Matt. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. Descriptions stay in our own words and are unchanged; each still describes its 2026.3 title. The sample's order within each family is unchanged, because every new code sorts where the old one sat.

## D2 — One substitution, with the END chain

`END-07` maps to `END-04`, which is itself a source (`END-04` maps to `END-03`). The six moves were therefore applied as one Perl pass that maps each match through a lookup table, never one `sed` per ID. The pattern is bounded, `(?<![A-Za-z0-9.-])(END-04|END-07|NET-01|NET-04|NET-08|PES-04)(?![0-9]|\.[0-9])`, so dotted children are untouched. No dotted child of these codes was cited.

Sources and targets overlap only in `END-04`, which the simultaneous pass handles. Before the change, none of the other targets (`END-03`, `NET-02`, `NET-03`, `NET-24`, `PES-08`) was cited anywhere outside `scf-pin.json`: not in the sample, the crosswalks, the demo seed or any synthetic fixture. So no reference moved twice and no synthetic use needed to move. The other END/NET/PES codes in the repo (`NET-26.5`/`NET-26` in `internal/control/anchor_coverage*.go` and `docs/spec/control-bundle.md`, `END-01` in `internal/controlimport/suggest_test.go`) are synthetic examples, not sample rows, and keep their meaning.

The pin guard's `knownMismatch` entries for the seven codes were removed by hand rather than by the pass.

Left as written:

- existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, and `scf-pin.json`;
- `Plans/_archive/mockups/`, which is archived history;
- `internal/demoseed/fixtures.go`'s family-name list, which includes "Endpoint Security" and "Network Security". They are fictional family labels with no SCF code, not anchors.

Crosswalk rationales and connector READMEs that quoted an old title now quote the 2026.3 title: "Endpoint Security" becomes "Endpoint Protection Mechanisms" (ISO A.6.7, A.7.9, A.8.1; the Intune and Jamf READMEs), "Network Security" becomes "Network Security Controls (NSC)" (ISO A.7.12, A.8.21), and "Malicious Code Protection" becomes "Malicious Code Protection (Antimalware)" (ISO A.8.23). "Boundary Protection" and "Physical Access Control" keep their titles.

## D3 — NET-06: title only

`NET-06` keeps its code, and its title becomes Network Segmentation (macrosegmentation) verbatim. There is no `remaps.json` row, because `from` would equal `to` (as `CHG-02` in #1974 D4). No stock control anchors on `NET-06`, so the #2321 stock title-only re-bind has nothing to move. A tenant-authored control on `NET-06` is reported by `rebind.go`'s title-changed branch, as before.

## D4 — Connector defaults move by meaning

Every connector default here moves with its concept, not just its code:

| Connector | Flag                     | Old          | New          | Concept kept        |
| --------- | ------------------------ | ------------ | ------------ | ------------------- |
| Azure     | `--nsg-control`          | `scf:NET-04` | `scf:NET-03` | Boundary Protection |
| Azure     | `--firewall-control`     | `scf:NET-04` | `scf:NET-03` | Boundary Protection |
| k8s       | `--netpol-control`       | `scf:NET-04` | `scf:NET-03` | Boundary Protection |
| Intune    | `--device-control`       | `scf:END-04` | `scf:END-03` | endpoint protection |
| Jamf      | `--device-control`       | `scf:END-04` | `scf:END-03` | endpoint protection |
| osquery   | `--host-posture-control` | `scf:END-04` | `scf:END-03` | endpoint protection |
| MDM       | webhook `ControlID`      | `scf:END-04` | `scf:END-03` | endpoint protection |

The evidence kinds' `x-default-scf-anchors` follow the same table: `NET-04`, `NET-01` become `NET-03`, `NET-02`, and `END-04` becomes `END-03`.

The endpoint defaults are a direct fit: a device-posture record is evidence of endpoint protection mechanisms, which is `END-03`. Moving them by code alone would have put them on `END-04`, which is now Antimalware. The permutation and the meaning agree here, so no default was re-chosen.

One existing fit is weak, and it predates this group. The Azure NSG and firewall kinds and the k8s NetworkPolicy kind are described in `internal/api/schemaregistry/registry.go` as "network-segmentation" evidence, yet they were anchored on Boundary Protection and Network Security, not on `NET-06` Network Segmentation. ISO A.8.22 (segregation of networks) likewise sits on Boundary Protection. This PR keeps those anchors' meaning and does not re-choose them: moving them to `NET-06` changes the mapping, not just the code. It is left out of scope, as #1977 D4 left the Slack retention default.

## D5 — Upgrade effect

Three stock SOC 2 controls change anchor code, so on the next boot each is superseded, not re-bound:

- `soc2_cc6_4_physical_access` (`PES-04`→`PES-08`);
- `soc2_cc6_6_boundary_protection` (`NET-04`→`NET-03`);
- `soc2_cc6_8_malware_protection` (`END-07`→`END-04`).

Owners, evidence, exceptions and risk links stay with the old version. The three manifest hashes were appended by `scripts/gen-stock-control-hashes.sh`. No stock control in this group keeps its code with a changed title, so no title-only re-bind applies. A tenant control on any of the six moved codes in `test-2026.1` is flagged with the note from `remaps.json`. A tenant control on old `END-04` is not re-bound onto the new `END-04`, because the titles differ (Endpoint Security, now Malicious Code Protection (Antimalware)); its note names `END-03`.

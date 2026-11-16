# #1977 — SCF 2026.3 remap, DCH family group: decisions log

detection_tier_actual: unit
detection_tier_target: unit

The seventh #1717 family group. It follows #1976 (`1976-scf-cry-remap-decisions.md`) and uses the same mechanics. The sample stays on the open working label `test-2026.3`. The remap rows go into `data/scfremap/remaps.json`. The boot-time re-bind and the crosswalk edge retirement from #1999 do the upgrade work. No migration. Two failures were seen, both expected:

- `TestSampleFixtureContentIsPinnedToItsRelease` flagged the changed anchor content, so the open label's hash was updated.
- `TestStockManifestHashes_CoverControlsDir` flagged the three re-anchored stock controls, so `scripts/gen-stock-control-hashes.sh` appended their hashes.

## D1 — The remap

| Sample ID | 2026.3 ID | 2026.3 title (verbatim)         | Basis in `remaps.json` |
| --------- | --------- | ------------------------------- | ---------------------- |
| `DCH-01`  | `DCH-04`  | Data & Asset Classification     | concept                |
| `DCH-03`  | `DCH-27`  | Media & Data Retention          | concept                |
| `DCH-05`  | `MON-27`  | File Integrity Monitoring (FIM) | concept (low; D3)      |
| `DCH-07`  | `DCH-16`  | Media Transportation            | legacy                 |

All rows come from D5 in `1717-scf-pin-ratchet-decisions.md`. Titles are verbatim from `internal/api/scfseed/testdata/scf-pin.json`. `scfremap.Parse` accepts only `legacy` and `concept` as a basis, and a row carries no free-text note. So the low pick is recorded as `concept`, as `CRY-11` was, and the confirmation is recorded here and in the CHANGELOG fragment.

Descriptions stay in our own words. The `MON-27` row is reworded to lead with detection ("detect unauthorized modification of stored data"), to fit its title. The other three are unchanged. The `MON-27` row moves to the sample's MON block with `family: "MON"`. The DCH block is now `DCH-04`, `DCH-16`, `DCH-27`.

## D2 — One substitution, no chain

None of the four target codes (`DCH-04`, `DCH-16`, `DCH-27`, `MON-27`) is a source. None was cited anywhere in the repo outside `scf-pin.json` and the 1717 decisions log, so no reference can move twice and nothing synthetic collides.

The four moves were applied as one Perl pass that maps each match through a lookup table. The pattern is bounded, `(?<![A-Za-z0-9.-])(DCH-01|DCH-03|DCH-05|DCH-07)(?![0-9]|\.[0-9])`, so dotted children are untouched. No dotted child of these four was cited.

Left as written:

- existing CHANGELOG entries and `changelog.d/` fragments, `docs/audit-log/`, `docs/issues/`, and `scf-pin.json`;
- `Plans/_archive/mockups/`, which is archived history;
- `internal/demoseed/fixtures.go`'s family-name list, which includes "Data Classification & Handling". It is a fictional family label with no SCF code, not an anchor.

Crosswalk rationales and code comments that quoted an old title now quote the 2026.3 title. "Data Classification & Handling" becomes "Data & Asset Classification", and "Media Transportation & Custody" becomes "Media Transportation". The schema registry comment on `slack.retention_settings` called `DCH-01` "data protection" (its pre-2026 meaning). It now says "data & asset classification", the meaning of the sample row it actually resolves to.

Historical "was DCH-01 @ 0.65" notes inside the HIPAA rationales went through the same substitution and now read "was DCH-04". That is the same anchor under its 2026.3 code, so the history still reads correctly.

## D3 — DCH-05 to MON-27 (low pick, Matt confirmed)

The sample concept is protecting stored data from improper alteration or destruction, and detecting unauthorized modification of it. Its only reference was HIPAA §164.312(c)(1) Integrity (`subset_of`, 0.80). SCF 2026.3 has no data-integrity control. The candidates sent to Matt were:

- **(a) `MON-27` File Integrity Monitoring (FIM)** (recommended; the D5 pick): a top-level control. It covers detection only, so the HIPAA strength drops to 0.70;
- (b) `CFG-08.2` Integrity Assurance & Enforcement (IAE): covers protection and enforcement too, and would keep 0.80. It is a dotted child, though, and the sample holds only three of those;
- (c) `END-11` Endpoint File Integrity Monitoring (FIM): detection only, and scoped to endpoints;
- (d) `PRI-35` Personal Data (PD) Accuracy & Integrity: privacy-scoped, so it does not fit ePHI in general.

Matt chose (a) on 2026-11-15, with the strength at **0.70**. The HIPAA row keeps `subset_of`. Its rationale records the drop and why. `hipaa_finer_anchor_test.go` asserts the new anchor and strength.

## D4 — Slack connector default stays a straight substitution

The Slack retention record's default `--retention-control-id` was `scf:DCH-01`, and the evidence kind's default anchors were `DCH-01` and `DCH-03`. By meaning, a retention record belongs on `DCH-27` alone. This group applies the remap as a permutation, though: `DCH-01` becomes `DCH-04` and `DCH-03` becomes `DCH-27`. It does not re-choose connector anchors. Re-pointing the retention default is a separate call, left out of scope.

## D5 — Upgrade effect

Three stock SOC 2 controls change anchor code, so on the next boot each is superseded, not re-bound:

- `soc2_c1_1_confidentiality_identification` (`DCH-01`→`DCH-04`);
- `soc2_cc6_7_data_classification` (`DCH-01`→`DCH-04`);
- `soc2_pi1_5_storage_integrity` (`DCH-03`→`DCH-27`).

Owners, evidence, exceptions and risk links stay with the old version. The three manifest hashes were appended by `scripts/gen-stock-control-hashes.sh`. No stock control in this group keeps its code with a changed title, so no title-only re-bind applies. A tenant control on any of the four old codes in `test-2026.1` is flagged with the note from `remaps.json`.

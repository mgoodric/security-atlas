# #2396 — SCF NET anchor fit after the #1978 remap: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

These are follow-ups from the review of #2388 (#1978, the END/NET/PES remap). The problems predate #2388 (its D4). Every check below was run against the pinned SCF 2026.3 workbook (`secure-controls-framework-scf-2026-3.xlsx`, sha256 `5a89bf2d…a7df697`, the hash in `internal/api/scfseed/testdata/scf-pin.json`), sheet `SCF 2026.3`, using its control, description and framework-mapping columns. SCF descriptions are paraphrased here, not bundled.

## D1 — PCI 1.4.1 moves from `NET-24` to `NET-06`

| Field                  | Before                       | After                                             |
| ---------------------- | ---------------------------- | ------------------------------------------------- |
| Anchor                 | `NET-24` Wireless Networking | `NET-06` Network Segmentation (macrosegmentation) |
| Relationship, strength | `equal`, 0.8                 | unchanged                                         |

1.4.1 requires network security controls between trusted and untrusted networks. Its rationale already said "map to SCF Network Segmentation". The `NET-24` anchor came from the mechanical #1978 move of old `NET-08` (Wireless Network Security), which was already the wrong concept before the remap. In the workbook's `PCI DSS 4.0.1` column, 1.4.1 maps to `NET-03`, `NET-06`, `NET-29`, `NET-41.1`, `CFG-03`, `MON-25` and `SEA-06`, and not to `NET-24`. `NET-06` matches the rationale's stated concept, so it was chosen over `NET-03`. The rationale now quotes the full 2026.3 title.

On the next catalog import, the crosswalk importer retires the `1.4.1 → NET-24` edge, because the crosswalk no longer lists it (#1971), and adds `1.4.1 → NET-06`. No stock control and no connector default anchors on either code through this mapping.

Confidence: HIGH. The requirement's own rationale and the workbook agree.

## D2 — PCI 5.3.1: stale title only

The rationale quoted "SCF Endpoint Protection Measures". The 2026.3 title of `END-03` is "Endpoint Protection Mechanisms", so the rationale now uses it. The anchor is unchanged.

In the workbook, 5.3.1 maps to `END-03.2` and `END-04`, not to `END-03` itself. `END-04` (Malicious Code Protection (Antimalware)) is already 5.2.1's anchor. Re-choosing 5.3.1's anchor was out of this ticket's scope, so the `intersects_with` 0.7 edge on `END-03` stays. It is recorded here in case a later crosswalk review wants `END-04`.

## D3 — Test

`TestLoad_PCINetworkAndEndpointAnchorsMatchSCF2026_3` (`internal/api/soc2import/pci_loader_test.go`) pins 1.4.1 to `NET-06`, and requires both rationales to quote the 2026.3 titles. It failed before the YAML change (`1.4.1 scf_anchor = "NET-24"`, plus the 5.3.1 title) and passes after.

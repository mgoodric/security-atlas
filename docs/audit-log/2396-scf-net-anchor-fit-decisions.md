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

## D4 — Segmentation evidence: Matt's call (2026-11-18)

This one changes connector defaults and where evidence lands, so it went to Matt. He chose the recommended split. From the workbook: `NET-03` covers monitoring and controlling communications at the external boundary and at key internal boundaries. `NET-06` covers network segmentation that isolates assets from other network resources. In the workbook's ISO 27002:2022 column, A.8.20 maps to both, and A.8.22 maps to `NET-06`, `NET-06.1` and `NET-33`, but not to `NET-03`.

| Source                                                  | What it attests                                                                                                        | Before                           | After                                                          | Confidence  |
| ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | -------------------------------- | -------------------------------------------------------------- | ----------- |
| ISO A.8.22 crosswalk                                    | Segregation of networks                                                                                                | `NET-03` equal 0.8               | `NET-06` equal 0.8, no `NET-03` secondary                      | HIGH        |
| k8s `--netpol-control`, `k8s.networkpolicy_coverage.v1` | Per-namespace NetworkPolicy and CNI policy counts, default-deny per direction: isolation between workloads             | `scf:NET-03`; `[NET-03, NET-02]` | `scf:NET-06`; `[NET-06, NET-02]`                               | HIGH-MEDIUM |
| Azure `--nsg-control`, `azure.nsg_rules.v1`             | Per-NSG allow/deny rules and subnet/NIC associations: both a boundary check (no 0.0.0.0/0 on 22/3389) and segmentation | `scf:NET-03`; `[NET-03, NET-02]` | flag unchanged; `[NET-03, NET-06, NET-02]`                     | MEDIUM      |
| Azure `--firewall-control`, `azure.firewall_rules.v1`   | A central L3-L7 perimeter policy point                                                                                 | `scf:NET-03`; `[NET-03, NET-02]` | unchanged; only the `registry.go` comment drops "segmentation" | HIGH        |

The schema anchor lists are flat (#2620), so "secondary" means a later entry in the list, not a weighted edge.

## D5 — Upgrade effect

- An install that has already registered `k8s.networkpolicy_coverage` or `azure.nsg_rules` 1.0.0 keeps the default anchors it stored, because the boot import skips a kind and version that is already present (as with #2387). New installs get the new lists.
- A k8s connector run on its defaults stamps `scf:NET-06` once upgraded. Evidence already ingested keeps its `control_id`, so records from before and after the change sit under different codes. The upgrade guide's connector-default table carries the row.
- On boot, the bundled ISO crosswalk converges (#2014): the `A.8.22 → NET-03` edge is retired and `A.8.22 → NET-06` is added.

## D6 — Tests

Each default is pinned:

- `TestRunCmd_NetpolDefaultIsSegmentation` (k8s connector);
- `TestNetworkControlDefaultsStayOnBoundaryProtection` (Azure, both the `run` and `eventgrid` commands);
- `TestSegmentationKindsDefaultAnchors` (the three schemas' anchor lists);
- `TestLoad_ISOA822OnNetworkSegmentation` (the ISO crosswalk).

The fixture-level guard `TestDefaultSCFAnchors_ResolveInBundledCatalog` still passes, because `NET-06` is in the bundled sample.

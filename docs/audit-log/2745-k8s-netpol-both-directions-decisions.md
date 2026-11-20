# #2745 — k8s NetworkPolicy verdict requires both directions; `--netpol-control` defaults to the stock control: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

Follow-ups from the review of #2735 (#2648). The review caught both gaps; no test pinned either.

## D1 — A one-direction namespace fails, not inconclusive

The ticket allowed either. `fail` was chosen. The stock `network_segmentation_default_deny` query rolls namespaces up as: any fail, then any pass, then any inconclusive. An `inconclusive` one-direction namespace would be outranked by any passing namespace, and the control would pass with the open direction hidden. `fail` keeps it visible in the control result. The reason names the open direction (`namespace enforces default-deny ingress only (egress is open)`, and the egress-only mirror). Confidence: HIGH.

Effect on evidence already flowing: a namespace that is default-deny in one direction only used to push `PASS` and now pushes `FAIL`. Records already in the ledger keep their verdict (the ledger is append-only). The upgrade guide says so.

## D2 — `--netpol-control` defaults to `network_segmentation_default_deny`

Ingest resolves a non-UUID `control_id` that names a bundle to the tenant's active control (`internal/evidence/ingest/ingest.go`, #2100). The bundle id is therefore the one default that links records to the stock control on default flags. `scf:NET-06` links to nothing and left the control inconclusive. The flag stays as an override. A tenant that has not run the bootstrap since #2648 has no such bundle. Its records then keep a free-form `control_ref` until the bootstrap uploads the control, which is the same outcome as the old default. Confidence: HIGH.

## D3 — NET-06 title vs what the evidence measures

SCF 2026.3 titles `NET-06` "Network Segmentation (macrosegmentation)". Namespace-level default-deny between workloads is closer to microsegmentation, which is finer-grained than that title. `NET-06` is still the anchor that the ISO A.8.22 and PCI 1.4.1 crosswalk edges reach (#2396 D1/D4), so the anchor does not change here. If SCF gives microsegmentation its own control, that control would fit the k8s evidence better, and the anchor should be revisited then.

## D4 — Tests

- `TestAssess_Verdicts` (`connectors/k8s/internal/netpol/netpol_test.go`): the ingress-only case now expects `fail`. Egress-only `fail` and split-across-two-policies `pass` cases are added.
- `TestAssess_CNIPolicyEstablishesDefaultDeny` checks that a CNI policy credits its direction's flag, and that a one-direction namespace fails.
- `TestClient_CiliumPresent_FoldsNamespacedAndClusterwide`: `dev`, which only inherits cluster-wide egress-deny, now fails with the ingress-open reason, and `prod` (both directions) passes.
- `TestRunCmd_NetpolDefaultIsStockSegmentationControl` pins the new default, and `TestRunCmd_NetpolControlOverride` pins the override.
- The control's description changed, so `stock_manifests_gen.go` was regenerated.

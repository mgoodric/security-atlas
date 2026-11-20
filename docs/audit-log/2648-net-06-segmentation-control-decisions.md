# #2648 — stock control on SCF NET-06 Network Segmentation: decisions log

detection_tier_actual: none
detection_tier_target: unit

#2396 moved ISO A.8.22 and PCI 1.4.1 to SCF `NET-06` Network Segmentation. Matt accepted (2026-11-18) that both would report uncovered until a stock control was anchored on `NET-06`. This ticket adds that control: `controls/soc2/network_segmentation_default_deny/control.yaml`.

## D1 — Anchor: `NET-06`, no secondary

The ticket fixes the anchor. Both crosswalk edges (A.8.22 and 1.4.1) are `equal` 0.8 on `NET-06`. #2396 D1 and D4 record the workbook evidence. No anchor or strength changed here. Confidence: HIGH.

## D2 — Evidence: `k8s.networkpolicy_coverage.v1` only, not `azure.nsg_rules.v1`

The ticket allowed `azure.nsg_rules.v1` "if sensible". It is left out:

- The Azure connector's verdict (`connectors/azure/internal/nsg/nsg.go` `verdict`) fails an NSG only when a rule allows inbound traffic from the Internet to a management port. That is a boundary check (`NET-03`), not segmentation between internal networks.
- If it were included, an open port 22 would fail the segmentation control, and an NSG that segments nothing would pass it.
- The Azure `--nsg-control` default stays `scf:NET-03` (#2396 D4), so its records do not reach a `NET-06` control on defaults anyway.

The schema's default anchor list keeps `NET-06` as a mapping hint. A future Azure verdict that scores subnet-to-subnet isolation could join this control. Confidence: MEDIUM-HIGH.

## D3 — Pass/fail comes from the connector's per-namespace verdict

The Rego sandbox (`internal/eval/rego.go`) gives a query only `{records: [{result, observed_at, hash}]}`, never the payload. The query therefore cannot read `default_deny_ingress`, `default_deny_egress` or `policies[].selects_all_pods` directly. The control rolls up the verdict the k8s connector already derives from those fields (`connectors/k8s/internal/netpol/netpol.go` `verdict`):

- **pass:** an all-pods (empty-selector) policy denies ingress, egress or both by default;
- **fail:** the namespace has no policy, or only per-pod allow rules with no default-deny;
- **inconclusive:** the namespace read errored.

The roll-up uses the same precedence as every other stock Rego query: any fail, then any pass, then any inconclusive, else na. Empty is inconclusive. One allow-all namespace fails the control.

The connector passes a namespace that is default-deny in one direction only. The ticket asked for ingress **and** egress coverage. Requiring both would mean changing the connector's verdict, which changes evidence already flowing under #2396's defaults. That is outside this ticket, so it goes to Matt as a follow-up question. The control text says a one-direction namespace stays visible through the record's per-direction flags.

## D4 — `bundle_id` without the `soc2_` prefix

The bundled SOC 2 crosswalk (`data/crosswalks/soc2-tsc-2017.yaml`) has no edge to `NET-06`. Its CC6.6 edges go to `NET-03` and `IAC-37`. A `soc2_cc6_6_*` id would claim a criterion the crosswalk does not give, so the bundle is `network_segmentation_default_deny`. It still lives in `controls/soc2/`, because that is the directory the bootstrap uploads (`CONTROLS_DIR`). The kit README lists it under "No TSC criterion". SOC 2 coverage is unchanged at 61 / 61.

Adding a SOC 2 → `NET-06` edge would be a crosswalk change that needs the workbook's SOC 2 column. It was not checked here and is left as a question for Matt.

## D5 — Control text

The text is in our own words, with no SCF or ISO wording reproduced (#2377). It cites ISO/IEC 27001:2022 A.8.22, PCI DSS 4.0 1.4.1 and the HIPAA 164.308(a)(4)(ii)(A) `intersects_with` edge by id, matching the crosswalks. `freshness_class: daily` matches the k8s connector's scheduled runs and the other daily automated bundles.

## D6 — Linking evidence to the control

`--netpol-control` defaults to `scf:NET-06`, which ingest stores as a free-form `control_ref` and which links to no control row (`internal/evidence/ingest/ingest.go`, #2100). The control text, the k8s README and the upgrade guide tell the operator to pass `--netpol-control network_segmentation_default_deny`. Changing the connector default to the bundle id is a connector-default change in the style of #2396 D4, so it was not made here.

## D7 — Upgrade

`bootstrap.sh` phase 5b uploads every `controls/soc2/` bundle on every run, so an existing install gains the control on its next bootstrap. ISO A.8.22 and PCI 1.4.1 are covered from then on. The stock-manifest hash table (`internal/api/scfimport/stock_manifests_gen.go`) was regenerated, so a later SCF re-bind treats the bundle as stock.

## D8 — Tests

- `TestStockControls_CoverSegmentationRequirements` (`internal/control/network_segmentation_stock_test.go`) walks stock control → `scf_anchor_id` → crosswalk edge → requirement over the bundled ISO and PCI crosswalks. It failed with the bundle removed (`A.8.22: reached by stock controls []`, the same for `1.4.1`) and passes with it.
- `TestSegmentationControl_EvaluatesNamespaceCoverage` evaluates the bundle through `eval.EvaluateFixture` on per-namespace fixture records.
- The generic `TestStockControlPoliciesEvaluateOnFreshInstall` and `TestEvidenceKindDrift_*` cover the bundle with the rest of the kit.
- The `coverage-check` bundle count moves from 63 to 64. The self-host bundle test's control counts move with it.

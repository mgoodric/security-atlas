# 523 — Kubernetes NetworkPolicy coverage evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shape, the per-policy field set, the SCF anchors, the
scope-minimum, the default-deny coverage-assessment verdict, and THE load-bearing
call — the structural over-collection boundary that keeps every NetworkPolicy
peer / selector / port VALUE and all pod/Secret material out of the record). It
does NOT block merge; the maintainer iterates post-deployment from the "Revisit
once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no product-behavior bug surfaced during the
  slice — the build-time corrections were the expected third-kind authoring fixes,
  caught at the unit tier as I wrote the threading).
- **detection_tier_target:** none.

The build-time corrections were the expected consequence of adding a third
evidence kind to the slice-487 connector, all authoring fixes rather than
product-behavior defects:

- the `cmd` seam harness gained a `netpolScan` override + each existing
  skip-test that does not seam netpol now sets `--skip-netpol`, so the
  on-by-default NetworkPolicy pull never reaches the live HTTP client in those
  seam tests (mirrors how slice 487's workload pull is skipped in RBAC-only
  tests);
- the register integration test's `supported_kinds` count moved 2 → 3 and the
  round-trip test moved `pushed == 2` → `3` (renamed `TestRun_PushesBothKinds`
  → `TestRun_PushesAllKinds`);
- the client test initially referenced a non-existent `rawBlock` type for the
  egress-derive case — corrected to the real `[]json.RawMessage` field type
  before first compile.

## Decisions made

### D1 — A SEPARATE sibling kind `k8s.networkpolicy_coverage.v1`, mirroring slice 487's two kinds

- **Options:** (a) a new sibling kind on the existing `atlas-k8s` binary;
  (b) fold NetworkPolicy facts into the workload kind (rejected — unrelated
  resource, different scope shape); (c) a brand-new connector binary (rejected —
  same source, same ServiceAccount, same read-only API).
- **Chosen:** (a). A new `connectors/k8s/internal/netpol` collector + a new
  `k8s.networkpolicy_coverage.v1` kind on the SAME `atlas-k8s` binary, registered
  alongside the two slice-487 kinds. This is the slice-487 pattern verbatim:
  `netpol.Assess` mirrors `workload.Inspect`, `netpol.Client` mirrors
  `workload.Client`, `idem.NetpolCoverageKey` mirrors `WorkloadKey`,
  `buildNetpolRecord` mirrors `buildWorkloadRecord`.
- **Rationale:** segmentation answers a control question (CC6.6 / ISO A.8)
  distinct from RBAC or workload hardening, but it is the same source (the
  Kubernetes API, the same read-only ServiceAccount, one additional `get,list`
  rule), so a sibling kind on the same binary is the minimum-surface choice.

### D2 — THE load-bearing call: NetworkPolicy SPEC metadata ONLY — never the peer / CIDR / selector / port contents, never pod material

- **Decision:** the netpol HTTP client models only the SPEC fields that decide
  coverage — `spec.podSelector` (reduced to a single `SelectsAllPods` boolean),
  `spec.policyTypes`, and the **length** of `spec.ingress` / `spec.egress`. The
  ingress/egress blocks are decoded as opaque `[]json.RawMessage` purely so their
  length can be taken; their `from` / `to` peers (namespaceSelector labels,
  podSelector labels, ipBlock CIDRs) and `ports` are never decoded into a typed
  Go shape. Go's JSON decoder discards every unmodeled key, so no pod content,
  container env, Secret / ConfigMap value, log, exec output, or traffic ever
  enters memory in this package. The `RawPolicy` / `RawNamespace` / `Coverage` /
  `PolicySummary` structs have **no field** that could carry a peer or selector
  value.
- **Why a COUNT, not the rules:** the coverage control question is "is this
  namespace default-deny?", answered by "all-pods policy with **zero** allow
  rules in a direction". That needs only the rule-block count, not the rule
  contents. Emitting the peers would (a) be over-collection and (b) risk leaking
  the very network topology the control is meant to keep tight.
- **Guards:** the netpol client test (`TestClient_NeverMaterializesPeerOrSelectorPayload`)
  serves a policy whose rule block embeds a podSelector label
  (`top-secret-app-label`), an ingress peer namespaceSelector label
  (`confidential-team-label`), a CIDR (`10.9.8.7/32`), and a port (`8443`), runs
  the full collect → assess path, and asserts none of those strings appears in
  any record-bound field. The integration `TestEmittedRecords_NoSecretsOrConfigValues`
  applies a top-level allow-list AND a nested per-policy-summary allow-list.

### D3 — Coverage-assessment verdict: default-deny = all-pods policy with zero allow rules in a direction

- **Decision:** a namespace is **default-deny** in a direction when at least one
  of its policies (a) selects every pod (`spec.podSelector == {}`) AND (b)
  governs that direction (`policyTypes` contains it) AND (c) has zero allow rules
  in that direction. `PASS` when default-deny holds for ingress OR egress; `FAIL`
  when no default-deny exists (zero policies, or only per-pod / has-allow-rule
  policies); `INCONCLUSIVE` when the per-namespace read errored.
- **Rationale:** this is the Kubernetes canonical default-deny shape (the
  upstream-documented `kind: NetworkPolicy / podSelector: {} / policyTypes:
[Ingress]` with no `ingress:` block). PASS on a SINGLE direction is deliberate:
  default-deny ingress alone is a meaningful, common segmentation posture, and the
  connector emits a **descriptive** verdict — the platform evaluator owns whether
  a given control demands both directions. The connector should not pre-judge the
  control's strictness.
- **Alternative rejected:** scoring per-policy (PASS/FAIL per NetworkPolicy
  object) — rejected because the control question is per-namespace ("is this
  namespace segmented?"), so the record granularity is the namespace, with the
  policies as a summary sub-list.

### D4 — SCF anchors `["NET-04", "NET-01"]`

- **Decision:** `x-default-scf-anchors = ["NET-04", "NET-01"]` — NET-04 Boundary
  Protection (the primary segmentation anchor) + NET-01 Network Security (the
  family parent). Both are present in `migrations/fixtures/scf-sample.json`.
- **Rationale:** matches the slice-520 Azure-NSG network-segmentation kind, which
  chose the same pair for the same control intent. Anchors are default mapping
  hints flagged for maintainer recheck (OQ #9), consistent with every other
  connector kind.

### D5 — Scope minimum adds a per-namespace `namespace` dimension

- **Decision:** netpol records set `cluster` + `environment` (the connector
  minimums) PLUS a `namespace` dimension, because there is one coverage record
  per namespace and a FrameworkScope predicate may cut on namespace (e.g. "PCI
  CDE = the `cardholder-data` namespace"). RBAC / workload records keep only
  `cluster` + `environment`.
- **Idempotency:** identity is the namespace alone (`idem.NetpolCoverageKey`), so
  two runs within the same UTC hour for the same namespace collapse to one ledger
  row.

## Anti-criteria honored (P0 — all met)

- **Push-only wire (invariant #3).** No platform route added, no proto change;
  records flow through the existing `EvidenceIngestService.Push`.
  `profiles_supported` stays `[pull]`.
- **No write verb / `secrets` / wildcard.** The ClusterRole gains exactly one
  rule: `networking.k8s.io: networkpolicies` verbs `get,list`. The
  `DocumentedClusterRole` least-privilege test (verbs ⊆ {get,list}, resources ∌
  {secrets,_}, apiGroups ∌ _) passes unchanged and now also covers the new rule.
- **No Secret / ConfigMap / env / log / traffic read.** See D2 + the two guard
  tests.
- **Honest interval.** The pull profile is named honestly ("operator-scheduled,
  recommended 24h — NOT continuous monitoring"); no "continuous monitoring"
  label.

## Revisit once in use (maintainer)

- **Cursor pagination.** v0 reads one bounded page (`limit=500`) of namespaces
  and of networkpolicies. A cluster with >500 of either truncates silently. The
  follow-on is filed (band 621–624) — see Spillover.
- **CNI-native policies.** Cilium / Calico CRDs (`CiliumNetworkPolicy`,
  `NetworkPolicy` in `crd.projectcalico.org`) are NOT read — `networking.k8s.io`
  only. A cluster that enforces segmentation purely via CNI CRDs would read as
  unprotected here. Filed as a follow-on.
- **PASS-on-single-direction.** If real-world controls consistently demand BOTH
  directions, the descriptive verdict could be tightened — but that is an
  evaluator-side policy choice, not a connector change.

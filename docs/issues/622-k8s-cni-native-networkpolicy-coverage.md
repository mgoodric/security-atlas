# 622 — Kubernetes connector: CNI-native NetworkPolicy (Cilium / Calico) coverage

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (which CRD fields map to "default-deny", per-CNI shape
differences)
**Status:** `blocked` — depends on #523 (the `networking.k8s.io` NetworkPolicy
coverage kind + the segmentation assessment shape it establishes).
**Parent:** #523. Spun off from slice 523's decisions-log "Revisit once in use" —
the netpol collector reads `networking.k8s.io/v1` NetworkPolicy ONLY.

## Narrative

Slice 523 reads upstream `networking.k8s.io/v1` NetworkPolicy objects. Many
production clusters enforce segmentation entirely through their CNI's own CRDs —
`CiliumNetworkPolicy` / `CiliumClusterwideNetworkPolicy` (`cilium.io`) or Calico
`NetworkPolicy` / `GlobalNetworkPolicy` (`crd.projectcalico.org`). Such a cluster
would read as fully **unprotected** under slice 523 even when it is tightly
segmented — a false-FAIL that erodes operator trust in the segmentation evidence.

This slice extends the netpol collector to optionally read the installed CNI's
policy CRDs (detected by CRD presence, opt-in per the connector's read-only
ClusterRole gaining the relevant CRD `get,list` rules) and fold their coverage
into the per-namespace assessment. The over-collection guard from slice 523
applies verbatim: CRD SPEC metadata only, never workload contents, never the peer
contents of a rule.

## Acceptance criteria

- [ ] **AC-1.** When a Cilium / Calico CRD is present, the collector reads its
      policy objects (`get,list`) and contributes to the per-namespace
      default-deny assessment.
- [ ] **AC-2.** The coverage record distinguishes the policy source
      (`networking.k8s.io` vs the CNI CRD) so the evaluator can reason about it.
- [ ] **AC-3.** The ClusterRole gains exactly the CRD `get,list` rules needed —
      no write verb, no `secrets`, no wildcard; the least-privilege test covers
      them.
- [ ] **AC-4.** Over-collection guard: CRD SPEC metadata only; a no-leak test
      proves no peer / selector / pod payload escapes.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only.
- Does NOT add a write verb / `secrets` / wildcard to the ClusterRole.
- Does NOT read pod / Secret / traffic data.

## Dependencies

- **#523** — the netpol collector + the per-namespace coverage record + the
  default-deny assessment shape this slice extends.

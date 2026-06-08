# 621 — Kubernetes connector: cursor pagination across all reads

**Cluster:** Connectors
**Estimate:** S (0.5-1d)
**Type:** STANDARD (mechanical — follow the Kubernetes list `continue` token)
**Status:** `ready`
**Parent:** #523 (and #487). Spun off from slice 523's decisions-log "Revisit
once in use" — the netpol collector (like the slice-487 RBAC + workload
collectors) reads only a bounded first page.

## Narrative

Every `atlas-k8s` read (`internal/rbac`, `internal/workload`, and now
`internal/netpol` from slice 523) issues a single `?limit=500` GET and consumes
only the first page. A cluster with more than 500 of any listed resource
(networkpolicies, namespaces, deployments, rolebindings, ...) is silently
truncated — the connector under-reports coverage with no error. This was an
explicit v0 deferral in slice 487 and re-deferred in slice 523.

This slice adds Kubernetes list-pagination: follow `metadata.continue` from each
list response until the server returns an empty continue token, accumulating
items across pages. It is a shared helper in the connector's HTTP client layer
that all three collectors (rbac / workload / netpol) adopt.

## Acceptance criteria

- [ ] **AC-1.** Each `get/list` read follows the `metadata.continue` token to
      completion (bounded by a sane page cap + the existing run timeout).
- [ ] **AC-2.** The rbac, workload, and netpol clients all use the shared
      paginating reader; a mocked multi-page API surface proves each accumulates
      across at least two pages.
- [ ] **AC-3.** A run timeout still caps a pathologically large cluster (no
      unbounded loop); the cap is documented.
- [ ] **AC-4.** No new ClusterRole grant; still `get,list` only.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only.
- Does NOT add a write verb or any new resource grant.
- Does NOT change the evidence-kind shapes.

## Dependencies

- **#523** / **#487** — the three collectors + their HTTP clients.

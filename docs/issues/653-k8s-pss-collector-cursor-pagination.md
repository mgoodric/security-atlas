# 653 — Kubernetes connector: cursor pagination for the PSS collector

**Cluster:** Connectors
**Estimate:** XS (< 0.5d)
**Type:** STANDARD (mechanical — adopt the shared paginating reader)
**Status:** `ready`
**Parent:** #621 (and #524). Spun off from slice 621's "pss in-or-out" decision —
slice 621 added the shared `k8slist.Reader` cursor walk to the rbac, workload,
and netpol collectors but deliberately left the slice-524 PSS collector OUT of
scope (slice 621 AC-2 names only rbac/workload/netpol).

## Narrative

The PSS (Pod Security Admission) collector
(`connectors/k8s/internal/pss/client.go`) still issues a single `?limit=500`
namespace-list GET and consumes only the first page (`getJSON` →
`fmt.Sprintf("%s%s?limit=%d", c.BaseURL, path, pageLimit)`), exactly the
single-page pattern slice 621 removed from the other three collectors. A cluster
with more than 500 namespaces has its PSS-admission posture silently truncated —
the connector under-reports coverage with no error.

Slice 621 built the fix as a shared, generic, read-only paginating reader
(`connectors/k8s/internal/k8slist`, `ListAll[T]`) that follows the Kubernetes
`metadata.continue` cursor to completion, bounded by a page cap
(`k8slist.MaxListPages`) plus the run timeout. This slice adopts that same
reader in the PSS collector — no new helper, no new pattern.

This was held back from slice 621 because PSS is slice 524's collector;
expanding 621's scope to touch it would have widened the blast radius. It is a
small, mechanical follow-on: delete the duplicated `getJSON` / `APIError` /
`drain` in `pss/client.go`, hold a `*k8slist.Reader`, and route the namespace
list through `k8slist.ListAll[apiNamespace]`. Re-export `pss.APIError =
k8slist.APIError` so existing callers/tests are unaffected (the same shape the
other three collectors adopted in slice 621).

## Acceptance criteria

- [ ] **AC-1.** The PSS collector's namespace list read follows the
      `metadata.continue` token to completion via `k8slist.ListAll`, bounded by
      `k8slist.MaxListPages` + the run timeout.
- [ ] **AC-2.** The PSS client uses the SHARED `k8slist.Reader` (no fourth copy
      of the paginating reader); a mocked multi-page namespace API surface proves
      it accumulates across at least two pages.
- [ ] **AC-3.** No new ClusterRole grant; still `get,list` only (the PSS
      collector reads namespaces, which the documented ClusterRole already
      grants — `k8sauth.DocumentedClusterRole` unchanged).

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3).
- Does NOT add a write verb or any new resource grant / ClusterRole rule.
- Does NOT change the `k8s.pod_security_admission.v1` evidence-kind shape.

## Dependencies

- **#621** — the shared `k8slist.Reader` / `ListAll[T]` this slice adopts (now
  on `main`).
- **#524** — the PSS collector being paginated.

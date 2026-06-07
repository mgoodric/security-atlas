# 487 — Kubernetes connector: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind field shapes, the `x-default-scf-anchors` per kind, and the
scope-minimum ClusterRole). It does NOT block merge — the maintainer iterates
post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the build; the connector mirrors the locked slice-486
pattern and the tests passed on first integration run after the standard
`t.Parallel`-vs-`t.Setenv` fix.)

## Decisions made

### D1 — Two evidence kinds, named `k8s.rbac_binding.v1` + `k8s.workload_security_context.v1`

- **Options considered:** (a) one combined `k8s.security_posture.v1` kind; (b)
  finer-grained kinds (separate `role` vs `binding`, separate per-flag workload
  kinds); (c) two kinds aligned to the two evidence surfaces in the spec.
- **Chosen:** (c). One kind per surface, mirroring the slice-486 Azure shape
  (`azure.entra_role_assignment.v1` + `azure.storage_account_config.v1`). The
  RBAC kind is **binding-centric** (one record per RoleBinding /
  ClusterRoleBinding, with the referenced role's rules attached) because
  "who-can-do-what" is the auditable unit — an orphan Role with no binding grants
  nobody anything. The workload kind is **workload-centric** (one record per
  Deployment / DaemonSet / StatefulSet) with the security-context flags
  aggregated across containers.
- **Rationale:** matches the canvas evidence-engine model (one descriptive
  record per real-world resource) and the locked connector pattern. A combined
  kind would force the evaluator to demux two unrelated schemas.
- **Confidence:** high.

### D2 — `x-default-scf-anchors`

- **RBAC kind → `["IAC-21", "IAC-22"]`.** IAC-21 (Privileged Account
  Management) + IAC-22 (Least Privilege) are the SCF anchors for "who holds which
  privileges". This reuses the exact anchor pair slice 486 chose for its Entra
  role-assignment kind — RBAC bindings are the Kubernetes analog of directory-role
  assignments. The connector-side `grants_wildcard` heuristic surfaces
  cluster-admin-grade reach for the evaluator.
- **Workload kind → `["CFG-02", "END-04"]`.** CFG-02 (Secure Baseline
  Configurations) is the primary anchor — runAsNonRoot / non-privileged /
  read-only-rootfs / no-host-namespace _are_ the secure baseline for a container
  workload. END-04 (Endpoint Security) is the secondary anchor — a workload is the
  compute endpoint whose hardening these flags describe. Both codes are already
  present in the SCF sample catalog and in use by existing schemas.
- **Rationale:** anchors must be defensible to an auditor; these are the closest
  SCF domains. They are _defaults_ — a tenant can remap via control bundles.
- **Confidence:** medium (anchor accuracy is the OQ #9 load-bearing call the
  maintainer re-checks against the full SCF crosswalk; the workload→CFG-02 mapping
  is the one most worth a second look).

### D3 — Scope minimum: the exact read-only ClusterRole (the load-bearing guard)

The connector requires EXACTLY these rules (verbs `get`,`list` only):

| apiGroups                   | resources                                                   | verbs    |
| --------------------------- | ----------------------------------------------------------- | -------- |
| `rbac.authorization.k8s.io` | `roles`,`clusterroles`,`rolebindings`,`clusterrolebindings` | get,list |
| `apps`                      | `deployments`,`daemonsets`,`statefulsets`                   | get,list |
| `""` (core)                 | `namespaces`                                                | get,list |

- **Chosen guard:** NO rule grants `secrets`; NO rule grants any write verb
  (create/update/patch/delete/deletecollection); NO rule uses a wildcard
  (`*`) verb / resource / apiGroup. This is enforced programmatically:
  `k8sauth.DocumentedClusterRole()` is the single source of truth that the
  README, the `permissions` subcommand, AND a unit test
  (`TestDocumentedClusterRole_IsLeastPrivilege`) all read — the test fails the
  build if a future edit adds a write verb, a wildcard, or `secrets` (P0-487-2 /
  P0-487-3).
- **Rationale:** the Secret-read exclusion is the dominant Kubernetes-specific
  over-collection risk (threat-model I). Pinning it in code + test makes the
  guard a ratchet, not a doc that drifts. `namespaces` is included so the
  connector can enumerate scope context without granting anything sensitive.
- **Confidence:** high.

### D4 — Stable fields (actor_id, observed_at, idempotency_key, scope)

- **actor_id:** `connector:k8s:<service>@<version>` where `<service>` ∈
  {`rbac`,`workload`} — the cross-connector convention (slice 004 / 486).
- **observed_at:** hour-truncated UTC (slice-004 granularity), so re-runs within
  the hour dedup.
- **idempotency_key:** `sha256(prefix | <identity> | <hour>)`. RBAC identity =
  `scope/namespace/name`; workload identity = `kind/namespace/name`. Both
  uniquely identify the resource cluster-wide.
- **scope dimensions:** `cluster` + `environment` (both operator-supplied,
  required flags). A cluster is the natural top-level scope for Kubernetes
  evidence; `environment` ties it into the platform's multidimensional scope.
- **Confidence:** high.

### D5 — Thin read-only HTTP client, NOT `k8s.io/client-go`

- **Options considered:** (a) full `client-go` typed list calls (the spec's
  Phase-2 note suggests this); (b) a thin read-only HTTP client against the
  Kubernetes REST API, mirroring slice 486's deliberate "no heavy vendor SDK"
  choice.
- **Chosen:** (b). The two evidence surfaces need four GET endpoints
  (`/apis/rbac.authorization.k8s.io/v1/...` and `/apis/apps/v1/...`). A thin
  HTTP client + a narrow `API` interface (faked in tests) satisfies every AC and
  P0 without dragging `client-go`'s large transitive tree (and its frequent
  `go.sum` churn) into the monorepo. The interface seam — not the SDK — is the
  load-bearing testability primitive the spec actually requires.
- **Rationale:** consistency with the locked 486 pattern; smaller dependency
  surface; the JSON shapes we decode are stable Kubernetes API surfaces.
  Critically, the workload client decodes ONLY the security-context + host-
  namespace fields of the pod template — it never models `env` / `envFrom` /
  `volumes` / Secret refs, so that payload is discarded by the JSON decoder and
  can never reach an evidence record (the structural half of P0-487-3).
- **Confidence:** high.

### D6 — Workload security-context aggregation semantics

- **Chosen:** a workload PASSES only when it runs non-root AND is non-privileged
  AND has a read-only root filesystem AND forbids privilege escalation AND uses
  no host namespace. Container-level settings override pod-level;
  `allowPrivilegeEscalation` defaults to `true` when unset (matching Kubernetes
  admission semantics — absence is permissive); a pod template with zero modeled
  containers is treated as unhardened (cannot assert container-level controls).
- **Rationale:** the connector emits a deterministic PASS/FAIL/INCONCLUSIVE
  verdict (like slice 486's storage kind) but the platform evaluator still owns
  the final policy call; conservative defaults avoid false-PASS.
- **Confidence:** medium (the aggregate-across-containers rollup is a judgment
  call; some programs may want per-container records — see revisit list).

## Revisit once in use

1. **Workload → CFG-02 anchor accuracy (D2).** Re-check against the full SCF
   STRM crosswalk once real SOC 2 CC6.x / ISO A.8 control bundles reference this
   kind. CFG-02 vs a more specific container-hardening anchor is the lowest-
   confidence anchor call.
2. **Per-container vs per-workload workload records (D6).** If auditors want to
   point at the specific container that runs privileged, the aggregate flag is
   too coarse — consider a `containers[]` breakdown or per-container records.
3. **Pagination (threat-model D).** v0 reads the first bounded page
   (`limit=500`) per endpoint. A very large cluster (>500 bindings / workloads)
   truncates silently. Add `continue`-cursor pagination when a real cluster hits
   the cap.
4. **`runAsUser: 0` without `runAsNonRoot: false`.** The connector trusts
   `runAsNonRoot`; a workload that sets `runAsUser: 0` but omits `runAsNonRoot`
   is reported as non-root-unenforced (FAIL), which is correct, but the reason
   string could be more specific. Revisit when operators report confusion.
5. **In-cluster managed-identity token refresh.** v0 reads the projected
   ServiceAccount token once at run start. A long-running invocation past the
   token TTL would 401; acceptable for a bounded pull pass, revisit if run
   durations grow.

## Spillover filed

See `docs/issues/523-*.md` … `526-*.md` (the natural follow-ons named in the
slice: NetworkPolicy evidence; Pod-Security-Standards admission-config evidence;
Secret-inventory metadata-only evidence; watch-based event-driven profile via the
Kubernetes audit log). Filed as docs only — NOT implemented in this slice
(P0-487-7).

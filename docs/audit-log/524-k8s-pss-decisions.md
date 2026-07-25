# 524 — Kubernetes Pod-Security-Standards admission-config evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shape, the SCF anchor, the scope-minimum — confirming NO new
ClusterRole rule — and THE load-bearing call: the label-filter / structural
over-collection boundary that keeps every non-PSS namespace label, all
annotations, and all pod/secret material out of the record). It does NOT block
merge; the maintainer iterates post-deployment from the "Revisit once in use"
list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no product-behavior bug surfaced during the
  slice — the build-time corrections were the expected fourth-kind authoring
  fixes, caught at the unit/build tier as I threaded the new collector).
- **detection_tier_target:** none.

The build-time adjustments were the expected consequence of adding a fourth
evidence kind to the slice-487 connector, all authoring fixes rather than
product-behavior defects:

- the `cmd` seam harness gained a `pssScan` override + each existing
  completing-skip-test that does not seam PSS now sets `--skip-pss`, so the
  on-by-default PSS pull never reaches the live HTTP client in those seam tests
  (mirrors how slice 487's workload and slice 523's netpol pulls are skipped in
  the narrower seam tests);
- the register integration test's `supported_kinds` count moved 3 → 4 and the
  round-trip test pushes a fourth record (PSS) end-to-end;
- `gofmt` re-aligned the client-test `banned []string` comment block on first
  write (no semantic change).

## Decisions made

### D1 — A SEPARATE sibling kind `k8s.pod_security_admission.v1`, mirroring slices 487/523

- **Options:** (a) a new sibling kind on the existing `atlas-k8s` binary;
  (b) fold PSS facts into the workload kind (rejected — the workload kind reports
  the _actual_ per-workload security context; PSS is the _enforced_ admission
  config at a different altitude and a different resource — the namespace, not
  the workload); (c) a brand-new connector binary (rejected — same source, same
  ServiceAccount, same read-only API, and the SAME already-held `namespaces`
  grant).
- **Chosen:** (a). A new `connectors/k8s/internal/pss` collector + a new
  `k8s.pod_security_admission.v1` kind on the SAME `atlas-k8s` binary, registered
  alongside the three existing kinds. This is the slice-487/523 pattern verbatim:
  `pss.Assess` mirrors `netpol.Assess`/`workload.Inspect`, `pss.Client` mirrors
  `netpol.Client`, `idem.PSSAdmissionKey` mirrors `NetpolCoverageKey`,
  `buildPSSRecord` mirrors `buildNetpolRecord`.
- **Rationale:** "is hardening enforced at admission?" is a control question
  (configuration-baseline / CFG-02) distinct from the actual per-workload posture
  the workload kind reports — but it is the same source (the Kubernetes API, the
  same read-only ServiceAccount, and crucially **no additional grant**), so a
  sibling kind on the same binary is the minimum-surface choice. It is the
  enforced complement to the actual-posture kind: an auditor reads both side by
  side ("are pods hardened?" + "is that hardening enforced at admission?").

### D2 — THE load-bearing call: namespace PSS LABELS ONLY — never other labels, never annotations, never pod/secret material

- **Decision:** the PSS HTTP client models only `metadata.name` +
  `metadata.labels`, and within `metadata.labels` `reduce()` reads **only** the
  six `pod-security.kubernetes.io/*` keys (`enforce`, `enforce-version`, `audit`,
  `audit-version`, `warn`, `warn-version`). `metadata.annotations`, `spec`,
  `status`, ownerReferences, managedFields, and every other namespace field have
  no matching struct field, so Go's JSON decoder discards them — they never enter
  Go memory. Every label OTHER than the six PSS keys is read into the transient
  decode map and then dropped on the floor by `reduce()` (never copied into a
  record-bound field). The `RawNamespace` / `Admission` structs have **no field**
  that could carry a pod spec, a secret, container env, or an arbitrary namespace
  label/annotation.
- **Mechanism (HOW non-PSS labels can't leak):** there is a single chokepoint —
  `reduce()` in `client.go`. It indexes `labels[labelEnforce]` etc. by the exact
  six PSS keys; an arbitrary label is unreachable because there is no struct field
  to receive it and no code path that iterates the label map. A malformed PSS
  level value (e.g. `enforce: bogus`) is collapsed to the unset sentinel by
  `normalizeLevel` rather than recorded verbatim.
- **Guards:**
  - `TestClient_OnlyPSSLabelsReachRecord` (the load-bearing label-filter test):
    serves a namespace carrying unrelated labels (`team: confidential-team-label`,
    `some-other-label: super-secret-label-value`, the system
    `kubernetes.io/metadata.name`) AND annotations (a kubectl
    `last-applied-configuration` blob with embedded `annotation-secret-blob`, an
    `owner-email: pii-leak@example.test`) plus a `spec.finalizers` + `status.phase`,
    runs the full collect → assess path, and asserts NONE of those strings appears
    in any `RawNamespace` or `Admission` record-bound field.
  - `TestStruct_PSSLabelsOnly_NoOverCollectionFields` (the structural reflection
    guard, mirrors slice 520/523): reflects over every field name of
    `RawNamespace` + `Admission` and FAILS the build if any field name hints at a
    pod-spec / secret / annotation / arbitrary-label surface.
  - the integration `TestEmittedRecords_NoSecretsOrConfigValues` applies a
    PSS-only top-level payload allow-list.

### D3 — Scope-minimum: NO new ClusterRole rule — reuses the existing `namespaces` get/list grant

- **Decision:** the collector reads only core `namespaces` (`get,list`), a rule
  the base slice-487 ClusterRole already holds
  (`k8sauth.DocumentedClusterRole()`'s core/namespaces rule). PSS configuration
  lives entirely in `pod-security.kubernetes.io/*` labels on the Namespace
  object, which the existing namespace read already returns — so **no new
  ClusterRole rule, no new verb, no new resource** is required. The least-privilege
  test (`TestDocumentedClusterRole_IsLeastPrivilege`) passes UNCHANGED (the rule
  count and verb set are untouched); only the namespaces rule's `Gates`
  documentation string was extended to name the PSS kind, and `atlas-k8s
permissions` / the README render it.
- **Why this matters (scope-expansion flag):** adding a ClusterRole rule changes
  the deploy RBAC and would have been a scope expansion to flag. It was not
  needed — confirmed by reading `k8sauth.go` before writing the collector. This is
  the cheapest possible new evidence surface: a fourth kind at **zero** additional
  cluster privilege.

### D4 — Verdict: PASS = enforce ∈ {baseline, restricted}; FAIL = unenforced or privileged-only

- **Decision:** the per-namespace verdict is driven by the `enforce` label only.
  `PASS` when the namespace ENFORCES a hardened level (`baseline` or
  `restricted`); `FAIL` when there is no `enforce` label (unenforced — recorded
  honestly with `configured=false`, not dropped) or when `enforce` is only
  `privileged`. `audit` and `warn` modes are reported in the record (and in the
  `configured` flag) but do NOT drive the verdict — they observe / warn, they do
  not block admission, so they cannot substitute for enforcement.
- **Rationale:** the control question CFG-02 answers is "is the hardened baseline
  enforced AT ADMISSION?" Audit/warn produce a log line or a user warning but
  still admit the pod, so a namespace that only audits/warns is honestly `FAIL`
  for _enforcement_ (its reason string says so explicitly). The connector emits a
  descriptive verdict; the platform evaluator owns the final (control, namespace)
  call (e.g. whether `baseline` enforcement suffices for a given framework scope,
  or whether `restricted` is required).
- **Honest unenforced recording:** a namespace with zero PSS labels is RECORDED
  (with all levels unset, `configured=false`, `FAIL`, reason "no
  Pod-Security-Standards admission labels (unenforced)") rather than silently
  dropped — the auditor needs the negative result ("these namespaces enforce
  nothing") as much as the positive.

### D5 — SCF anchor: CFG-02 (Secure Baseline Configurations)

- **Decision:** `x-default-scf-anchors=["CFG-02"]` — the same anchor the
  actual-posture `k8s.workload_security_context.v1` kind carries, because PSS
  admission is the _enforcement_ of the secure baseline that the workload kind
  _measures_. A single anchor (not a pair) — CFG-02 is the precise control;
  admission enforcement is squarely a configuration-baseline control, not a
  network or IAM one.
- **Caveat:** per the schema convention, `x-default-scf-anchors` are default
  mapping hints flagged for maintainer recheck (OQ #9). The default `--pss-control`
  flag is `scf:CFG-02` to match.

## Bounded / DoS guard

- A bounded namespace page read (`limit=500`, the slice-487/523 idiom; cursor
  pagination for a >500-namespace cluster is the documented follow-on) plus a
  per-run namespace cap (`pss.maxNamespaces=5000`) in `Assess`, asserted by
  `TestAssess_BoundedByCap`. A pathological / hostile namespace list is truncated
  rather than blowing up memory.

## Spillover (band 652–659)

Out-of-scope surfaces named in the slice as follow-ons (NOT implemented here):
the cluster's `AdmissionConfiguration` file (out of API reach), validating /
mutating admission webhooks, and third-party policy engines (OPA/Gatekeeper,
Kyverno). These read different (often cluster-scoped or CRD) resources and would
need new ClusterRole rules — a genuine scope expansion. Filed as
`docs/issues/652-k8s-admission-webhook-policy-engine-evidence.md` (parent #524).

## Revisit once in use

- Whether operators want the `audit`/`warn` modes to influence the verdict (some
  programs treat "audit at restricted" as a meaningful interim posture). Today
  they are reported but verdict-neutral.
- Whether to surface a namespace-count rollup (how many namespaces enforce
  restricted vs baseline vs nothing) as a derived metric — likely a board/metrics
  concern, not a connector concern.
- Cursor pagination for >500-namespace clusters (shared follow-on with the
  netpol/workload bounded-page limitation).

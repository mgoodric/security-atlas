# Slice 622 — K8s CNI-native NetworkPolicy (Cilium / Calico) coverage — decisions log

JUDGMENT slice. Claude made the subjective calls below using best-reasoned,
pattern-matched judgment (slice 523 over-collection guard, slice 621 shared
`k8slist` reader, the connector-pattern memory notes). The maintainer iterates
post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during the slice. The only red→green was a test-expectation
ordering error in my own new test (`Sources` sort order), caught immediately by
`go test` at the unit tier — not a defect in shipped code.

---

## Decisions made

### D1 — CRD presence detection via API discovery probe (not a hard dependency)

- **Options considered:** (a) require the CNI CRD group to exist and fail when
  absent; (b) attempt the list and treat a 404 as "absent"; (c) probe API
  discovery (`GET /apis/<group>/<version>`) first, then list only when present.
- **Chosen:** (c). `cniReader.present` does a read-only `GET /apis/cilium.io/v2`
  / `GET /apis/crd.projectcalico.org/v1` via the new `k8slist.Reader.Probe`
  primitive; 200 ⇒ installed, 404 ⇒ absent (contribute nothing, no hard-fail),
  any other status ⇒ real error. The subsequent list ALSO tolerates a 404
  (`listOrAbsent`) so a kind removed between probe and list, or a CNI version
  that does not serve the cluster-wide kind, degrades to "no policies" rather
  than erroring.
- **Rationale:** the spec mandates "detect by CRD presence — do not hard-fail
  when absent" (AC-1). Discovery is the cheapest, least-privilege presence check
  — it needs no `apiextensions`/`customresourcedefinitions` grant (probing the
  served group path is covered by the same read-only plane). A Cilium-only
  cluster never errors on the absent Calico group and vice versa.
- **Confidence:** high.

### D2 — Cilium default-deny field mapping

- **Mapping:** a `CiliumNetworkPolicy` / `CiliumClusterwideNetworkPolicy`
  establishes default-deny in a direction when **(a)** its `spec.endpointSelector`
  is empty (`{}` — selects every endpoint in scope) AND **(b)** the direction's
  key (`ingress` / `egress`) is **present** in the spec AND **(c)** that key's
  rule array is empty (zero allow entries). Governed directions are derived from
  key presence (Cilium has no `policyTypes` field): an empty `ingress: []`
  present means "deny all ingress".
- **Conservative under-credit:** we do NOT credit a policy whose endpointSelector
  is non-empty (it narrows to specific endpoints, not the namespace), nor one
  that carries allow rules, nor the `spec.specs[]` array form (its per-spec
  selector is ambiguous to read without decoding rule contents — out of scope of
  the SPEC-metadata-only guard). Better a false-FAIL (operator adds an explicit
  default-deny) than a false-PASS (segmentation credited that does not exist).
- **Rationale:** mirrors the upstream `assessNamespace` shape (empty-selector +
  zero-rule = default-deny) so the verdict logic is unchanged; only the SOURCE of
  the policy differs. The Cilium endpointSelector emptiness reuses the existing
  `apiLabelSelector.isEmpty()` (same Kubernetes label-selector shape).
- **Confidence:** medium — the `specs[]` form and Cilium L7/DNS rules are
  deliberately not credited; revisit against a real Cilium cluster.

### D3 — Calico default-deny field mapping

- **Mapping:** a Calico `NetworkPolicy` / `GlobalNetworkPolicy` establishes
  default-deny in a direction when **(a)** `spec.selector` is all-endpoints (`""`
  or `all()`) AND **(b)** `spec.types` includes the direction AND **(c)** the
  direction's rule array is empty. Calico's `spec.types` is the authoritative
  governed-direction list (it mirrors upstream `policyTypes`), so we use it
  directly rather than deriving from key presence.
- **Conservative under-credit:** `calicoSelectsAll` returns true ONLY for the
  empty selector and the explicit `all()`. Any other selector DSL expression
  (`app == 'x'`, `has(role)`) is a narrower match and is NOT all-pods — it cannot
  establish a namespace-wide default-deny. Calico's selector is a string DSL; we
  read only its all-vs-narrow disposition, never parse the expression contents.
- **Rationale:** matches Calico's documented default-deny idiom (an `all()`
  policy with `types: [Ingress, Egress]` and no rules). Reading the selector as
  an opaque all-vs-narrow string keeps us inside the SPEC-metadata-only guard.
- **Confidence:** medium — the Calico selector DSL has richer forms (label
  ranges, `&&`/`||`) that a real deployment may use to express "all in a tier";
  those read as narrow today. Revisit against a real Calico cluster.

### D4 — Cluster-wide CNI policies fold into every namespace

- **Decision:** `CiliumClusterwideNetworkPolicy` and Calico `GlobalNetworkPolicy`
  are cluster-scoped (no namespace). When such a policy is all-endpoints +
  zero-rule for a direction, a COPY of its reduced summary is folded into EVERY
  real namespace so each namespace's per-namespace default-deny assessment credits
  it. The namespaced kinds fold only into their own `metadata.namespace`.
- **Rationale:** the coverage record is per-namespace (one record per namespace
  per run). A cluster-wide default-deny genuinely protects every namespace, so the
  assessment must reflect that everywhere — otherwise a cluster running a single
  `GlobalNetworkPolicy: default-deny` still reads as a wall of false-FAILs.
- **Confidence:** high.

### D5 — Additive schema field, NO version bump (stay at 1.0.0)

- **Options considered:** (a) bump to `k8s.networkpolicy_coverage.v2` / schema
  `2.0.0`; (b) add the source fields additively to the existing `1.0.0` schema.
- **Chosen:** (b). Added an optional top-level `sources` array and an optional
  per-policy `source` enum to the existing `1.0.0` schema. Both are **optional**
  (not in `required`), so every slice-523 record that predates this field still
  validates; an absent value implies upstream `networking.k8s.io`.
- **Rationale:** the existing schema sets `additionalProperties: false`, so the
  new keys had to be declared — but declaring an OPTIONAL property is a
  backward-compatible schema change (old records validate, new records validate).
  The evidence-kind contract (the four required keys, the verdict semantics) is
  unchanged; only descriptive metadata is added. A version bump would force a new
  evidence_kind registration and a re-mapping with no semantic benefit. This
  matches the project's additive-field discipline (CLAUDE.md migration rule:
  additive over destructive).
- **Confidence:** high.

### D6 — CRD-absence fallback behavior

- **Decision:** when a CNI CRD is absent (404 on discovery), the collector
  contributes ZERO policies for that source and the assessment is byte-identical
  to the slice-523 upstream-only result. No record carries a CNI `source`, and
  `sources` is `["networking.k8s.io"]` (or omitted when the namespace has no
  policies at all). No error, no log noise.
- **Rationale:** AC-1 ("do not hard-fail when absent"). The common case is a
  cluster running exactly one CNI (or none with policy CRDs); the collector must
  not penalize the upstream-only path.
- **Confidence:** high.

---

## Revisit once in use

1. **Cilium `specs[]` rule form (D2).** A `CiliumNetworkPolicy` may express rules
   under `spec.specs[]` (a list) rather than top-level `spec.ingress/egress`. We
   currently do not credit that form as default-deny. Re-check against a real
   Cilium cluster whether the `specs[]` default-deny idiom is common enough to
   warrant SPEC-metadata-only support (it would need a per-spec empty-selector +
   empty-rules read that stays inside the no-leak guard).
2. **Calico selector DSL richness (D3).** Confirm whether real Calico
   deployments commonly express "all endpoints in a tier/namespace" via selector
   expressions other than `""`/`all()` (e.g. `projectcalico.org/namespace ==
'<ns>'`). If so, those read as narrow (false-FAIL) today; consider a small
   allow-list of known all-equivalent expressions.
3. **CNI version skew (D1).** Probes pin `cilium.io/v2` and
   `crd.projectcalico.org/v1`. If a future CNI release serves a different group
   version (e.g. `cilium.io/v2alpha1` for a kind), the probe will 404 and the
   kind will be silently skipped. Re-check the served group-versions when
   bumping the supported CNI matrix.
4. **L7 / DNS / node policies.** Cilium L7 (HTTP), DNS, and node policies are out
   of scope — they are not segmentation default-deny signals. Confirm no operator
   expects them folded into this kind (they would be a separate evidence kind if
   ever wanted).
5. **`sources` consumption by the evaluator.** This slice emits the source set
   but no evaluator reads it yet. When the platform-side network-segmentation
   evaluation lands, confirm it uses `sources` / per-policy `source` to reason
   about CNI vs upstream coverage as intended by AC-2.

---

## Anti-criteria honored (P0)

- **Push-only wire (invariant #3):** unchanged — the connector reads the cluster
  and pushes via the existing SDK `Push`. No new inbound platform API.
- **No write verb / `secrets` / wildcard in the ClusterRole (AC-3):** the two new
  rules grant `get,list` only on the named CNI CRD resources;
  `TestDocumentedClusterRole_IsLeastPrivilege` iterates ALL rules and rejects any
  non-read verb, any `secrets`, and any `*`.
- **No pod / Secret / traffic data — CRD SPEC metadata only (AC-4):** the CNI
  decode targets model only name + selector-emptiness + direction + rule COUNT;
  `ingress`/`egress` are opaque `json.RawMessage` arrays (counted, never
  decoded). `TestClient_CNINeverMaterializesPeerOrSelectorPayload` proves no
  selector label, peer endpoint, CIDR, or port escapes into a record, and the
  cmd-layer integration allow-list test bounds the emitted payload keys.

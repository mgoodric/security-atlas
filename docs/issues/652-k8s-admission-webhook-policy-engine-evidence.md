# 652 — Kubernetes connector: admission-webhook + policy-engine evidence (PSS follow-on)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + a genuine ClusterRole expansion)
**Status:** `blocked` (depends on #524 — PSS admission-config evidence — merged first)
**Parent:** #524

## Narrative

Slice 524 shipped the **namespace PSS label** half of admission-time enforcement
evidence (`k8s.pod_security_admission.v1`): which namespaces carry the
`pod-security.kubernetes.io/*` labels and at which level, read via the existing
`namespaces` get/list grant (no new ClusterRole rule). It deliberately scoped OUT
the other admission-enforcement surfaces, which read different resources and
require new RBAC:

- **Validating / mutating admission webhooks** —
  `admissionregistration.k8s.io/v1` `validatingwebhookconfigurations` +
  `mutatingwebhookconfigurations`. Proves a policy webhook is wired in (which
  resources/operations it intercepts, fail-open vs fail-closed, namespaceSelector
  scope) without reading the webhook's decision logic.
- **Third-party policy engines** — OPA/Gatekeeper (`constraints` +
  `constrainttemplates` CRDs) and Kyverno (`policies` + `clusterpolicies` CRDs).
  Proves which admission policies are enforced cluster-wide.

These are the "is hardening enforced beyond namespace PSS labels?" surfaces an
auditor asks about once PSS labels are covered.

## Scope discipline

CONFIGURATION metadata only — webhook wiring + policy existence/enforcement-action,
never the webhook's TLS client key, the policy's Rego/CEL decision logic body, or
any intercepted-object payload. Mirror the slice-487/523/524 structural
over-collection guard (a reflection test pinning the record struct to metadata
fields).

## Scope EXPANSION to flag (unlike #524)

Unlike #524 (which reused the held `namespaces` grant), this slice **adds new
read-only ClusterRole rules**:

- `admissionregistration.k8s.io: validatingwebhookconfigurations,
mutatingwebhookconfigurations` — verbs `get,list`
- (if Gatekeeper/Kyverno support is in scope) the relevant CRD groups —
  verbs `get,list`

Each new rule must stay `get,list`-only, never `secrets`, never a write verb,
never a wildcard — and the `DocumentedClusterRole` least-privilege test + the
`atlas-k8s permissions` render + the README must be updated together.

## Acceptance criteria (sketch — refine when grabbed)

- [ ] A new collector under `connectors/k8s/` following the slice-487 pattern
      (narrow `API` interface, faked in tests).
- [ ] New evidence kind(s) for webhook config (and optionally
      Gatekeeper/Kyverno policy) + schema with `x-default-scf-anchors` (candidate:
      `CFG-02`), registered in `DefaultSeed`.
- [ ] The new read-only ClusterRole rule(s) documented + the least-privilege test
      updated (this is the deliberate, flagged scope expansion).
- [ ] Structural over-collection guard: webhook/policy CONFIG metadata only —
      never the webhook client key, the policy decision-logic body, or an
      intercepted payload.
- [ ] Push only (invariant #3); `profiles_supported=[pull]`; honest interval.

## Dependencies

- **#524** (PSS admission-config evidence) — the collector pattern + the
  admission-enforcement evidence framing.

## Notes

The `AdmissionConfiguration` file (the API-server-level PSS default config) is
genuinely out of Kubernetes-API reach (it lives on the control-plane host
filesystem) and is NOT covered by this slice either — it would need a different
source entirely (node/file access) and is likely never in scope for an API-only
connector.

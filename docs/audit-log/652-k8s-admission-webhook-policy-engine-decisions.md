# Slice 652 — K8s admission-webhook + policy-engine evidence — decisions log

JUDGMENT slice. Claude made the subjective calls below, recorded them here, and
shipped when CI was green (per the JUDGMENT-slice process; this does NOT touch the
product's runtime AI-assist boundary).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The one pre-PR adjustment — the Gatekeeper
wildcard-grant tension — was a design call resolved before any code shipped, not a
defect caught at a tier.)

## Decisions made

### D1 — Kind shape: TWO separate kinds, not one combined kind. (confidence: high)

Options: (a) one combined `k8s.admission_enforcement.v1` carrying both webhook
configs and policy-engine policies; (b) two sibling kinds —
`k8s.admission_webhook.v1` + `k8s.admission_policy.v1`.

Chose (b). A webhook configuration and a policy-engine policy have structurally
disjoint field sets: a webhook has `failurePolicy` / `sideEffects` / selector
scope / intercepted resources+operations / a service dispatch ref; a policy has an
engine / scope / kind / enforcement-action. A single combined schema would be a
union with most fields optional-and-mutually-exclusive — the exact shape the
slice-488 → 533 sibling-split precedent rejected ("a detection rule carries fields
the alert-config kind lacks, so it gets its own kind"). Two narrow kinds keep each
schema `additionalProperties: false` and each evaluator query simple. Both anchor
to the same `CFG-02`, so the crosswalk cost of two kinds is zero.

### D2 — Gatekeeper/Kyverno: IN scope for v0 (not spilled). (confidence: high)

The spec recommended including them via the slice-622 CRD-presence probe. Done:
the policy-engine collector probes `templates.gatekeeper.sh/v1` and `kyverno.io/v1`
group discovery (200 = present, 404 = absent, never a hard-fail) and reads the
present engine's policies. Absence is silent — a cluster with neither engine
yields zero policy records and no error (proven by
`TestPolicyClient_AbsentEngineNotAnError`). No spillover slice was needed.

### D3 — Webhook metadata field set. (confidence: high)

Per webhook entry the record carries: `webhook_kind` (validating/mutating),
`config_name`, `webhook_name`, `failure_policy` + derived `fail_closed`,
`side_effects`, `has_namespace_selector` / `has_object_selector` (the **presence**
of a selector — the scope-narrowing signal — never the match expressions, which can
encode tenant-identifying labels), `target_service` (the `namespace/name` dispatch
ref, never the raw URL), and the deduped+sorted `intercepted_resources` /
`intercepted_operations` sets. The `fail_closed` derivation (`failurePolicy == Fail`)
is the load-bearing auditor signal ("is this policy fail-open?"). An unset or
malformed `failurePolicy` normalizes to unset and reads as **not** fail-closed
(recorded honestly rather than asserting the Kubernetes default).

### D4 — Policy metadata field set. (confidence: medium)

Per policy the record carries: `engine` (gatekeeper/kyverno), `policy_name`,
`scope` (cluster/namespaced) + `namespace` when namespaced, `policy_kind`, and
`enforcement_action` + derived `enforcing`. `enforcing` is true only for actions
that BLOCK admission (`enforce` / `deny`); `audit` / `warn` / `dryrun` / unset read
as not-enforcing. Confidence is medium because Kyverno moved
`validationFailureAction` from a top-level field to per-rule in newer versions; v0
reads the top-level field (the cluster-wide default the auditor cares about) and
will under-report a policy that only sets the action per-rule. See revisit list.

### D5 — Gatekeeper reads the ConstraintTemplate CATALOG, not per-constraint enforcement. (confidence: medium)

The natural richer read — each Gatekeeper **Constraint**'s `spec.enforcementAction`
— is impossible without a **wildcard resource grant** over the
`constraints.gatekeeper.sh` group, because Gatekeeper renders one dynamic CRD kind
per installed ConstraintTemplate and those kinds cannot be named statically in a
ClusterRole. The slice's HARD anti-criterion is "NEVER a wildcard". So v0 reads the
**static, named** `templates.gatekeeper.sh constrainttemplates` resource (proving
WHICH policies are DEFINED, with each template's rendered constraint kind) and
leaves the Gatekeeper `enforcement_action` unset. Kyverno's resources are static
and named, so its enforcement action is read directly. This is the wildcard-free
choice; the cost is that Gatekeeper coverage proves definition, not per-constraint
enforcement. Recorded as the most load-bearing revisit item.

### D6 — Anchor: CFG-02, verified present in the bundled SCF catalog. (confidence: high)

The spec candidate was `CFG-02`. Verified present in
`migrations/fixtures/scf-sample.json` (`grep -c CFG-02` → 1) BEFORE using it, so the
slice-068 anchor drift guard will not dangle. CFG-02 is the **same** anchor the
sibling `k8s.pod_security_admission.v1` (slice 524) and `k8s.workload_security_context.v1`
kinds use — admission webhooks + policy engines are the same configuration-hardening
/ enforced-admission surface, so sharing the anchor is correct, not a stretch.

### D7 — A new public `k8slist` method was considered and REJECTED. (confidence: high)

An early draft of the Gatekeeper path read a group-version API-discovery document
to enumerate the dynamic constraint kinds, which needed a new `Reader.GetJSON`
single-object method on the shared slice-621 `k8slist` reader. When D5 settled on
the wildcard-free ConstraintTemplate-catalog read, that discovery path (and the new
method) became unnecessary and was removed — the collector now uses only the
existing `k8slist.ListAll` + `Reader.Probe`. The shared reader is unchanged.

### D8 — Push-only, profiles_supported=[pull], honest interval. (confidence: high)

No change to the platform-side wire (invariant #3): the connector pushes via the
existing SDK client; `profiles_supported` stays `[pull]`; the interval is named
"24h (recommended; operator-scheduled — NOT continuous monitoring)". The two new
kinds run by default in `run` (no opt-in flag — unlike secret-inventory, these read
no Secret material); `--skip-admission-webhooks` / `--skip-admission-policies`
disable them.

## Revisit once in use

- **D5 (Gatekeeper per-constraint enforcement).** Once a real Gatekeeper-using
  auditor needs the per-constraint `enforcementAction` (not just "the policy is
  defined"), revisit whether a wildcard-free discovery path is acceptable — e.g. a
  bounded API-discovery enumeration of the served constraint kinds, granting
  `get,list` per discovered kind, still avoids a literal `*` resource. File a
  follow-on slice rather than widening the v0 grant.
- **D4 (Kyverno per-rule action).** Confirm against a real newer-Kyverno cluster
  whether the top-level `validationFailureAction` is still populated, or whether the
  collector under-reports a policy that sets the action only per-rule. If the
  per-rule form dominates in practice, add per-rule action aggregation (still
  metadata, no rule body).
- **D3 (selector presence vs. shape).** Re-check whether auditors want more than the
  boolean selector-presence flags — e.g. the namespace NAMES a webhook scopes to.
  That is a real over-collection trade-off (selector match expressions can leak
  tenant labels); keep it a deliberate decision, not a default.
- **D6 (anchor).** Re-check CFG-02 vs. a finer admission-specific anchor once the
  SCF catalog grows an admission-control-specific control; CFG-02 is the closest
  real anchor today.

## Confidence summary

| Decision                              | Confidence |
| ------------------------------------- | ---------- |
| D1 two kinds                          | high       |
| D2 Gatekeeper/Kyverno in scope        | high       |
| D3 webhook fields                     | high       |
| D4 policy fields                      | medium     |
| D5 Gatekeeper catalog-only            | medium     |
| D6 CFG-02 anchor                      | high       |
| D7 no new k8slist method              | high       |
| D8 push-only / pull / honest interval | high       |

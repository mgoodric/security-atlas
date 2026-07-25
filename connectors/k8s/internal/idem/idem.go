// Package idem derives idempotency keys for the Kubernetes connector.
//
// Two emitters, two key shapes (parallel to slice 004 / slice 486 idem
// packages). Both collapse same-resource pushes within the same hour into the
// same key, so a re-run within the hour does not double-write the ledger.
//
//   - RBACBindingKey:     sha256("k8s.rbac_binding|<scope>/<namespace>/<name>|<hour>")
//   - WorkloadKey:        sha256("k8s.workload_security_context|<kind>/<namespace>/<name>|<hour>")
//   - NetpolCoverageKey:  sha256("k8s.networkpolicy_coverage|<namespace>|<hour>")
//   - PSSAdmissionKey:    sha256("k8s.pod_security_admission|<namespace>|<hour>")
//   - SecretInventoryKey: sha256("k8s.secret_inventory|<namespace>/<name>|<hour>")
//   - AdmissionWebhookKey: sha256("k8s.admission_webhook|<kind>/<config>/<webhook>|<hour>")
//   - AdmissionPolicyKey:  sha256("k8s.admission_policy|<engine>/<namespace>/<name>|<hour>")
//
// Anti-criterion: every push from this connector derives its idempotency_key
// here. The cmd layer never invents one ad-hoc and never pushes with an empty
// key.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// RBACBindingKey returns the idempotency key for one binding. scope/namespace/
// name together uniquely identify a binding across the cluster. Truncated to the
// hour in UTC so two runs within the same hour produce identical keys.
func RBACBindingKey(scope, namespace, name string, observedAt time.Time) string {
	return hashKey("k8s.rbac_binding", scope+"/"+namespace+"/"+name, observedAt)
}

// WorkloadKey returns the idempotency key for one workload security context.
func WorkloadKey(kind, namespace, name string, observedAt time.Time) string {
	return hashKey("k8s.workload_security_context", kind+"/"+namespace+"/"+name, observedAt)
}

// NetpolCoverageKey returns the idempotency key for one namespace's
// NetworkPolicy coverage assessment. The namespace uniquely identifies a
// coverage record (one record per namespace per run).
func NetpolCoverageKey(namespace string, observedAt time.Time) string {
	return hashKey("k8s.networkpolicy_coverage", namespace, observedAt)
}

// PSSAdmissionKey returns the idempotency key for one namespace's
// Pod-Security-Standards admission assessment. The namespace uniquely identifies
// a PSS record (one record per namespace per run).
func PSSAdmissionKey(namespace string, observedAt time.Time) string {
	return hashKey("k8s.pod_security_admission", namespace, observedAt)
}

// SecretInventoryKey returns the idempotency key for one Secret's metadata
// inventory record (slice 525). namespace/name together uniquely identify a
// Secret across the cluster; one record per Secret per run.
func SecretInventoryKey(namespace, name string, observedAt time.Time) string {
	return hashKey("k8s.secret_inventory", namespace+"/"+name, observedAt)
}

// AdmissionWebhookKey returns the idempotency key for one admission-webhook
// entry (slice 652). kind/config/webhook together uniquely identify a webhook
// entry across the cluster; one record per webhook entry per run.
func AdmissionWebhookKey(kind, config, webhook string, observedAt time.Time) string {
	return hashKey("k8s.admission_webhook", kind+"/"+config+"/"+webhook, observedAt)
}

// AdmissionPolicyKey returns the idempotency key for one policy-engine policy
// (slice 652). engine/namespace/name together uniquely identify a policy; one
// record per policy per run. namespace is empty for cluster-wide policies.
func AdmissionPolicyKey(engine, namespace, name string, observedAt time.Time) string {
	return hashKey("k8s.admission_policy", engine+"/"+namespace+"/"+name, observedAt)
}

func hashKey(prefix, id string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(prefix + "|" + id + "|" + hour))
	return hex.EncodeToString(sum[:])
}

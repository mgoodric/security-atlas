// Package idem derives idempotency keys for the Kubernetes connector.
//
// Two emitters, two key shapes (parallel to slice 004 / slice 486 idem
// packages). Both collapse same-resource pushes within the same hour into the
// same key, so a re-run within the hour does not double-write the ledger.
//
//   - RBACBindingKey:     sha256("k8s.rbac_binding|<scope>/<namespace>/<name>|<hour>")
//   - WorkloadKey:        sha256("k8s.workload_security_context|<kind>/<namespace>/<name>|<hour>")
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

func hashKey(prefix, id string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(prefix + "|" + id + "|" + hour))
	return hex.EncodeToString(sum[:])
}

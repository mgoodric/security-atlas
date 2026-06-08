// Package idem derives idempotency keys for the Azure connector.
//
// Three emitters, three key shapes (parallel to slice 004 / slice 045 idem
// packages). All collapse same-resource pushes within the same hour into the
// same key, so a re-run within the hour does not double-write the ledger.
//
//   - EntraRoleAssignmentKey: sha256("azure.entra_role_assignment|<assignment_id>|<hour>")
//   - StorageAccountKey:      sha256("azure.storage_account_config|<account_id>|<hour>")
//   - AKSClusterConfigKey:    sha256("azure.aks_cluster_config|<cluster_id>|<hour>")
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

// EntraRoleAssignmentKey returns the idempotency key for one (assignment_id,
// observed_at) pair. Truncated to the hour in UTC so two runs within the same
// hour produce identical keys.
func EntraRoleAssignmentKey(assignmentID string, observedAt time.Time) string {
	return hashKey("azure.entra_role_assignment", assignmentID, observedAt)
}

// StorageAccountKey returns the idempotency key for one (account_id,
// observed_at) pair.
func StorageAccountKey(accountID string, observedAt time.Time) string {
	return hashKey("azure.storage_account_config", accountID, observedAt)
}

// AKSClusterConfigKey returns the idempotency key for one (cluster_id,
// observed_at) pair.
func AKSClusterConfigKey(clusterID string, observedAt time.Time) string {
	return hashKey("azure.aks_cluster_config", clusterID, observedAt)
}

func hashKey(prefix, id string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte(prefix + "|" + id + "|" + hour))
	return hex.EncodeToString(sum[:])
}

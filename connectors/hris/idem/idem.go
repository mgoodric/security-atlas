// Package idem derives idempotency keys for the HRIS connector family (Rippling +
// BambooHR, slice 491). One key shape across both vendors, parallel to the slice
// 004 / 486 / 487 / 488 / 490 idem packages.
//
//	WorkerLifecycleKey: sha256("hris.worker_lifecycle|<hris>/<worker_id>|<hour>")
//	ManagerHierarchyKey: sha256("hris.manager_hierarchy|<hris>/<worker_id>|<hour>")
//
// The hris + worker_id together uniquely identify a worker; truncating
// observed_at to the UTC hour collapses same-worker re-runs within the hour into
// one ledger row. The manager-hierarchy key (slice 571) keys the SAME way — one
// hierarchy-edge record per worker per hour — but with a distinct kind prefix so
// the two HRIS evidence kinds never collide in the ledger.
//
// Anti-criterion: every push from either HRIS connector derives its
// idempotency_key here. The cmd layer never invents one ad-hoc and never pushes
// with an empty key.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// WorkerLifecycleKey returns the idempotency key for one worker's lifecycle
// record.
func WorkerLifecycleKey(hris, workerID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("hris.worker_lifecycle|" + hris + "/" + workerID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// ManagerHierarchyKey returns the idempotency key for one worker's
// manager-hierarchy edge record (slice 571). Same (hris, worker_id, hour)
// identity as the lifecycle key, distinct kind prefix.
func ManagerHierarchyKey(hris, workerID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("hris.manager_hierarchy|" + hris + "/" + workerID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

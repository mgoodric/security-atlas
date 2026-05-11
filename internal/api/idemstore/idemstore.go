// Package idemstore tracks idempotency keys for Evidence.Push so retries
// return the original receipt. The in-memory implementation grows unbounded
// and has no TTL; both are addressed by the DB-backed successor in slice 013.
package idemstore

import (
	"sync"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// Store is the slice-003 surface. Lookup-or-Insert is the only operation
// callers need.
type Store interface {
	// LookupOrInsert atomically returns (existing, true) if an entry exists
	// for (tenantID, idempotencyKey) and the recorded hash matches; returns
	// (nil, false) on first-write so the caller proceeds to record; returns
	// ErrHashMismatch when the key is reused with different content.
	LookupOrInsert(tenantID, idempotencyKey, hash string, receipt *evidencev1.EvidenceReceipt) (*evidencev1.EvidenceReceipt, bool, error)
}

// ErrHashMismatch indicates the caller reused an idempotency_key with
// different content. Translates to gRPC AlreadyExists.
type ErrHashMismatch struct{ IdempotencyKey string }

func (e *ErrHashMismatch) Error() string {
	return "idemstore: hash mismatch for idempotency_key " + e.IdempotencyKey
}

type entry struct {
	hash    string
	receipt *evidencev1.EvidenceReceipt
}

// InMemory is a thread-safe in-memory store.
type InMemory struct {
	mu      sync.Mutex
	entries map[string]entry // tenant + "/" + key
}

// New returns an empty store.
func New() *InMemory { return &InMemory{entries: map[string]entry{}} }

// LookupOrInsert implements Store.
func (s *InMemory) LookupOrInsert(tenantID, idempotencyKey, hash string, receipt *evidencev1.EvidenceReceipt) (*evidencev1.EvidenceReceipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := tenantID + "/" + idempotencyKey
	if existing, ok := s.entries[k]; ok {
		if existing.hash != hash {
			return nil, false, &ErrHashMismatch{IdempotencyKey: idempotencyKey}
		}
		return existing.receipt, true, nil
	}
	s.entries[k] = entry{hash: hash, receipt: receipt}
	return nil, false, nil
}

// Package connectorregistry tracks live connector registrations. The
// in-memory implementation here is the slice-004 surface; slice 034 swaps
// it for a DB-backed store against the `connectors` table.
package connectorregistry

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrAlreadyRegistered indicates a duplicate Register call. The natural key
// is (tenant_id, name, version, instance_id) — same on the DB-backed
// successor, so the swap stays mechanical.
var ErrAlreadyRegistered = errors.New("connectorregistry: already registered")

// Handle is the metadata view of a registered connector.
type Handle struct {
	ID                string
	TenantID          string
	Name              string
	Version           string
	InstanceID        string
	SupportedKinds    []string
	ProfilesSupported []string
	RegisteredAt      time.Time
}

// Store is the slice-004 surface.
type Store interface {
	Register(tenantID, name, version, instanceID string, supportedKinds, profilesSupported []string) (Handle, error)
	List(tenantID string) []Handle
}

// InMemory is a thread-safe in-memory store.
type InMemory struct {
	mu      sync.Mutex
	handles map[string]Handle // id -> handle
	now     func() time.Time
}

// New returns an empty store. Pass nil for now to use time.Now.
func New(now func() time.Time) *InMemory {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &InMemory{handles: map[string]Handle{}, now: now}
}

// Register inserts a new handle. Server sets ID + RegisteredAt — never the
// caller. Returns ErrAlreadyRegistered if the natural key collides.
func (s *InMemory) Register(tenantID, name, version, instanceID string, supportedKinds, profilesSupported []string) (Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range s.handles {
		if h.TenantID == tenantID && h.Name == name && h.Version == version && h.InstanceID == instanceID {
			return Handle{}, ErrAlreadyRegistered
		}
	}

	id := "conn_" + uuid.NewString()
	handle := Handle{
		ID:                id,
		TenantID:          tenantID,
		Name:              name,
		Version:           version,
		InstanceID:        instanceID,
		SupportedKinds:    append([]string(nil), supportedKinds...),
		ProfilesSupported: append([]string(nil), profilesSupported...),
		RegisteredAt:      s.now(),
	}
	s.handles[id] = handle
	return handle, nil
}

// List returns handles for the supplied tenant, sorted by RegisteredAt
// descending.
func (s *InMemory) List(tenantID string) []Handle {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Handle, 0)
	for _, h := range s.handles {
		if h.TenantID == tenantID {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegisteredAt.After(out[j].RegisteredAt) })
	return out
}

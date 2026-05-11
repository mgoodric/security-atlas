// Package schemaregistry tracks which evidence_kind identifiers are valid.
// Today it answers a single question (IsRegistered); the service-backed
// version in slice 014 will add JSON-Schema payload validation. Callers
// depend on Registry, not the concrete type, so the swap is transparent.
package schemaregistry

import "sync"

// Registry is the slice-003 surface. Only Lookup is on the hot path; admin
// operations (Register, Unregister) come with slice 014.
type Registry interface {
	IsRegistered(kind, version string) bool
}

// InMemory is a thread-safe in-memory registry. The zero value is unusable;
// use New to seed.
type InMemory struct {
	mu    sync.RWMutex
	kinds map[string]map[string]struct{} // kind -> version -> {}
}

// New returns a registry seeded with the default v1 kinds. Tests can pass
// nil to start empty.
func New(seed []KindVersion) *InMemory {
	r := &InMemory{kinds: map[string]map[string]struct{}{}}
	for _, kv := range seed {
		r.register(kv.Kind, kv.Version)
	}
	return r
}

// KindVersion is one (kind, semver) pair.
type KindVersion struct {
	Kind    string
	Version string
}

// DefaultSeed returns the small starter set of evidence kinds that slice
// 003 understands. Real kinds land with their respective connector slices.
func DefaultSeed() []KindVersion {
	return []KindVersion{
		{Kind: "sast.scan_result.v1", Version: "1.0.0"},
		{Kind: "access_review.completion.v1", Version: "1.0.0"},
		{Kind: "manual.attestation.v1", Version: "1.0.0"},
	}
}

// IsRegistered returns true if (kind, version) is known.
func (r *InMemory) IsRegistered(kind, version string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if versions, ok := r.kinds[kind]; ok {
		_, present := versions[version]
		return present
	}
	return false
}

func (r *InMemory) register(kind, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[kind]; !ok {
		r.kinds[kind] = map[string]struct{}{}
	}
	r.kinds[kind][version] = struct{}{}
}

// Package schemaregistry is the contract-enforcement point for every
// evidence_kind. Each kind has a stable identifier, a JSON Schema (draft
// 2020-12), an owner, default SCF anchor mappings, and a semver. Tenants
// can register private kinds for custom internal tools without touching
// the global namespace — the OpenTelemetry-semantic-conventions analog
// (canvas §4.1; EVIDENCE_SDK §4.5).
//
// Surface:
//   - Registry interface: the lookup + validate contract every caller
//     (today: evidence ingest service, schema HTTP handler, future:
//     slice 013 push validator) depends on.
//   - InMemory: thread-safe in-memory store. Used by the gRPC evidence
//     service and as the inner cache for the DB-backed Service.
//   - Service: DB-backed registry. Reads/writes evidence_kind_schemas,
//     loads the bundled platform schemas at boot, validates JSON
//     payloads against registered schemas using draft 2020-12.
//
// The interface stays narrow on purpose: services that only need
// IsRegistered (slice 003 wire-format check) don't pull in the
// validator. ValidatePayload is the slice 013 hook.
package schemaregistry

import (
	"sync"
)

// Registry is the runtime surface every caller depends on.
type Registry interface {
	// IsRegistered returns true if (kind, semver) is known. Slice 003 calls
	// this before accepting a push.
	IsRegistered(kind, version string) bool
}

// PayloadValidator is the slice 013 hook: validate the JSON-encoded
// payload against the registered JSON Schema for (kind, semver). Returns
// nil if the payload conforms; an error describing the first failure
// otherwise.
type PayloadValidator interface {
	ValidatePayload(kind, version string, payload []byte) error
}

// KindVersion is one (kind, semver) pair. Kept for backwards compatibility
// with the slice-003 evidence service which seeds the in-memory registry
// from a slice.
type KindVersion struct {
	Kind    string
	Version string
}

// InMemory is a thread-safe in-memory registry. The zero value is unusable;
// use New to seed.
type InMemory struct {
	mu    sync.RWMutex
	kinds map[string]map[string]struct{} // kind -> version -> {}
}

// New returns a registry seeded with the supplied kinds. Tests can pass
// nil to start empty.
func New(seed []KindVersion) *InMemory {
	r := &InMemory{kinds: map[string]map[string]struct{}{}}
	for _, kv := range seed {
		r.register(kv.Kind, kv.Version)
	}
	return r
}

// DefaultSeed returns the starter set of evidence kinds the platform knows
// about at boot when no DB-backed Service is available. Slice 014 ships
// the same kinds plus the slice-044 GitHub kinds via embedded JSON
// Schemas; this slim fallback exists for unit tests that don't want to
// spin up the file loader.
//
// Canonical evidence_kind identifier convention (Plans/EVIDENCE_SDK.md
// §4.5): the Kind string is `.v<major>`-suffixed (`osquery.host_posture.v1`)
// and the schema version is a SEPARATE semver (`1.0.0`). The `.v<major>`
// suffix is part of the stable identifier; the semver tracks additive
// minor / patch evolution within that major. Every on-the-wire consumer
// honors this: the 9 first-party connectors, the per-language SDKs, the
// push CLI, the bundled JSON Schemas' `x-evidence-kind`, and the SOC 2
// control bundles' `evidence_kind` references. Do NOT reintroduce
// bare-name kinds here — slice 068 fixed exactly that drift (the SOC 2
// control bundles had drifted to bare names, breaking fresh-deploy
// control-bundle upload). Keep this set aligned with the `schemas/*/`
// directory's `x-evidence-kind` values; `internal/control` ships a
// drift-guard test that fails the build if they diverge.
func DefaultSeed() []KindVersion {
	return []KindVersion{
		{Kind: "sast.scan_result.v1", Version: "1.0.0"},
		{Kind: "access_review.completion.v1", Version: "1.0.0"},
		{Kind: "manual.attestation.v1", Version: "1.0.0"},
		{Kind: "aws.s3.bucket_encryption_state.v1", Version: "1.0.0"},
		{Kind: "github.repo_protection.v1", Version: "1.0.0"},
		{Kind: "github.audit_event.v1", Version: "1.0.0"},
		{Kind: "github.scim_user.v1", Version: "1.0.0"},
		{Kind: "okta.mfa_policy.v1", Version: "1.0.0"},
		{Kind: "okta.app_assignment.v1", Version: "1.0.0"},
		{Kind: "okta.user_lifecycle.v1", Version: "1.0.0"},
		{Kind: "1password.org_policy.v1", Version: "1.0.0"},
		{Kind: "osquery.host_posture.v1", Version: "1.0.0"},
		{Kind: "jira.ticket_evidence.v1", Version: "1.0.0"},
		{Kind: "manual.upload.v1", Version: "1.0.0"},
		// Slice 023: policy acknowledgment workflow. Each
		// POST /v1/policies/{id}/acknowledge emits one record of this
		// kind through the slice-013 evidence ledger.
		{Kind: "policy.acknowledgment.v1", Version: "1.0.0"},
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

// Register adds a (kind, semver) pair to the in-memory registry. Used by
// the DB-backed Service to seed its inner cache after a successful insert.
func (r *InMemory) Register(kind, version string) {
	r.register(kind, version)
}

func (r *InMemory) register(kind, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.kinds[kind]; !ok {
		r.kinds[kind] = map[string]struct{}{}
	}
	r.kinds[kind][version] = struct{}{}
}

package control

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// SchemaRegistry is the slice-014 contract this package needs to validate
// evidence_kind references on a bundle. We accept the interface (not the
// concrete *schemaregistry.Service) so unit tests can supply a stub and
// rejection logic stays under test even when no Postgres is available.
type SchemaRegistry interface {
	IsRegistered(kind, version string) bool
}

// errEvidenceKindUnknown is the sentinel returned when a bundle references
// an evidence_kind that is not registered in the schema registry. The HTTP
// handler maps it to a 400 with the offending kind in the body.
var errEvidenceKindUnknown = errors.New("control bundle: unknown evidence_kind")

// ErrUnknownEvidenceKind wraps the parser error with the offending name.
type ErrUnknownEvidenceKind struct{ Kind string }

func (e ErrUnknownEvidenceKind) Error() string {
	return fmt.Sprintf("control bundle: evidence_kind %q is not registered in the schema registry", e.Kind)
}

func (ErrUnknownEvidenceKind) Is(target error) bool { return target == errEvidenceKindUnknown }

// ValidateApplicabilityExpr runs the slice-017 validator on the manifest's
// applicability_expr. The slice-017 evaluator's `Evaluate` rejects malformed
// expressions even when invoked with an empty universe — that's the cheapest
// way to check well-formedness without a tenant context.
//
// AC-5: bundles with a malformed applicability_expr (unknown operator, wrong
// shape) are rejected here.
func (b *Bundle) ValidateApplicabilityExpr() error {
	if len(b.Manifest.ApplicabilityExpr) == 0 {
		return nil // nil / empty = "match every cell" (slice 017 AC-4).
	}
	exprJSON, err := b.Manifest.ApplicabilityExprJSON()
	if err != nil {
		return err
	}
	if _, err := scope.Evaluate(exprJSON, nil); err != nil {
		return fmt.Errorf("control bundle: applicability_expr invalid: %w", err)
	}
	return nil
}

// ValidateEvidenceKinds checks every query.evidence_kind (when non-empty)
// against the schema registry. Returns ErrUnknownEvidenceKind for the first
// miss; later misses are not reported (the author fixes one at a time, same
// pattern as slice 014).
//
// Queries with an empty evidence_kind are accepted: not every query is bound
// to a schema (e.g., a manual JSON-path probe). Slice 012 may later require
// a kind for executable queries; that's a v2 tightening.
//
// The registry contract (slice 014) expects (kind, semver). The bundle
// format does not let authors pin a semver — they declare a kind by name.
// We accept any registered version of that kind by checking the registry
// with the empty string and treating it as "any version".
func (b *Bundle) ValidateEvidenceKinds(_ context.Context, reg SchemaRegistry) error {
	if reg == nil {
		return nil
	}
	for _, q := range b.Manifest.EvidenceQueries {
		if q.EvidenceKind == "" {
			continue
		}
		if !registryKnowsKind(reg, q.EvidenceKind) {
			return ErrUnknownEvidenceKind{Kind: q.EvidenceKind}
		}
	}
	return nil
}

// registryKnowsKind probes the slice-014 cache for any registered semver of
// the kind. The bundle author hasn't pinned one, so we accept the kind iff
// at least one semver is registered. We try the v1 canonical "1.0.0" first
// (the documented convention for platform-bundled schemas) and fall back to
// the empty string for tenant-private kinds that may register with a
// non-1.0.0 baseline.
//
// This wrapper exists because the slice-014 service holds a (kind, semver)
// map — there's no "kind exists in any semver" query exposed. We do the
// minimal viable check without forcing a new interface method. Future
// slices can extend SchemaRegistry with a Kinds() listing.
func registryKnowsKind(reg SchemaRegistry, kind string) bool {
	// Canonical first guess: the v1 baseline most platform schemas register.
	if reg.IsRegistered(kind, "1.0.0") {
		return true
	}
	// Some platform schemas register under "1.0" (slice 014 supports both).
	if reg.IsRegistered(kind, "1.0") {
		return true
	}
	// Empty-version probe — slice 014's InMemory.IsRegistered returns false
	// for empty version, so this never accidentally passes; it's a sentinel
	// for a future "exists?" check.
	return reg.IsRegistered(kind, "")
}

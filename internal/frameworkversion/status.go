// Package frameworkversion implements the slice-484 framework-versioning
// capability layer on top of the framework_versions storage (ADR 0019): the
// version lifecycle (promote a version to current + demote the prior to the
// "superseded" status, audited + reversible), the migration-suggest engine
// (exact requirement-code 1:1 carryovers into a human-reviewed queue, never
// auto-applied), and the migration approve/reject path.
//
// # The "superseded" status maps to the existing `legacy` enum value
//
// ADR 0019 §1 calls the prior-version status "superseded". The actual
// framework_version_status enum on main is { current, legacy, withdrawn } — it
// has no `superseded`/`deprecated` value. The existing value that carries the
// ADR's "replaced-but-still-valid-for-historical-audits" semantic is `legacy`
// (and the soc2import loader already demotes to `legacy`). So this package uses
// `legacy` AS the ADR's "superseded" status: a legacy version is replaced but
// readable when explicitly pinned, distinct from `withdrawn` (the ADR's
// "deprecated"/discouraged). See slice 484 decisions-log D1.
//
// # Catalog-level, NOT tenant-scoped
//
// frameworks / framework_versions / framework_requirements are bundled catalog
// tables (no tenant_id, no RLS). The trust gate is admin-role authz (enforced
// at the HTTP layer, internal/api/adminframeworkversions) plus the append-only
// framework_version_audit trail this package writes in the same transaction —
// never the four-policy tenant-RLS pattern.
package frameworkversion

import (
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Status is the domain form of framework_version_status. Values mirror the
// Postgres enum (migration _init); the typed dbx.FrameworkVersionStatus is the
// DB-boundary form.
type Status string

const (
	// StatusCurrent is the single live version a framework defaults reads to.
	// At most one version per framework is `current` at a time.
	StatusCurrent Status = "current"
	// StatusLegacy is the ADR's "superseded" status: a version replaced by a
	// newer current version but STILL VALID for the audits conducted against
	// it. Readable when explicitly pinned; never the default (ADR 0019 §4).
	StatusLegacy Status = "legacy"
	// StatusWithdrawn is the ADR's "deprecated"/discouraged status: a version
	// pulled from active use. Still pinnable for forensic reads, but signals
	// "do not start new work here".
	StatusWithdrawn Status = "withdrawn"
)

// ErrUnknownStatus is returned when a status value is not one of the three
// canonical statuses.
var ErrUnknownStatus = errors.New("frameworkversion: unknown status")

// ErrIllegalTransition is returned when a requested lifecycle move is not legal
// (e.g. promoting a version that is already current, or reverting a version
// that is not legacy).
var ErrIllegalTransition = errors.New("frameworkversion: illegal status transition")

// IsValid reports whether s is one of the three canonical statuses.
func (s Status) IsValid() bool {
	switch s {
	case StatusCurrent, StatusLegacy, StatusWithdrawn:
		return true
	default:
		return false
	}
}

// DBStatus converts the domain Status to the dbx enum form. Caller must have
// validated the Status first.
func (s Status) DBStatus() dbx.FrameworkVersionStatus {
	return dbx.FrameworkVersionStatus(s)
}

// StatusFromDB converts the dbx enum form back to the domain Status.
func StatusFromDB(d dbx.FrameworkVersionStatus) Status { return Status(d) }

// ValidatePromotion checks the pure-Go legality of promoting a version that is
// currently in `from` status to `current`. A version can be promoted to current
// only from a non-current status (legacy or withdrawn — re-promoting a revived
// version is legal). Promoting a version that is ALREADY current is a no-op the
// store rejects as illegal (it would write an empty audit row). This is the
// pure-Go check the AC unit test exercises without a DB.
func ValidatePromotion(from Status) error {
	if !from.IsValid() {
		return fmt.Errorf("%w: from %q", ErrUnknownStatus, from)
	}
	if from == StatusCurrent {
		return fmt.Errorf("%w: version is already current", ErrIllegalTransition)
	}
	return nil
}

// ValidateRevert checks the pure-Go legality of reverting a promotion: the
// version being reverted must currently be `current` (you revert a promotion by
// demoting the now-current version), and the prior version being restored must
// be `legacy` (the status promotion put it in). The store enforces the pairing;
// this checks the shapes.
func ValidateRevert(current, prior Status) error {
	if !current.IsValid() {
		return fmt.Errorf("%w: current %q", ErrUnknownStatus, current)
	}
	if !prior.IsValid() {
		return fmt.Errorf("%w: prior %q", ErrUnknownStatus, prior)
	}
	if current != StatusCurrent {
		return fmt.Errorf("%w: version to revert is not current", ErrIllegalTransition)
	}
	if prior != StatusLegacy {
		return fmt.Errorf("%w: prior version to restore is not legacy", ErrIllegalTransition)
	}
	return nil
}

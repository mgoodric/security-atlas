// Package crosswalktier implements the slice-483 crosswalk-mapping verified-tier
// governance: the trust-tier state machine on fw_to_scf_edges plus the store
// that performs a tier transition + its append-only audit row in one
// transaction.
//
// The tier is ORTHOGONAL to the slice-438 source_attribution provenance field
// (ADR 0018 §1 / P0-483-3): provenance says WHERE a mapping came from; the tier
// says HOW TRUSTED it is now. This package owns only the trust dimension.
//
// Catalog-level, NOT tenant-scoped: fw_to_scf_edges and the audit table are
// bundled catalog tables (no tenant_id, no RLS). The trust gate is admin-role
// authz (enforced at the HTTP layer, internal/api/admincrosswalktier) plus this
// append-only audit trail — never the four-policy tenant-RLS pattern.
package crosswalktier

import (
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Tier is the trust tier of a crosswalk mapping. The values mirror the
// crosswalk_mapping_tier Postgres enum (migration 20260612080000); the typed
// dbx.CrosswalkMappingTier is the DB-boundary form and Tier is the domain form
// the state machine reasons over.
type Tier string

const (
	// TierDraft is the initial tier for a community_draft / org_internal edge:
	// agent-authored, unreviewed.
	TierDraft Tier = "draft"
	// TierUnderReview means a reviewer has claimed the mapping — the explicit
	// "who is reviewing what" state between draft and verified (ADR 0018 §1).
	TierUnderReview Tier = "under_review"
	// TierVerified is the trust act: a human admin has vetted the mapping. An
	// scf_official edge may seed here directly (ADR 0018 §2); a community_draft
	// must pass through under_review first.
	TierVerified Tier = "verified"
	// TierRejected is a terminal tier — a mapping judged wrong. Reachable from
	// draft or under_review.
	TierRejected Tier = "rejected"
)

// ErrUnknownTier is returned when a tier value is not one of the four canonical
// tiers (e.g. a malformed request body).
var ErrUnknownTier = errors.New("crosswalktier: unknown tier")

// ErrIllegalTransition is returned when a requested move is not a legal edge of
// the state machine (e.g. the draft -> verified skip a community draft must not
// take, or any move out of the terminal rejected tier).
var ErrIllegalTransition = errors.New("crosswalktier: illegal tier transition")

// IsValid reports whether t is one of the four canonical tiers.
func (t Tier) IsValid() bool {
	switch t {
	case TierDraft, TierUnderReview, TierVerified, TierRejected:
		return true
	default:
		return false
	}
}

// ParseTier validates a raw tier string and returns the typed Tier, or
// ErrUnknownTier. Used to validate request input before any DB work.
func ParseTier(s string) (Tier, error) {
	t := Tier(s)
	if !t.IsValid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownTier, s)
	}
	return t, nil
}

// DBTier converts the domain Tier to the dbx enum form for a query parameter.
// Caller must have validated the Tier (IsValid / ParseTier) first.
func (t Tier) DBTier() dbx.CrosswalkMappingTier {
	return dbx.CrosswalkMappingTier(t)
}

// TierFromDB converts the dbx enum form back to the domain Tier.
func TierFromDB(d dbx.CrosswalkMappingTier) Tier { return Tier(d) }

// legalTransitions is the adjacency map of the tier state machine (ADR 0018
// §1). It models the OPERATOR-DRIVEN transitions only:
//
//	draft        -> under_review | rejected
//	under_review -> verified     | rejected
//	verified     -> (none — a verified mapping is not demoted via this API)
//	rejected     -> (none — terminal)
//
// The scf_official seed-to-verified path is NOT an operator transition: it is a
// load/seed-time data step (the migration sets it), so it is deliberately
// absent here. There is intentionally NO draft -> verified edge: a community
// draft must pass through under_review (the load-bearing P0-483 guard).
var legalTransitions = map[Tier]map[Tier]bool{
	TierDraft: {
		TierUnderReview: true,
		TierRejected:    true,
	},
	TierUnderReview: {
		TierVerified: true,
		TierRejected: true,
	},
	TierVerified: {},
	TierRejected: {},
}

// CanTransition reports whether from -> to is a legal operator transition.
// A self-transition (from == to) is NOT legal: it would write an empty audit
// row with no state change.
func CanTransition(from, to Tier) bool {
	if !from.IsValid() || !to.IsValid() {
		return false
	}
	return legalTransitions[from][to]
}

// ValidateTransition returns nil when from -> to is a legal operator
// transition, ErrUnknownTier when either tier is malformed, or
// ErrIllegalTransition otherwise (an illegal skip, a move out of a terminal
// tier, or a no-op self-transition). This is the pure-Go legality check the
// store calls inside the transaction and the AC-8 unit test exercises without a
// DB.
func ValidateTransition(from, to Tier) error {
	if !from.IsValid() {
		return fmt.Errorf("%w: from %q", ErrUnknownTier, from)
	}
	if !to.IsValid() {
		return fmt.Errorf("%w: to %q", ErrUnknownTier, to)
	}
	if !legalTransitions[from][to] {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
	}
	return nil
}

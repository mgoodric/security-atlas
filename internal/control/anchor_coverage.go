package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// frameworkScopeActivated is the framework_scopes.state value that puts a
// framework version in scope for a tenant (slice 018).
const frameworkScopeActivated = "activated"

// AnchorCoverageWarning reports, as a non-fatal note, when the bundle's SCF
// anchor reaches no requirement in any enabled framework (issue #1738). Such
// a control persists fine but silently earns no framework credit: the SCF
// maps NET-26.5, IRO-17, VPM-18 and others to no SOC 2 requirement at all.
//
// "Enabled" is the tenant's activated FrameworkScopes. A tenant with none
// activated is treated as having every current framework version enabled,
// which is what the dashboard credits (dashboard.sql uses every current
// version). When the anchor reaches nothing, the warning suggests the parent
// anchor (NET-26.5 -> NET-26) if that one does.
//
// An empty string means there is nothing to warn about, including when the
// anchor does not resolve (Upload reports that as ErrSCFAnchorUnknown). The
// read runs in a READ-ONLY tenant tx.
func (s *Store) AnchorCoverageWarning(ctx context.Context, anchorRef string) (string, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return "", err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return "", fmt.Errorf("control: parse tenant id: %w", err)
	}
	var warning string
	err = s.WithReadOnlyTenantTx(ctx, func(tx pgx.Tx) error {
		q := dbx.New(tx)
		anchorID, err := resolveSCFAnchor(ctx, q, anchorRef)
		if errors.Is(err, ErrSCFAnchorUnknown) {
			return nil
		}
		if err != nil {
			return err
		}
		row, err := q.GetSCFAnchorByID(ctx, pgUUID(anchorID))
		if err != nil {
			return fmt.Errorf("get scf anchor: %w", err)
		}
		enabled, err := enabledFrameworkVersions(ctx, q, pgUUID(tenantID))
		if err != nil {
			return err
		}
		reqs, err := q.ListRequirementsForAnchor(ctx, pgUUID(anchorID))
		if err != nil {
			return fmt.Errorf("list requirements for anchor: %w", err)
		}
		if reachesEnabledFramework(reqs, enabled) {
			return nil
		}

		parentCode := parentSCFCode(row.ScfID)
		parentReaches := false
		if parentCode != "" {
			parent, perr := q.GetSCFAnchorBySCFID(ctx, parentCode)
			switch {
			case errors.Is(perr, pgx.ErrNoRows):
				parentCode = ""
			case perr != nil:
				return fmt.Errorf("get parent scf anchor: %w", perr)
			default:
				preqs, lerr := q.ListRequirementsForAnchor(ctx, parent.ID)
				if lerr != nil {
					return fmt.Errorf("list requirements for parent anchor: %w", lerr)
				}
				parentReaches = reachesEnabledFramework(preqs, enabled)
			}
		}
		warning = anchorCoverageMessage(row.ScfID, enabled.scoped, parentCode, parentReaches)
		return nil
	})
	return warning, err
}

// enabledSet is the set of framework versions a tenant earns credit in.
// When scoped is false the tenant has no activated FrameworkScope and every
// current framework version counts.
type enabledSet struct {
	scoped   bool
	versions map[[16]byte]bool
}

func (e enabledSet) includes(fvID pgtype.UUID, status dbx.FrameworkVersionStatus) bool {
	if !e.scoped {
		return status == dbx.FrameworkVersionStatusCurrent
	}
	return fvID.Valid && e.versions[fvID.Bytes]
}

func enabledFrameworkVersions(ctx context.Context, q *dbx.Queries, tenantID pgtype.UUID) (enabledSet, error) {
	scopes, err := q.ListFrameworkScopes(ctx, tenantID)
	if err != nil {
		return enabledSet{}, fmt.Errorf("list framework scopes: %w", err)
	}
	return enabledFromScopes(scopes), nil
}

func enabledFromScopes(scopes []dbx.FrameworkScope) enabledSet {
	set := enabledSet{versions: map[[16]byte]bool{}}
	for _, sc := range scopes {
		if sc.State == frameworkScopeActivated && sc.FrameworkVersionID.Valid {
			set.versions[sc.FrameworkVersionID.Bytes] = true
		}
	}
	set.scoped = len(set.versions) > 0
	return set
}

// reachesEnabledFramework reports whether any requirement row (already
// filtered to live edges by the query) belongs to an enabled version.
func reachesEnabledFramework(reqs []dbx.ListRequirementsForAnchorRow, enabled enabledSet) bool {
	for _, r := range reqs {
		if enabled.includes(r.FrameworkVersionID, r.FrameworkVersionStatus) {
			return true
		}
	}
	return false
}

// parentSCFCode returns the parent of an SCF sub-control code by cutting the
// last dotted segment: "NET-26.5" -> "NET-26". A top-level code ("IRO-17")
// has no parent and returns "".
func parentSCFCode(code string) string {
	i := strings.LastIndex(code, ".")
	if i <= 0 {
		return ""
	}
	return code[:i]
}

func anchorCoverageMessage(code string, scoped bool, parentCode string, parentReaches bool) string {
	where := "any current framework"
	if scoped {
		where = "any framework with an activated scope"
	}
	msg := fmt.Sprintf("scf_anchor_id %s reaches no requirement in %s, so this control earns no framework credit", code, where)
	if parentCode != "" && parentReaches {
		msg += fmt.Sprintf("; its parent anchor %s does", parentCode)
	} else {
		msg += "; consider a nearby anchor in the same SCF family that is mapped"
	}
	return msg
}

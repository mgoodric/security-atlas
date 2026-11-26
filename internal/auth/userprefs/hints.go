// #2145: first-visit onboarding hints. A user dismisses a module's
// "New to <module>?" line once; the dismissal is stored per (tenant, user,
// module) in user_onboarding_hint_dismissals and read back through
// GET /v1/me/preferences, so it follows the user to every browser.

package userprefs

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrUnknownHintModule is the sentinel for a hint key outside HintModules.
// The HTTP layer maps it to 400.
var ErrUnknownHintModule = errors.New("userprefs: unknown hint module")

// HintModules is the allow-list of modules with an onboarding hint: the help
// module ids registered in web/lib/help/modules.ts. It mirrors the migration's
// module CHECK constraint, and the two move together. Its length is also the
// cap on how many dismissals one user can hold.
var HintModules = []string{
	"controls",
	// #2155: Settings & admin.
	"settings",
	"admin",
	"admin-users",
	"admin-api-keys",
	"admin-sso",
	"admin-features",
	"admin-tenants",
	"admin-super-admins",
	"admin-demo",
	// #2152: Audit.
	"audits",
	"framework-scopes",
	"board-packs",
	"csf",
	"vendor-claims",
	// #2154: Library.
	"policies",
	"vendors",
	"personnel-security",
	"questionnaires",
	"scf-catalog",
	// #2150: Controls.
	"evidence",
	"exceptions",
	"controls-checklist",
	// #2151: Risks.
	"risks",
	"risks-hierarchy",
	"action-plans",
	// #2149: Program.
	"dashboard",
	"calendar",
	"metrics",
	"activity",
	"approvals",
	// #2592: auditor workspace and requirement detail.
	"audit-workspace",
	"requirements",
}

// HintChange is one PATCH's worth of hint edits. Reset clears every
// dismissal first; Dismissed then sets each module dismissed (true) or shown
// again (false).
type HintChange struct {
	Reset     bool
	Dismissed map[string]bool
}

// Empty reports whether the change does nothing.
func (c HintChange) Empty() bool { return !c.Reset && len(c.Dismissed) == 0 }

// Validate rejects a module key outside HintModules, before any write.
func (c HintChange) Validate() error {
	if len(c.Dismissed) > len(HintModules) {
		return fmt.Errorf("%w: at most %d modules", ErrUnknownHintModule, len(HintModules))
	}
	for m := range c.Dismissed {
		if !slices.Contains(HintModules, m) {
			return fmt.Errorf("%w: %q", ErrUnknownHintModule, m)
		}
	}
	return nil
}

// DismissedHints returns the modules whose hint the user has dismissed,
// sorted. Never nil.
func (s *Store) DismissedHints(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := dbx.New(tx).ListOnboardingHintDismissals(ctx, dbx.ListOnboardingHintDismissalsParams{
		TenantID: pgtype.UUID{Bytes: tenantID, Valid: true},
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("userprefs: list hint dismissals: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []string{}
	}
	return rows, nil
}

// ApplyHints applies a HintChange in one transaction. It validates first, so
// an unknown module writes nothing.
func (s *Store) ApplyHints(ctx context.Context, tenantID, userID uuid.UUID, c HintChange) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Empty() {
		return nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	tid := pgtype.UUID{Bytes: tenantID, Valid: true}
	uid := pgtype.UUID{Bytes: userID, Valid: true}
	if c.Reset {
		if err := q.DeleteOnboardingHintDismissals(ctx, dbx.DeleteOnboardingHintDismissalsParams{TenantID: tid, UserID: uid}); err != nil {
			return fmt.Errorf("userprefs: reset hints: %w", err)
		}
	}
	for m, dismissed := range c.Dismissed {
		var err error
		if dismissed {
			err = q.InsertOnboardingHintDismissal(ctx, dbx.InsertOnboardingHintDismissalParams{TenantID: tid, UserID: uid, Module: m})
		} else {
			err = q.DeleteOnboardingHintDismissal(ctx, dbx.DeleteOnboardingHintDismissalParams{TenantID: tid, UserID: uid, Module: m})
		}
		if err != nil {
			return fmt.Errorf("userprefs: hint %s: %w", m, err)
		}
	}
	return tx.Commit(ctx)
}

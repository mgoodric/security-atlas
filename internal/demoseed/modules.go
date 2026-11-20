package demoseed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// seededSwitches are the module and feature switches that own the data
// writeDataset writes (#2590). The seed switches each one on for the demo
// tenant, rather than leaving the tenant with rows its pages refuse to
// show: a demo tenant exists to show the product, and board.reporting is
// off by default.
var seededSwitches = []string{
	"risk.enabled",           // risks, org units, risk-control links
	"decisions.log",          // decisions
	"policy.enabled",         // policies
	"policy.acknowledgments", // policy acknowledgments
	"questionnaires.enabled", // the demo questionnaire
	"vendor.enabled",         // vendors
	"exceptions.enabled",     // exceptions
	"board.reporting",        // board briefs and packs
	"audit.workflow",         // populations and samples
}

// demoSeedSwitchActor is the feature-flag actor for a switch the seed
// turns on, so the flag audit log names the seed and the admin who ran it.
func demoSeedSwitchActor(actor uuid.UUID) string {
	return "demo-seed:" + actor.String()
}

// switchOnSeededModules switches on every seededSwitches key that is off
// for the demo tenant, inside the seed transaction: an upsert of the
// tenant's feature_flags row and one feature_flag_audit_log row per
// change, as featureflag.Store.Set writes them. A switch already on is
// left alone, with no audit row. It returns the keys it switched on.
func switchOnSeededModules(ctx context.Context, tx pgx.Tx, tenantID, actor uuid.UUID) ([]string, error) {
	q := dbx.New(tx)
	tid := pgtype.UUID{Bytes: tenantID, Valid: true}
	by := demoSeedSwitchActor(actor)
	now := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	var changed []string
	for _, key := range seededSwitches {
		def, ok := featureflag.DefaultByKey(key)
		if !ok {
			return nil, fmt.Errorf("demoseed: switch %q: %w", key, featureflag.ErrNotFound)
		}
		on := def.Enabled
		row, err := q.GetFeatureFlag(ctx, dbx.GetFeatureFlagParams{TenantID: tid, FlagKey: key})
		switch {
		case err == nil:
			on = row.Enabled
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("demoseed: read switch %q: %w", key, err)
		}
		if on {
			continue
		}
		if _, err := q.UpsertFeatureFlag(ctx, dbx.UpsertFeatureFlagParams{
			TenantID:      tid,
			FlagKey:       key,
			Enabled:       true,
			Description:   def.Description,
			Category:      def.Category,
			LastChangedBy: &by,
			LastChangedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("demoseed: switch on %q: %w", key, err)
		}
		if _, err := q.WriteFeatureFlagAuditLog(ctx, dbx.WriteFeatureFlagAuditLogParams{
			ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:    tid,
			FlagKey:     key,
			FromEnabled: false,
			ToEnabled:   true,
			Actor:       by,
			Reason:      "demo seed: switched on so the seeded data is shown",
		}); err != nil {
			return nil, fmt.Errorf("demoseed: audit switch %q: %w", key, err)
		}
		changed = append(changed, key)
	}
	return changed, nil
}

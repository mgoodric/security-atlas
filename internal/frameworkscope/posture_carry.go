package frameworkscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Posture-target carry-over (#2406).
//
// A posture target is stored per framework version (#2136), so a tenant
// moving from version N to N+1 would start N+1's dashboard tile neutral.
// Activating a framework scope on N+1 copies the tenant's target on N when
// N+1 has none. N is the version the version-migration queue (ADR 0019)
// leads into N+1 from, the same rule as the applicability carry-over
// (#1793): no queue rows, or rows from more than one version, and nothing
// is copied. A target already on N+1 is never overwritten, so a re-run, or
// the activation of a later revision, writes nothing.

// PostureTargetCarryActor is the actor on a carried target and its audit
// row. The person whose activation triggered the copy is in the row's
// `after` image as activated_by.
const PostureTargetCarryActor = "system:posture-target-carry-over"

// PriorFrameworkVersions maps each framework version to the one version the
// version-migration queue leads into it from. A version the queue leads
// into from more than one version is left out: there is no single prior
// target to carry.
func PriorFrameworkVersions(rows []dbx.ListFrameworkVersionMigrationSourcesRow) map[uuid.UUID]uuid.UUID {
	out := make(map[uuid.UUID]uuid.UUID, len(rows))
	ambiguous := make(map[uuid.UUID]bool)
	for _, r := range rows {
		if !r.ToVersionID.Valid || !r.FromVersionID.Valid {
			continue
		}
		to, from := uuid.UUID(r.ToVersionID.Bytes), uuid.UUID(r.FromVersionID.Bytes)
		if ambiguous[to] {
			continue
		}
		if prev, ok := out[to]; ok && prev != from {
			delete(out, to)
			ambiguous[to] = true
			continue
		}
		out[to] = from
	}
	return out
}

// postureCarrySource is recorded under `carried_over_from` in the audit
// row's after image.
type postureCarrySource struct {
	FrameworkVersionID uuid.UUID `json:"framework_version_id"`
	FrameworkVersion   string    `json:"framework_version,omitempty"`
}

// carryPostureTarget runs inside Activate's transaction, under the tenant's
// RLS. It reports whether a target was copied.
func carryPostureTarget(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, scope dbx.FrameworkScope, activatedBy string) (bool, error) {
	migrations, err := q.ListFrameworkVersionMigrationsInto(ctx, scope.FrameworkVersionID)
	if err != nil {
		return false, fmt.Errorf("posture carry: list version migrations: %w", err)
	}
	pairs := make([]dbx.ListFrameworkVersionMigrationSourcesRow, 0, len(migrations))
	for _, m := range migrations {
		pairs = append(pairs, dbx.ListFrameworkVersionMigrationSourcesRow{
			ToVersionID:   m.ToVersionID,
			FromVersionID: m.FromVersionID,
		})
	}
	from, ok := PriorFrameworkVersions(pairs)[uuid.UUID(scope.FrameworkVersionID.Bytes)]
	if !ok {
		return false, nil
	}
	// The source must be visible to the tenant: a catalog version or one
	// of its own. Its label also titles the audit row.
	label, err := q.GetFrameworkVersionLabel(ctx, dbx.GetFrameworkVersionLabelParams{
		FrameworkVersionID: pgUUID(from),
		TenantID:           pgUUID(tenantID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("posture carry: get source version: %w", err)
	}

	row, err := q.CarryFrameworkPostureTarget(ctx, dbx.CarryFrameworkPostureTargetParams{
		ToVersionID:   scope.FrameworkVersionID,
		Actor:         PostureTargetCarryActor,
		TenantID:      pgUUID(tenantID),
		FromVersionID: pgUUID(from),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// No target on the source, or one already on this version.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("posture carry: copy target: %w", err)
	}

	after, err := json.Marshal(map[string]any{
		"target_value":       numericFloat(row.TargetValue),
		"warning_threshold":  numericFloat(row.WarningThreshold),
		"critical_threshold": numericFloat(row.CriticalThreshold),
		"direction":          row.Direction,
		"carried_over_from": postureCarrySource{
			FrameworkVersionID: from,
			FrameworkVersion:   label.FrameworkVersion,
		},
		"framework_scope_id": uuid.UUID(scope.ID.Bytes),
		"activated_by":       activatedBy,
	})
	if err != nil {
		return false, fmt.Errorf("posture carry: marshal audit: %w", err)
	}
	if err := q.InsertFrameworkPostureTargetCarryAudit(ctx, dbx.InsertFrameworkPostureTargetCarryAuditParams{
		TenantID:           pgUUID(tenantID),
		FrameworkVersionID: scope.FrameworkVersionID,
		After:              after,
		Actor:              PostureTargetCarryActor,
	}); err != nil {
		return false, fmt.Errorf("posture carry: audit: %w", err)
	}
	return true, nil
}

func numericFloat(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

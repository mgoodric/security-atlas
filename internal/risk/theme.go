// Theme management for slice 053. Validates risk theme assignments against
// the visible vocabulary (10 defaults + tenant-private themes from
// org_themes), updates `risks.themes`, and returns the catalog for the
// frontend's theme picker.
//
// The default vocabulary is seeded into `org_themes` (tenant_id IS NULL) by
// migration 20260511000015 (10 canvas §6.5 themes). Tenant-private themes
// are created via a separate (future) endpoint — slice 053 does not ship a
// "create theme" endpoint; it consumes the vocabulary.

package risk

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrUnknownTheme is returned when a caller assigns a theme not in the
// visible vocabulary. The wrapping error message lists every offending
// theme so the 400 response is actionable.
var ErrUnknownTheme = errors.New("risk: theme not in default + tenant-private vocabulary")

// ThemeSource distinguishes a built-in default theme from a tenant-private one.
type ThemeSource string

const (
	ThemeSourceDefault ThemeSource = "default"
	ThemeSourceTenant  ThemeSource = "tenant"
)

// Theme is a single entry in the visible vocabulary returned by GET /v1/themes.
type Theme struct {
	Name        string
	Description string
	Source      ThemeSource
}

// ListVisibleThemes returns the union of default themes (tenant_id IS NULL)
// and the active tenant's private themes, sorted alphabetically by name.
// Defaults and tenant-private themes share the same flat namespace per
// canvas §6.5; collisions are prevented at DDL level by the partial unique
// index on (theme_name) WHERE tenant_id IS NULL.
func (s *Store) ListVisibleThemes(ctx context.Context) ([]Theme, error) {
	var out []Theme
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAllVisibleThemes(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list visible themes: %w", err)
		}
		out = make([]Theme, 0, len(rows))
		for _, r := range rows {
			src := ThemeSourceTenant
			if !r.TenantID.Valid {
				src = ThemeSourceDefault
			}
			out = append(out, Theme{
				Name:        r.ThemeName,
				Description: r.Description,
				Source:      src,
			})
		}
		// Sort alphabetically per AC-3. The SQL `ORDER BY (tenant_id IS NULL)
		// DESC, theme_name` puts defaults first by group; AC-3 wants a single
		// alpha sort across the whole list.
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return nil
	})
	return out, err
}

// AssignThemes overwrites the themes on a risk. Themes are validated against
// the visible vocabulary; any unknown theme aborts the call with ErrUnknownTheme.
//
// AC-1: POST /v1/risks/{id}/themes returns 200 with the updated risk.
// AC-2: Idempotent — replacing with the same set is a no-op.
//
// The current implementation is "replace all themes" rather than "append".
// The HTTP handler exposes both add (POST) and remove (DELETE) semantics by
// reading the current themes, merging in the operation, and calling
// AssignThemes with the resulting set.
func (s *Store) AssignThemes(ctx context.Context, riskID uuid.UUID, themes []string) (Risk, error) {
	// Normalise: trim + lowercase + dedupe + sorted-for-stability.
	normalised := normaliseThemes(themes)
	var out Risk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := validateThemesAgainstVocab(ctx, q, tenantID, normalised); err != nil {
			return err
		}
		row, err := q.UpdateRiskThemes(ctx, dbx.UpdateRiskThemesParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(riskID),
			Column3:  normalised,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update risk themes: %w", err)
		}
		out = riskFromRow(row)
		// LinkedControlIDs requires a separate query (slice 019) — read it
		// so the wire response keeps the same shape.
		links, err := q.ListRiskControlLinks(ctx, dbx.ListRiskControlLinksParams{
			TenantID: pgUUID(tenantID),
			RiskID:   pgUUID(riskID),
		})
		if err != nil {
			return fmt.Errorf("list risk control links: %w", err)
		}
		out.LinkedControlIDs = make([]uuid.UUID, len(links))
		for i, l := range links {
			out.LinkedControlIDs[i] = uuid.UUID(l.ControlID.Bytes)
		}
		return nil
	})
	return out, err
}

// GetRiskThemes returns the current themes on a risk. Used by handlers that
// need to merge before calling AssignThemes.
func (s *Store) GetRiskThemes(ctx context.Context, riskID uuid.UUID) ([]string, error) {
	var out []string
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(riskID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get risk: %w", err)
		}
		out = append([]string(nil), row.Themes...)
		return nil
	})
	return out, err
}

// validateThemesAgainstVocab returns ErrUnknownTheme listing every offender if
// any supplied theme is missing from the visible vocabulary.
func validateThemesAgainstVocab(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error {
	if len(themes) == 0 {
		return nil
	}
	rows, err := q.ListAllVisibleThemes(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list visible themes for validation: %w", err)
	}
	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		known[r.ThemeName] = struct{}{}
	}
	var bad []string
	for _, t := range themes {
		if _, ok := known[t]; !ok {
			bad = append(bad, t)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s", ErrUnknownTheme, strings.Join(bad, ", "))
	}
	return nil
}

// normaliseThemes trims whitespace, lowercases (theme names are slug-style
// per canvas §6.5), dedupes while preserving first-seen order, and returns
// the slice in sorted order so the stored representation is canonical.
func normaliseThemes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		t = strings.ToLower(t)
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

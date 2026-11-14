package risk

// #1811: organisation theme management, a step of #1745.
//
// An organisation adds, renames and deletes its own risk themes beside the
// ten built-ins. Names are unique across the built-ins and the
// organisation's own themes, and the built-ins cannot be changed. Rename
// rewrites the name in every one of the organisation's risk theme arrays and
// in its aggregation rules' target theme, in one transaction. Delete is
// refused while any risk or rule uses the theme. Every change writes one
// taxonomy_audit_log row in the same transaction.
//
// Locks, in the order every path takes them:
//
//  1. the tenant's theme-vocabulary lock: exclusive for rename and delete,
//     shared for a theme assignment (validateThemesAgainstVocab), so an
//     assignment cannot write a name a concurrent rename or delete removed;
//  2. the aggregation rules' locks (BeforeRiskWrite), for the old and new
//     names, so a rename never interleaves with an evaluation of a rule it
//     retargets (#1867);
//  3. risk rows, taken by the UPDATE.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Theme management errors. The HTTP layer maps each to a status.
var (
	// ErrThemeNotFound: no built-in or organisation theme has that name or id.
	ErrThemeNotFound = errors.New("risk: theme not found")
	// ErrThemeConflict: the name is taken by a built-in or another of the
	// organisation's themes.
	ErrThemeConflict = errors.New("risk: theme name already in use by a built-in or organisation theme")
	// ErrThemeBuiltIn: the ten built-in themes cannot be renamed or deleted.
	ErrThemeBuiltIn = errors.New("risk: built-in themes cannot be changed")
)

// InvalidThemeError is a request that fails the theme shape rules.
type InvalidThemeError struct{ Reason string }

func (e *InvalidThemeError) Error() string { return "risk: invalid theme: " + e.Reason }

// ThemeInUseError refuses deleting a theme that risks or aggregation rules
// still use, with the counts.
type ThemeInUseError struct {
	Name  string
	Risks int64
	Rules int64
}

func (e *ThemeInUseError) Error() string {
	return fmt.Sprintf("risk: theme %q is used by %d risk(s) and %d aggregation rule(s)",
		e.Name, e.Risks, e.Rules)
}

// OrgTheme is one of the organisation's own themes.
type OrgTheme struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ThemeRename is a renamed theme and how many risks and aggregation rules
// the rename rewrote.
type ThemeRename struct {
	OrgTheme
	RisksUpdated int64
	RulesUpdated int64
}

// NewTheme is the input to CreateTheme.
type NewTheme struct {
	Name        string
	Description string
}

// ThemeText is the input to RenameTheme. A nil field keeps its value.
type ThemeText struct {
	Name        *string
	Description *string
}

// Theme names are stored as risks.themes stores them: lowercase slugs
// (normaliseThemes lowercases every assignment), so a name with capitals
// could never be assigned.
var themeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

const maxThemeDescription = 2000

// normaliseThemeName trims and lowercases, as an assignment does, then
// checks the shape.
func normaliseThemeName(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if !themeNamePattern.MatchString(n) {
		return "", &InvalidThemeError{Reason: "name must be lowercase letters, digits, '-' or '_', starting with a letter or digit, at most 63 characters"}
	}
	return n, nil
}

func validateThemeDescription(d string) error {
	if len(d) > maxThemeDescription {
		return &InvalidThemeError{Reason: fmt.Sprintf("description exceeds %d characters", maxThemeDescription)}
	}
	return nil
}

// CreateTheme adds a theme to the caller's organisation.
func (s *Store) CreateTheme(ctx context.Context, in NewTheme, actor string) (OrgTheme, error) {
	name, err := normaliseThemeName(in.Name)
	if err != nil {
		return OrgTheme{}, err
	}
	if err := validateThemeDescription(in.Description); err != nil {
		return OrgTheme{}, err
	}
	var out OrgTheme
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		id := uuid.New()
		if err := checkThemeNameFree(ctx, q, tenantID, name, id); err != nil {
			return err
		}
		row, err := q.CreateOrgTheme(ctx, dbx.CreateOrgThemeParams{
			ID: pgUUID(id), TenantID: pgUUID(tenantID), ThemeName: name, Description: in.Description,
		})
		if err != nil {
			return mapThemeWriteErr("create theme", err)
		}
		out = orgThemeFromRow(row)
		return writeThemeAudit(ctx, q, tenantID, "create", nil, &out, actor)
	})
	return out, err
}

// RenameTheme changes an organisation theme's name and/or description. A
// new name is rewritten onto every risk and aggregation rule that used the
// old one, in the same transaction.
func (s *Store) RenameTheme(ctx context.Context, ref string, in ThemeText, actor string) (ThemeRename, error) {
	var out ThemeRename
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := lockTheme(ctx, q, tenantID, ref)
		if err != nil {
			return err
		}
		name, description := cur.Name, cur.Description
		if in.Name != nil {
			if name, err = normaliseThemeName(*in.Name); err != nil {
				return err
			}
		}
		if in.Description != nil {
			description = *in.Description
		}
		if err := validateThemeDescription(description); err != nil {
			return err
		}
		renamed := name != cur.Name
		if renamed {
			if err := checkThemeNameFree(ctx, q, tenantID, name, cur.ID); err != nil {
				return err
			}
			// Hold the locks of every rule that targets either name before
			// any risk row, the order an evaluation takes them (#1867).
			if err := s.runBeforeRiskWrite(ctx, q, tenantID, []string{cur.Name, name}); err != nil {
				return err
			}
		}
		row, err := q.UpdateTenantTheme(ctx, dbx.UpdateTenantThemeParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID), ThemeName: name, Description: description,
		})
		if err != nil {
			return mapThemeWriteErr("rename theme", err)
		}
		out.OrgTheme = orgThemeFromRow(row)
		if renamed {
			if out.RisksUpdated, err = q.RenameThemeOnRisks(ctx, dbx.RenameThemeOnRisksParams{
				TenantID: pgUUID(tenantID), OldName: cur.Name, NewName: name,
			}); err != nil {
				return fmt.Errorf("rename theme on risks: %w", err)
			}
			if out.RulesUpdated, err = q.RenameThemeOnRules(ctx, dbx.RenameThemeOnRulesParams{
				TenantID: pgUUID(tenantID), OldName: cur.Name, NewName: name,
			}); err != nil {
				return fmt.Errorf("rename theme on aggregation rules: %w", err)
			}
		}
		return writeThemeAudit(ctx, q, tenantID, "rename", &cur, &out.OrgTheme, actor)
	})
	return out, err
}

// DeleteTheme removes an organisation theme no risk or aggregation rule
// uses. A used theme is a *ThemeInUseError with the counts.
func (s *Store) DeleteTheme(ctx context.Context, ref, actor string) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := lockTheme(ctx, q, tenantID, ref)
		if err != nil {
			return err
		}
		usage, err := q.ThemeUsage(ctx, dbx.ThemeUsageParams{TenantID: pgUUID(tenantID), ThemeName: cur.Name})
		if err != nil {
			return fmt.Errorf("theme usage: %w", err)
		}
		if usage.RiskCount > 0 || usage.RuleCount > 0 {
			return &ThemeInUseError{Name: cur.Name, Risks: usage.RiskCount, Rules: usage.RuleCount}
		}
		if err := q.DeleteTenantTheme(ctx, dbx.DeleteTenantThemeParams{
			TenantID: pgUUID(tenantID), ID: pgUUID(cur.ID),
		}); err != nil {
			return fmt.Errorf("delete theme: %w", err)
		}
		return writeThemeAudit(ctx, q, tenantID, "delete", &cur, nil, actor)
	})
}

// lockTheme takes the tenant's theme-vocabulary lock, resolves ref (a name
// or id) to one of the organisation's own themes and locks its row. A
// built-in is ErrThemeBuiltIn.
func lockTheme(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, ref string) (OrgTheme, error) {
	if err := q.LockThemeVocabulary(ctx, pgUUID(tenantID)); err != nil {
		return OrgTheme{}, fmt.Errorf("lock theme vocabulary: %w", err)
	}
	row, err := q.GetVisibleThemeByRef(ctx, dbx.GetVisibleThemeByRefParams{
		TenantID: pgUUID(tenantID), Ref: strings.ToLower(strings.TrimSpace(ref)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgTheme{}, ErrThemeNotFound
	}
	if err != nil {
		return OrgTheme{}, fmt.Errorf("get theme: %w", err)
	}
	if !row.TenantID.Valid {
		return OrgTheme{}, ErrThemeBuiltIn
	}
	locked, err := q.LockTenantTheme(ctx, dbx.LockTenantThemeParams{TenantID: pgUUID(tenantID), ID: row.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgTheme{}, ErrThemeNotFound
	}
	if err != nil {
		return OrgTheme{}, fmt.Errorf("lock theme: %w", err)
	}
	return orgThemeFromRow(locked), nil
}

func checkThemeNameFree(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, name string, exceptID uuid.UUID) error {
	taken, err := q.ThemeNameTaken(ctx, dbx.ThemeNameTakenParams{
		TenantID: pgUUID(tenantID), ThemeName: name, ExceptID: pgUUID(exceptID),
	})
	if err != nil {
		return fmt.Errorf("check theme name: %w", err)
	}
	if taken {
		return ErrThemeConflict
	}
	return nil
}

// mapThemeWriteErr turns a unique violation (a concurrent create of the
// same name) into ErrThemeConflict.
func mapThemeWriteErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrThemeConflict
	}
	return fmt.Errorf("%s: %w", op, err)
}

func orgThemeFromRow(r dbx.OrgTheme) OrgTheme {
	return OrgTheme{
		ID:          uuid.UUID(r.ID.Bytes),
		Name:        r.ThemeName,
		Description: r.Description,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

// themeAuditState is a theme as the audit trail records it.
type themeAuditState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func themeAuditJSON(t *OrgTheme) ([]byte, error) {
	if t == nil {
		return nil, nil
	}
	return json.Marshal(themeAuditState{Name: t.Name, Description: t.Description})
}

func writeThemeAudit(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, action string, before, after *OrgTheme, actor string) error {
	if actor == "" {
		return errors.New("risk: theme change has no actor")
	}
	subject := after
	if subject == nil {
		subject = before
	}
	b, err := themeAuditJSON(before)
	if err != nil {
		return fmt.Errorf("marshal theme audit before: %w", err)
	}
	a, err := themeAuditJSON(after)
	if err != nil {
		return fmt.Errorf("marshal theme audit after: %w", err)
	}
	if err := q.InsertTaxonomyAuditLog(ctx, dbx.InsertTaxonomyAuditLogParams{
		TenantID:    pgUUID(tenantID),
		SubjectKind: "theme",
		SubjectID:   pgUUID(subject.ID),
		SubjectSlug: subject.Name,
		Action:      action,
		Before:      b,
		After:       a,
		Actor:       actor,
	}); err != nil {
		return fmt.Errorf("write taxonomy audit log: %w", err)
	}
	return nil
}

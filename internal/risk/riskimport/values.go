package riskimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Category and theme values map once per batch, not per row (spec #2812,
// Q13; #2844), so a register's spelling variants ("Ops", "Operational")
// land in one category instead of multiplying:
//
//   - staging lists the batch's distinct category values and distinct theme
//     values (values differing only in case are one value), and maps each
//     one that already names an active category (by slug or name) or a
//     visible theme (by name) to it;
//   - a reviewer maps any value to an existing category or theme, or clears
//     a mapping, while the batch is staged;
//   - an approver may instead create the category or theme for a value,
//     through risk.Store's audited CreateCategory and CreateTheme. Nothing
//     is ever created implicitly;
//   - at approval a row takes the mapped category and themes; a row with a
//     value still unmapped is rejected with the values in its reason.

// Value kinds.
const (
	ValueKindCategory = "category"
	ValueKindTheme    = "theme"
)

var (
	// ErrBatchCompleted is a mapping change on a batch with no pending row.
	ErrBatchCompleted = errors.New("riskimport: import batch is completed; its value mapping is final")
	// ErrValueNotFound is a mapping of a value the batch does not have.
	ErrValueNotFound = errors.New("riskimport: the batch has no such value")
	// ErrInvalidValueKind is a kind other than category or theme.
	ErrInvalidValueKind = errors.New("riskimport: kind must be category or theme")
)

// UnknownTargetError is a mapping to a category or theme the organisation
// does not have. Valid lists the ones it can be mapped to.
type UnknownTargetError struct {
	Kind   string
	Target string
	Valid  []string
}

func (e *UnknownTargetError) Error() string {
	what := "an active category slug"
	if e.Kind == ValueKindTheme {
		what = "a theme name"
	}
	return fmt.Sprintf("riskimport: %q is not %s of this organisation; valid: %s",
		e.Target, what, strings.Join(e.Valid, ", "))
}

// ValueMapping is one distinct category or theme value of a batch.
type ValueMapping struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	// Rows is how many staged rows carry the value.
	Rows int `json:"row_count"`
	// Target is the category slug or theme name the value maps to; empty
	// while unmapped.
	Target string `json:"target,omitempty"`
	// MappedBy is who set the mapping; empty when it matched at upload.
	MappedBy string `json:"mapped_by,omitempty"`
}

// MapValueInput maps one value of a batch to an existing category (by slug
// or name) or theme (by name). An empty Target clears the mapping.
type MapValueInput struct {
	BatchID string
	Kind    string
	Value   string
	Target  string
	Actor   string
}

// CreateValueInput creates the category or theme for one value of a batch
// and maps the value to it. Category takes Slug, Name and Description;
// theme takes Name and Description.
type CreateValueInput struct {
	BatchID     string
	Kind        string
	Value       string
	Slug        string
	Name        string
	Description string
	// Actor is recorded on the category or theme's taxonomy audit row, as
	// the creation routes record it; MappedBy on the mapping.
	Actor    string
	MappedBy string
}

// valueKey is a value's identity within a batch: trimmed, case ignored.
func valueKey(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// splitThemes splits a theme cell on commas and semicolons, as the risk
// export writes themes comma-separated, keeping each value once ignoring
// case, in the order written.
func splitThemes(cell string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range strings.FieldsFunc(cell, func(r rune) bool { return r == ',' || r == ';' }) {
		v = strings.TrimSpace(v)
		if v == "" || seen[valueKey(v)] {
			continue
		}
		seen[valueKey(v)] = true
		out = append(out, v)
	}
	return out
}

// distinctValues lists the distinct category and theme values of the staged
// risks, each spelled as it first appears, with the target it matches at
// upload: an active category by slug or name, a visible theme by name.
func distinctValues(risks []StagedRisk, cats []risk.Category, themes []risk.Theme) []ValueMapping {
	themeNames := map[string]string{}
	for _, t := range themes {
		themeNames[valueKey(t.Name)] = t.Name
	}
	var out []ValueMapping
	index := map[string]int{}
	add := func(kind, value string) {
		k := kind + "\x00" + valueKey(value)
		if i, ok := index[k]; ok {
			out[i].Rows++
			return
		}
		m := ValueMapping{Kind: kind, Value: value, Rows: 1}
		switch kind {
		case ValueKindCategory:
			m.Target, _ = MatchCategory(cats, value)
		case ValueKindTheme:
			m.Target = themeNames[valueKey(value)]
		}
		index[k] = len(out)
		out = append(out, m)
	}
	for _, r := range risks {
		add(ValueKindCategory, r.Category)
		for _, t := range r.Themes {
			add(ValueKindTheme, t)
		}
	}
	return out
}

// valueTargets is a batch's value map, by kind and value key.
type valueTargets map[string]map[string]string

func (v valueTargets) target(kind, value string) string { return v[kind][valueKey(value)] }

// resolveRow returns the category and themes a row takes from the map, and
// the values that are unmapped.
func (v valueTargets) resolveRow(category string, themes []string) (string, []string, []string) {
	var unmapped []string
	cat := v.target(ValueKindCategory, category)
	if cat == "" {
		unmapped = append(unmapped, fmt.Sprintf("category %q", category))
	}
	var mapped []string
	for _, t := range themes {
		target := v.target(ValueKindTheme, t)
		if target == "" {
			unmapped = append(unmapped, fmt.Sprintf("theme %q", t))
			continue
		}
		mapped = append(mapped, target)
	}
	return cat, mapped, unmapped
}

// readValueMaps reads a batch's value map. With lock, the map rows are
// held FOR SHARE, so a mapping change waits for the approval reading it.
func readValueMaps(ctx context.Context, tx pgx.Tx, batchID string, lock bool) ([]ValueMapping, valueTargets, error) {
	q := `
		SELECT kind, value, COALESCE(target, ''), mapped_by
		FROM risk_import_value_maps
		WHERE batch_id = $1::uuid
		ORDER BY kind, lower(value), value`
	if lock {
		q += ` FOR SHARE`
	}
	rows, err := tx.Query(ctx, q, batchID)
	if err != nil {
		return nil, nil, fmt.Errorf("riskimport: read value map: %w", err)
	}
	defer rows.Close()
	var out []ValueMapping
	targets := valueTargets{ValueKindCategory: {}, ValueKindTheme: {}}
	for rows.Next() {
		var m ValueMapping
		if err := rows.Scan(&m.Kind, &m.Value, &m.Target, &m.MappedBy); err != nil {
			return nil, nil, fmt.Errorf("riskimport: scan value map: %w", err)
		}
		targets[m.Kind][valueKey(m.Value)] = m.Target
		out = append(out, m)
	}
	return out, targets, rows.Err()
}

// splitByKind fills each mapping's row count from the staged rows and
// returns the category and theme values.
func splitByKind(maps []ValueMapping, staged []BatchRow) ([]ValueMapping, []ValueMapping) {
	counts := map[string]int{}
	for _, r := range staged {
		counts[ValueKindCategory+"\x00"+valueKey(r.CategoryValue)]++
		for _, t := range r.ThemeValues {
			counts[ValueKindTheme+"\x00"+valueKey(t)]++
		}
	}
	cats, themes := []ValueMapping{}, []ValueMapping{}
	for _, m := range maps {
		m.Rows = counts[m.Kind+"\x00"+valueKey(m.Value)]
		if m.Kind == ValueKindCategory {
			cats = append(cats, m)
		} else {
			themes = append(themes, m)
		}
	}
	return cats, themes
}

// MapValue maps one value of a staged batch to an existing category or
// theme, or clears its mapping. Rows take the mapping at approval.
func (s *Store) MapValue(ctx context.Context, in MapValueInput) (ValueMapping, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return ValueMapping{}, err
	}
	if in.Kind != ValueKindCategory && in.Kind != ValueKindTheme {
		return ValueMapping{}, ErrInvalidValueKind
	}
	target := strings.TrimSpace(in.Target)
	if target != "" {
		var err error
		if target, err = s.existingTarget(ctx, in.Kind, target); err != nil {
			return ValueMapping{}, err
		}
	}
	return s.setTarget(ctx, in.BatchID, in.Kind, in.Value, target, in.Actor)
}

// CreateValue creates the category or theme for one value of a staged batch
// through the risk store's audited creation, then maps the value to it. The
// caller gates it as the category and theme creation routes are gated: a
// tenant approver.
func (s *Store) CreateValue(ctx context.Context, in CreateValueInput) (ValueMapping, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return ValueMapping{}, err
	}
	if in.Kind != ValueKindCategory && in.Kind != ValueKindTheme {
		return ValueMapping{}, ErrInvalidValueKind
	}
	// Refuse a value the batch lacks, or a completed batch, before
	// creating anything.
	if err := s.checkMappable(ctx, in.BatchID, in.Kind, in.Value); err != nil {
		return ValueMapping{}, err
	}
	var target string
	switch in.Kind {
	case ValueKindCategory:
		c, err := s.risks.CreateCategory(ctx, risk.NewCategory{Slug: in.Slug, Name: in.Name, Description: in.Description}, in.Actor)
		if err != nil {
			return ValueMapping{}, err
		}
		target = c.Slug
	case ValueKindTheme:
		t, err := s.risks.CreateTheme(ctx, risk.NewTheme{Name: in.Name, Description: in.Description}, in.Actor)
		if err != nil {
			return ValueMapping{}, err
		}
		target = t.Name
	}
	return s.setTarget(ctx, in.BatchID, in.Kind, in.Value, target, in.MappedBy)
}

// existingTarget resolves a mapping target to the active category slug or
// visible theme name it names.
func (s *Store) existingTarget(ctx context.Context, kind, target string) (string, error) {
	if kind == ValueKindCategory {
		cats, err := s.risks.ListCategories(ctx)
		if err != nil {
			return "", fmt.Errorf("riskimport: list categories: %w", err)
		}
		if slug, ok := MatchCategory(cats, target); ok {
			return slug, nil
		}
		return "", &UnknownTargetError{Kind: kind, Target: target, Valid: activeSlugs(cats)}
	}
	themes, err := s.risks.ListVisibleThemes(ctx)
	if err != nil {
		return "", fmt.Errorf("riskimport: list themes: %w", err)
	}
	names := make([]string, 0, len(themes))
	for _, t := range themes {
		if valueKey(t.Name) == valueKey(target) {
			return t.Name, nil
		}
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return "", &UnknownTargetError{Kind: kind, Target: target, Valid: names}
}

// lockMapRow finds and locks the value's map row of a staged batch.
func lockMapRow(ctx context.Context, tx pgx.Tx, batchID, kind, value string) (uuid.UUID, error) {
	if _, err := uuid.Parse(batchID); err != nil {
		return uuid.Nil, ErrBatchNotFound
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM risk_import_batches WHERE id = $1::uuid FOR SHARE`, batchID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrBatchNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("riskimport: read batch: %w", err)
	}
	if status != BatchStatusStaged {
		return uuid.Nil, ErrBatchCompleted
	}
	rows, err := tx.Query(ctx, `
		SELECT id, value FROM risk_import_value_maps
		WHERE batch_id = $1::uuid AND kind = $2
		FOR UPDATE`, batchID, kind)
	if err != nil {
		return uuid.Nil, fmt.Errorf("riskimport: lock value map: %w", err)
	}
	defer rows.Close()
	found := uuid.Nil
	for rows.Next() {
		var id uuid.UUID
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return uuid.Nil, fmt.Errorf("riskimport: scan value map: %w", err)
		}
		if valueKey(v) == valueKey(value) {
			found = id
		}
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if found == uuid.Nil {
		return uuid.Nil, ErrValueNotFound
	}
	return found, nil
}

func (s *Store) checkMappable(ctx context.Context, batchID, kind, value string) error {
	return s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		_, err := lockMapRow(ctx, tx, batchID, kind, value)
		return err
	})
}

func (s *Store) setTarget(ctx context.Context, batchID, kind, value, target, actor string) (ValueMapping, error) {
	var out ValueMapping
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		id, err := lockMapRow(ctx, tx, batchID, kind, value)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			UPDATE risk_import_value_maps
			SET target = $2, mapped_by = $3, mapped_at = now(), updated_at = now()
			WHERE id = $1
			RETURNING kind, value, COALESCE(target, ''), mapped_by
		`, id, nullable(target), actor).Scan(&out.Kind, &out.Value, &out.Target, &out.MappedBy); err != nil {
			return fmt.Errorf("riskimport: set value map: %w", err)
		}
		staged, err := readStaged(ctx, tx, batchID)
		if err != nil {
			return err
		}
		cats, themes := splitByKind([]ValueMapping{out}, staged)
		out = append(cats, themes...)[0]
		return nil
	})
	return out, err
}

package risks

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// A saved register view (#2133) is the register's URL state: the
// GET /v1/risks filters and sort it was saved with, and the optional
// columns the chooser added. Each filter key is the platform parameter of
// the same name, so a view applies by putting its entries back on the URL.
// Paging is not part of a view.

// savedViewColumnsKey holds the chosen optional columns, comma-separated
// (the register's `?cols=`).
const savedViewColumnsKey = "cols"

// savedViewParamKeys are the GET /v1/risks parameters a view may hold. They
// match the register's filters (web/app/(authed)/risks/server-query.ts
// FILTER_KEYS) plus the sort and the roll-ups toggle.
var savedViewParamKeys = []string{
	"status", "owner", "treatment", "category", "review_due", "methodology",
	"org_unit", "review_due_from", "review_due_to", "sort", "include_rollups",
}

// savedViewColumns are the chooser's optional columns
// (server-query.ts OPTIONAL_COLUMNS).
var savedViewColumns = []string{"methodology", "level", "org_unit", "themes", "created_at"}

// categoryRefRE is a category slug or id, the shape the register's
// category pill sends.
var categoryRefRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidateSavedView checks a register view's entries before it is saved.
// An unknown key, or a value GET /v1/risks or the column chooser would not
// accept, is an error naming it. Empty values are dropped. The kept
// entries are then read together as one GET /v1/risks query, so a view
// whose entries are each fine but disagree (a review_due_from after its
// review_due_to) is refused too (#2550).
func ValidateSavedView(params map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(params))
	q := url.Values{}
	for k, v := range params {
		if v == "" {
			continue
		}
		if err := checkSavedViewEntry(k, v); err != nil {
			return nil, err
		}
		out[k] = v
		if k != savedViewColumnsKey {
			q.Set(k, v)
		}
	}
	f, msg := parsePageFilter(q)
	if msg != "" {
		return nil, errors.New(msg)
	}
	if f.DueFrom != nil && f.DueTo != nil && f.DueFrom.After(*f.DueTo) {
		return nil, errors.New("review_due_from must not be after review_due_to")
	}
	return out, nil
}

// SanitizeSavedView keeps the entries of a stored view that are still
// valid. A view saved before a filter value or column was retired loses
// that entry rather than failing the register.
func SanitizeSavedView(params map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		if v != "" && checkSavedViewEntry(k, v) == nil {
			out[k] = v
		}
	}
	return out
}

var errUnknownSavedViewKey = errors.New("is not a risk register view parameter")

func checkSavedViewEntry(k, v string) error {
	if k == savedViewColumnsKey {
		cols := strings.Split(v, ",")
		if len(cols) > len(savedViewColumns) {
			return fmt.Errorf("cols: at most %d columns", len(savedViewColumns))
		}
		for i, c := range cols {
			if !slices.Contains(savedViewColumns, c) {
				return fmt.Errorf("cols: %q is not a register column", c)
			}
			if slices.Contains(cols[:i], c) {
				return fmt.Errorf("cols: %q is listed twice", c)
			}
		}
		return nil
	}
	if !slices.Contains(savedViewParamKeys, k) {
		return fmt.Errorf("%q %w", k, errUnknownSavedViewKey)
	}
	if k == "category" && !categoryRefRE.MatchString(v) {
		return errors.New("category must be a category slug or id")
	}
	if _, msg := parsePageFilter(url.Values{k: {v}}); msg != "" {
		return errors.New(msg)
	}
	return nil
}

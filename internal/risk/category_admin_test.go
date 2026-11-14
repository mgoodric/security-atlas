package risk

import (
	"errors"
	"strings"
	"testing"
)

// TestCategoryShapeRules covers the #1810 checks made before any SQL, so a
// bad request is a 400 naming the rule rather than a constraint violation.
func TestCategoryShapeRules(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{"iam", "cloud-security", "third_party", "a1"} {
		if !categorySlugPattern.MatchString(slug) {
			t.Errorf("slug %q refused", slug)
		}
	}
	for _, slug := range []string{"", "IAM", "-iam", "cloud security", strings.Repeat("a", 64)} {
		if categorySlugPattern.MatchString(slug) {
			t.Errorf("slug %q accepted", slug)
		}
	}
	for name, tc := range map[string]struct {
		name, description string
		ok                bool
	}{
		"valid":            {"IAM", "Identity", true},
		"blank name":       {"  ", "", false},
		"long name":        {strings.Repeat("n", maxCategoryName+1), "", false},
		"long description": {"IAM", strings.Repeat("d", maxCategoryDescription+1), false},
	} {
		err := validateCategoryText(tc.name, tc.description)
		var invalid *InvalidCategoryError
		if tc.ok != (err == nil) || (err != nil && !errors.As(err, &invalid)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCategoryErrors(t *testing.T) {
	t.Parallel()
	err := error(&CategoryInUseError{Slug: "privacy", Risks: 3, Rules: 1})
	if !strings.Contains(err.Error(), "3 risk(s) and 1 aggregation rule(s)") {
		t.Errorf("in-use message = %q", err)
	}
	if got := mapCategoryWriteErr("op", ErrCategoryIsDefault); !errors.Is(got, ErrCategoryIsDefault) {
		t.Errorf("default error not passed through: %v", got)
	}
	if got := mapCategoryWriteErr("op", errors.New("boom")); !strings.HasPrefix(got.Error(), "op: ") {
		t.Errorf("other error not wrapped: %v", got)
	}
}

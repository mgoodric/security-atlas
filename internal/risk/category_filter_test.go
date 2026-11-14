package risk

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestCategoryFilter covers the ?category= resolution of #1812: slugs and
// ids both match, archived categories stay filterable, no refs is no
// filter, and an unknown ref names every slug the tenant has.
func TestCategoryFilter(t *testing.T) {
	t.Parallel()
	privacy := Category{ID: uuid.New(), Slug: "privacy"}
	archived := Category{ID: uuid.New(), Slug: "regulatory", Archived: true}
	byID := map[uuid.UUID]Category{privacy.ID: privacy, archived.ID: archived}

	if got, err := categoryFilter(byID, nil); got != nil || err != nil {
		t.Fatalf("no refs: got %v, %v; want nil, nil", got, err)
	}
	got, err := categoryFilter(byID, []string{"privacy", archived.ID.String()})
	if err != nil {
		t.Fatalf("slug and id: %v", err)
	}
	if len(got) != 2 || !got[privacy.ID] || !got[archived.ID] {
		t.Fatalf("slug and id: got %v", got)
	}

	_, err = categoryFilter(byID, []string{"privacy", "technical"})
	if !IsUnknownCategory(err) {
		t.Fatalf("unknown ref: err = %v, want UnknownCategoryError", err)
	}
	if msg := err.Error(); !strings.Contains(msg, `"technical"`) || !strings.Contains(msg, "privacy, regulatory") {
		t.Fatalf("unknown ref: error %q does not name the ref and the sorted valid slugs", msg)
	}
}

func TestSetCategoryNames(t *testing.T) {
	t.Parallel()
	c := Category{ID: uuid.New(), Slug: "privacy", Name: "Data Protection"}
	r := Risk{CategoryID: c.ID}
	setCategoryNames(map[uuid.UUID]Category{c.ID: c}, &r)
	if r.CategorySlug != "privacy" || r.CategoryName != "Data Protection" {
		t.Fatalf("got slug %q name %q", r.CategorySlug, r.CategoryName)
	}
}

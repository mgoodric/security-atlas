package metrics

import "testing"

// Regression for the slice 386 bug: a metric with `source_slices: []`
// produced a nil slice, which pgx encodes as SQL NULL, violating the
// metrics_catalog.source_slices NOT NULL constraint and aborting the whole
// catalog seed transaction. nonNilStrings must always return a non-nil slice.
func TestNonNilStrings(t *testing.T) {
	t.Run("nil input returns non-nil empty", func(t *testing.T) {
		got := nonNilStrings(nil)
		if got == nil {
			t.Fatal("nonNilStrings(nil) = nil; must be non-nil so pgx encodes [] not NULL")
		}
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})

	t.Run("empty non-nil input returns non-nil empty", func(t *testing.T) {
		got := nonNilStrings([]string{})
		if got == nil {
			t.Fatal("nonNilStrings([]) = nil; must be non-nil")
		}
	})

	t.Run("populated input is copied, not aliased", func(t *testing.T) {
		in := []string{"021", "076"}
		got := nonNilStrings(in)
		if len(got) != 2 || got[0] != "021" || got[1] != "076" {
			t.Fatalf("nonNilStrings(%v) = %v", in, got)
		}
		got[0] = "mutated"
		if in[0] != "021" {
			t.Error("nonNilStrings returned an aliased slice; expected an independent copy")
		}
	})
}

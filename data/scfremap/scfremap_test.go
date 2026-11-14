package scfremap

import "testing"

func TestEmbeddedTableParses(t *testing.T) {
	t.Parallel()
	r, ok, err := Lookup("test-2026.1", "IAC-21")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ok || r.To != "IAC-10" || r.Basis != "concept" || r.ToRelease != "test-2026.3" {
		t.Fatalf("IAC-21 remap = %+v ok=%v; want IAC-10 (concept) in test-2026.3", r, ok)
	}
	if got := r.Note(); got != "IAC-21 -> IAC-10 in test-2026.3 (concept)" {
		t.Fatalf("Note = %q", got)
	}
	if _, ok, _ := Lookup("test-2026.3", "IAC-21"); ok {
		t.Fatal("a remap is keyed on its from_release")
	}
}

func TestParseRejectsBadRows(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"not json":      `{`,
		"missing field": `{"remaps":[{"from_release":"a","to_release":"b","from":"X"}]}`,
		"bad basis":     `{"remaps":[{"from_release":"a","to_release":"b","from":"X","to":"Y","basis":"guess"}]}`,
		"duplicate":     `{"remaps":[{"from_release":"a","to_release":"b","from":"X","to":"Y","basis":"legacy"},{"from_release":"a","to_release":"b","from":"X","to":"Z","basis":"legacy"}]}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}

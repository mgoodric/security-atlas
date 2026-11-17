package control

import (
	"errors"
	"testing"
)

// #2131 — `?sort=` is allow-listed before any SQL runs.
func TestParseListSort(t *testing.T) {
	t.Parallel()
	ok := []struct {
		raw  string
		key  ListSortKey
		desc bool
	}{
		{"", SortDefault, false},
		{"scf_id", SortSCFID, false},
		{"-name", SortName, true},
		{"family", SortFamily, false},
		{"-state", SortState, true},
		{"freshness", SortFreshness, false},
		{"-last_observed_at", SortLastObservedAt, true},
		{"owner", SortOwner, false},
		{"-frameworks", SortFrameworks, true},
	}
	for _, tc := range ok {
		key, desc, err := ParseListSort(tc.raw)
		if err != nil || key != tc.key || desc != tc.desc {
			t.Errorf("ParseListSort(%q) = %q, %v, %v; want %q, %v", tc.raw, key, desc, err, tc.key, tc.desc)
		}
	}
	for _, raw := range []string{"default", "-default", "-", "--name", "title", "name desc", "c.id"} {
		if _, _, err := ParseListSort(raw); !errors.Is(err, ErrInvalidListSort) {
			t.Errorf("ParseListSort(%q) err = %v; want ErrInvalidListSort", raw, err)
		}
	}
}

func TestOptText(t *testing.T) {
	t.Parallel()
	if optText("") != nil {
		t.Error(`optText("") is not nil`)
	}
	if v := optText("fail"); v == nil || *v != "fail" {
		t.Errorf(`optText("fail") = %v`, v)
	}
}

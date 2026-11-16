package mappingoverlay

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #1804: the remap decision's pure-Go rules. The database paths are in
// internal/api/scfimport's overlay remap integration tests.

func TestRemapDecideInput_Validate(t *testing.T) {
	t.Parallel()
	ok := RemapDecideInput{ProposalID: "x", Decision: RemapApproved, Actor: "jwt:1", DecidedBy: "u1"}
	cases := []struct {
		name   string
		mutate func(*RemapDecideInput)
		reason string
	}{
		{"ok", func(*RemapDecideInput) {}, ""},
		{"reject", func(in *RemapDecideInput) { in.Decision = RemapRejected }, ""},
		{"keep legacy", func(in *RemapDecideInput) { in.Decision = RemapKeptLegacy }, ""},
		{"open is not a decision", func(in *RemapDecideInput) { in.Decision = RemapOpen }, "decision must be"},
		{"no decider", func(in *RemapDecideInput) { in.DecidedBy = " " }, "deciding person"},
		{"no actor", func(in *RemapDecideInput) { in.Actor = "" }, "actor"},
		{"long reason", func(in *RemapDecideInput) { in.Reason = strings.Repeat("r", maxRemapReason+1) }, "reason exceeds"},
	}
	for _, tc := range cases {
		in := ok
		tc.mutate(&in)
		err := in.Validate()
		var invalid *InvalidRemapError
		switch {
		case tc.reason == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.reason != "" && (!errors.As(err, &invalid) || !strings.Contains(invalid.Reason, tc.reason)):
			t.Errorf("%s: err=%v; want reason containing %q", tc.name, err, tc.reason)
		}
	}
}

func TestRemapFilter_Validate(t *testing.T) {
	t.Parallel()
	for _, f := range []RemapFilter{{}, {Class: "split"}, {Status: RemapKeptLegacy}, {Class: "unchanged", Status: RemapOpen}} {
		if err := f.Validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	for _, f := range []RemapFilter{{Class: "moved"}, {Status: "rebound"}} {
		var invalid *InvalidRemapError
		if err := f.Validate(); !errors.As(err, &invalid) {
			t.Errorf("%+v: err=%v; want InvalidRemapError", f, err)
		}
	}
}

func TestCheckDecidable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status, decision string
		ok               bool
	}{
		{RemapOpen, RemapApproved, true},
		{RemapOpen, RemapRejected, true},
		{RemapOpen, RemapKeptLegacy, true},
		{RemapRejected, RemapApproved, true},
		{RemapRejected, RemapKeptLegacy, true},
		{RemapRejected, RemapRejected, false},
		{RemapApproved, RemapKeptLegacy, false},
		{RemapKeptLegacy, RemapApproved, false},
	} {
		err := checkDecidable(tc.status, tc.decision)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, ErrRemapDecided)) {
			t.Errorf("%s -> %s: err=%v; want ok=%v", tc.status, tc.decision, err, tc.ok)
		}
	}
}

func TestSuccessorFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		class      string
		successors []string
		chosen     string
		want       string
	}{
		{"unchanged", []string{"TRM-01"}, "", "TRM-01"},
		{"renamed", []string{"TRM-20"}, "", "TRM-20"},
		{"reused_id", []string{"TRM-41"}, "", "TRM-41"},
		{"renamed", []string{"TRM-20"}, " TRM-99 ", "TRM-99"},
		{"split", []string{"TRM-30", "TRM-31"}, "TRM-31", "TRM-31"},
		{"no_successor", nil, "TRM-41", "TRM-41"},
	} {
		got, err := successorFor(tc.class, tc.successors, tc.chosen)
		if err != nil || got != tc.want {
			t.Errorf("%s %v %q: %q, %v; want %q", tc.class, tc.successors, tc.chosen, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		class      string
		successors []string
	}{
		{"split", []string{"TRM-30", "TRM-31"}},
		{"no_successor", nil},
		{"reused_id", nil},
	} {
		if _, err := successorFor(tc.class, tc.successors, ""); !errors.Is(err, ErrRemapAnchorRequired) {
			t.Errorf("%s %v with no choice: err=%v; want ErrRemapAnchorRequired", tc.class, tc.successors, err)
		}
	}
}

// Invalid input is refused before the store needs a pool or a tenant.
func TestDecideRemap_RefusesBeforeTheDatabase(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	if _, err := s.DecideRemap(context.Background(), RemapDecideInput{Decision: "maybe"}); err == nil {
		t.Fatal("unknown decision accepted")
	}
	in := RemapDecideInput{ProposalID: "not-a-uuid", Decision: RemapRejected, Actor: "a", DecidedBy: "b"}
	if _, err := s.DecideRemap(context.Background(), in); !errors.Is(err, ErrRemapNotFound) {
		t.Fatalf("malformed id: err=%v; want ErrRemapNotFound", err)
	}
	if _, err := s.ListRemapProposals(context.Background(), RemapFilter{Status: "x"}); err == nil {
		t.Fatal("bad filter accepted")
	}
}

func TestInvalidRemapError(t *testing.T) {
	t.Parallel()
	err := &InvalidRemapError{Reason: "why"}
	if code, reason, ok := Classify(err); !ok || code != "invalid_remap_decision" || reason != "why" {
		t.Fatalf("Classify = %q %q %v", code, reason, ok)
	}
	if !strings.Contains(err.Error(), "why") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

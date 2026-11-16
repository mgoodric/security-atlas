package userprefs

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// #2145: the hint allow-list is checked before any write.
func TestHintChange_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		c       HintChange
		wantErr bool
	}{
		{"empty", HintChange{}, false},
		{"reset only", HintChange{Reset: true}, false},
		{"known module dismissed", HintChange{Dismissed: map[string]bool{"controls": true}}, false},
		{"known module shown again", HintChange{Dismissed: map[string]bool{"controls": false}}, false},
		{"unknown module", HintChange{Dismissed: map[string]bool{"risks-typo": true}}, true},
		{"over the cap", HintChange{Dismissed: map[string]bool{"controls": true, "a": true, "b": true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v; wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrUnknownHintModule) {
				t.Fatalf("Validate() = %v; want ErrUnknownHintModule", err)
			}
		})
	}
}

func TestHintChange_Empty(t *testing.T) {
	t.Parallel()
	if !(HintChange{}).Empty() {
		t.Error("zero HintChange should be empty")
	}
	if (HintChange{Reset: true}).Empty() {
		t.Error("reset is not empty")
	}
	if (HintChange{Dismissed: map[string]bool{"controls": false}}).Empty() {
		t.Error("a dismissed entry is not empty")
	}
}

func TestApplyHints_RejectsBeforeTouchingTheDB(t *testing.T) {
	t.Parallel()
	// A Store with no pool proves validation and the empty check run first.
	s := &Store{}
	err := s.ApplyHints(t.Context(), uuid.Nil, uuid.Nil, HintChange{Dismissed: map[string]bool{"nope": true}})
	if !errors.Is(err, ErrUnknownHintModule) {
		t.Fatalf("ApplyHints = %v; want ErrUnknownHintModule", err)
	}
	if err := s.ApplyHints(t.Context(), uuid.Nil, uuid.Nil, HintChange{}); err != nil {
		t.Fatalf("empty ApplyHints = %v; want nil", err)
	}
}

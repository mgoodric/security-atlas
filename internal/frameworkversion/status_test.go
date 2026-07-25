package frameworkversion

import (
	"errors"
	"testing"
)

// Pure-Go unit tests for the lifecycle status validation (slice 353 Q-2). No DB.

func TestStatus_IsValid(t *testing.T) {
	t.Parallel()
	for _, s := range []Status{StatusCurrent, StatusLegacy, StatusWithdrawn} {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []Status{"", "superseded", "deprecated", "draft", "bogus"} {
		if Status(s).IsValid() {
			t.Errorf("%q should NOT be valid (enum is current/legacy/withdrawn)", s)
		}
	}
}

func TestValidatePromotion(t *testing.T) {
	t.Parallel()
	// A legacy or withdrawn version can be (re-)promoted to current.
	for _, from := range []Status{StatusLegacy, StatusWithdrawn} {
		if err := ValidatePromotion(from); err != nil {
			t.Errorf("promotion from %q should be legal, got %v", from, err)
		}
	}
	// Promoting an already-current version is illegal (a no-op).
	if err := ValidatePromotion(StatusCurrent); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("promoting a current version should be ErrIllegalTransition, got %v", err)
	}
	// An unknown status is rejected.
	if err := ValidatePromotion(Status("nope")); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("unknown from-status should be ErrUnknownStatus, got %v", err)
	}
}

func TestValidateRevert(t *testing.T) {
	t.Parallel()
	// Legal: demote a current version, restore a legacy one.
	if err := ValidateRevert(StatusCurrent, StatusLegacy); err != nil {
		t.Errorf("revert current<-legacy should be legal, got %v", err)
	}
	// Illegal: the version being reverted is not current.
	if err := ValidateRevert(StatusLegacy, StatusLegacy); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("reverting a non-current version should be ErrIllegalTransition, got %v", err)
	}
	// Illegal: the prior version to restore is not legacy.
	if err := ValidateRevert(StatusCurrent, StatusWithdrawn); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("restoring a non-legacy prior should be ErrIllegalTransition, got %v", err)
	}
	// Unknown status rejected.
	if err := ValidateRevert(Status("x"), StatusLegacy); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("unknown current-status should be ErrUnknownStatus, got %v", err)
	}
}

func TestStatus_DBRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []Status{StatusCurrent, StatusLegacy, StatusWithdrawn} {
		if got := StatusFromDB(s.DBStatus()); got != s {
			t.Errorf("round-trip %q -> %q", s, got)
		}
	}
}

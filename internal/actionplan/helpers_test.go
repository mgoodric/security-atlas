// Pure-Go unit tests for the slice-384 ActionPlan state machine + input
// validators (slice-353 Q-2 pure-Go-first convention). No Postgres, no build
// tag — fast t.Parallel() table tests covering the branches the integration
// suite would otherwise be the only exercise of.

package actionplan

import (
	"errors"
	"testing"
	"time"
)

func TestValidStatus(t *testing.T) {
	t.Parallel()
	valid := []string{StatusDraft, StatusInProgress, StatusBlocked, StatusCompleted, StatusVerified}
	for _, s := range valid {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "open", "closed", "DRAFT", "in-progress", "approved"}
	for _, s := range invalid {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
}

func TestAllowedTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		from string
		to   string
		want bool
	}{
		// Happy-path forward edges.
		{"draft->in_progress", StatusDraft, StatusInProgress, true},
		{"in_progress->blocked", StatusInProgress, StatusBlocked, true},
		{"in_progress->completed", StatusInProgress, StatusCompleted, true},
		{"blocked->in_progress", StatusBlocked, StatusInProgress, true},
		{"blocked->completed", StatusBlocked, StatusCompleted, true},
		{"completed->verified", StatusCompleted, StatusVerified, true},
		{"completed->in_progress (reopen)", StatusCompleted, StatusInProgress, true},
		// Same-status (non-status field edit) always allowed.
		{"draft->draft", StatusDraft, StatusDraft, true},
		{"verified->verified", StatusVerified, StatusVerified, true},
		// AC-15 explicit illegal edges.
		{"draft->completed (skip)", StatusDraft, StatusCompleted, false},
		{"draft->verified (skip)", StatusDraft, StatusVerified, false},
		{"verified->draft (terminal)", StatusVerified, StatusDraft, false},
		{"verified->in_progress (terminal)", StatusVerified, StatusInProgress, false},
		{"completed->draft (back to draft)", StatusCompleted, StatusDraft, false},
		{"in_progress->draft (back to draft)", StatusInProgress, StatusDraft, false},
		{"blocked->draft (back to draft)", StatusBlocked, StatusDraft, false},
		{"in_progress->verified (skip)", StatusInProgress, StatusVerified, false},
		{"blocked->verified (skip)", StatusBlocked, StatusVerified, false},
		// Unknown statuses never allowed.
		{"unknown from", "bogus", StatusInProgress, false},
		{"unknown to", StatusDraft, "bogus", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AllowedTransition(tc.from, tc.to); got != tc.want {
				t.Errorf("AllowedTransition(%q,%q) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestValidateTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		title string
		want  error
	}{
		{"empty", "", ErrTitleRequired},
		{"ok short", "Close the IAC gap", nil},
		{"ok exactly 200", string(make([]byte, 200)), nil},
		{"too long 201", string(make([]byte, 201)), ErrTitleTooLong},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidateTitle(tc.title); !errors.Is(got, tc.want) {
				t.Errorf("ValidateTitle(len=%d) = %v, want %v", len(tc.title), got, tc.want)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	t.Parallel()
	if err := ValidateDescription(""); err != nil {
		t.Errorf("empty description should be allowed, got %v", err)
	}
	if err := ValidateDescription(string(make([]byte, 4000))); err != nil {
		t.Errorf("4000-char description should be allowed, got %v", err)
	}
	if err := ValidateDescription(string(make([]byte, 4001))); !errors.Is(err, ErrDescriptionTooLong) {
		t.Errorf("4001-char description: got %v, want ErrDescriptionTooLong", err)
	}
}

func TestValidateTriggeringEvent(t *testing.T) {
	t.Parallel()
	if err := ValidateTriggeringEvent(""); err != nil {
		t.Errorf("empty triggering_event should be allowed, got %v", err)
	}
	if err := ValidateTriggeringEvent(string(make([]byte, 500))); err != nil {
		t.Errorf("500-char triggering_event should be allowed, got %v", err)
	}
	if err := ValidateTriggeringEvent(string(make([]byte, 501))); !errors.Is(err, ErrTriggeringEventTooLong) {
		t.Errorf("501-char triggering_event: got %v, want ErrTriggeringEventTooLong", err)
	}
}

func TestValidateDueDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	if err := ValidateDueDate(nil, now); err != nil {
		t.Errorf("nil due_date (no deadline) should be allowed, got %v", err)
	}
	within := now.AddDate(4, 11, 0)
	if err := ValidateDueDate(&within, now); err != nil {
		t.Errorf("due_date within 5y should be allowed, got %v", err)
	}
	exactly := now.AddDate(MaxDueDateYears, 0, 0)
	if err := ValidateDueDate(&exactly, now); err != nil {
		t.Errorf("due_date exactly 5y should be allowed, got %v", err)
	}
	tooFar := now.AddDate(MaxDueDateYears, 0, 1)
	if err := ValidateDueDate(&tooFar, now); !errors.Is(err, ErrDueDateTooFar) {
		t.Errorf("due_date > 5y: got %v, want ErrDueDateTooFar", err)
	}
}

func TestValidateDueDate_DefaultsClockWhenZero(t *testing.T) {
	t.Parallel()
	// A due_date far in the past relative to "now" is fine (only the upper
	// horizon is capped). With a zero clock the validator uses time.Now().
	past := time.Now().UTC().AddDate(-1, 0, 0)
	if err := ValidateDueDate(&past, time.Time{}); err != nil {
		t.Errorf("past due_date with default clock: got %v, want nil", err)
	}
}

func TestClampLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want int
	}{
		{0, DefaultPageLimit},
		{-5, DefaultPageLimit},
		{1, 1},
		{25, 25},
		{100, 100},
		{101, MaxPageLimit},
		{10000, MaxPageLimit},
	}
	for _, tc := range cases {
		if got := ClampLimit(tc.in); got != tc.want {
			t.Errorf("ClampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestPlanSnapshot_StableShape(t *testing.T) {
	t.Parallel()
	due := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	p := ActionPlan{
		Title:           "Close gap",
		Description:     "desc",
		TriggeringEvent: "TPRM #4",
		Status:          StatusInProgress,
		DueDate:         &due,
	}
	snap := planSnapshot(p)
	for _, k := range []string{"title", "description", "triggering_event", "owner_id", "status", "due_date"} {
		if _, ok := snap[k]; !ok {
			t.Errorf("planSnapshot missing key %q", k)
		}
	}
	if snap["due_date"] != "2026-12-01" {
		t.Errorf("due_date snapshot = %v, want 2026-12-01", snap["due_date"])
	}
	// No due date -> key omitted.
	p.DueDate = nil
	if _, ok := planSnapshot(p)["due_date"]; ok {
		t.Errorf("planSnapshot should omit due_date when nil")
	}
}

func TestStrPtrAndNilIfEmpty(t *testing.T) {
	t.Parallel()
	if strPtr("") != nil {
		t.Error("strPtr(\"\") should be nil")
	}
	if v := strPtr("x"); v == nil || *v != "x" {
		t.Error("strPtr(\"x\") should point to \"x\"")
	}
	if nilIfEmpty("") != nil {
		t.Error("nilIfEmpty(\"\") should be nil")
	}
	if v := nilIfEmpty("active"); v == nil || *v != "active" {
		t.Error("nilIfEmpty(\"active\") should point to \"active\"")
	}
}

package entra_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
)

// fakeGraph is a faked Microsoft Graph surface — NO live Azure in tests.
type fakeGraph struct {
	assignments []entra.RawAssignment
	err         error
}

func (f *fakeGraph) ListRoleAssignments(_ context.Context) ([]entra.RawAssignment, error) {
	return f.assignments, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

func TestPull_NormalizesAssignments(t *testing.T) {
	api := &fakeGraph{assignments: []entra.RawAssignment{
		{
			ID: "ra-1", PrincipalID: "p-1", PrincipalType: "user",
			PrincipalDisplayName: "Alice", RoleDefinitionID: "role-1",
			RoleDisplayName: "Global Administrator", DirectoryScopeID: "/",
		},
		{
			ID: "ra-2", PrincipalID: "p-2", PrincipalType: "servicePrincipal",
			RoleDefinitionID: "role-2", RoleDisplayName: "Reader",
		},
	}}
	got, err := entra.Pull(context.Background(), api, "tenant-1", fixedNow)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if !got[0].IsPrivileged {
		t.Error("Global Administrator should be flagged privileged")
	}
	if got[1].IsPrivileged {
		t.Error("Reader should not be flagged privileged")
	}
	if got[1].DirectoryScopeID != "/" {
		t.Errorf("empty scope should default to '/'; got %q", got[1].DirectoryScopeID)
	}
	if got[0].TenantID != "tenant-1" {
		t.Errorf("tenant = %q; want tenant-1", got[0].TenantID)
	}
	if !got[0].ObservedAt.Equal(fixedNow()) {
		t.Errorf("observed_at = %v; want injected now", got[0].ObservedAt)
	}
}

func TestPull_SkipsIncompleteAssignments(t *testing.T) {
	api := &fakeGraph{assignments: []entra.RawAssignment{
		{ID: "", PrincipalID: "p", RoleDefinitionID: "r"},   // missing id
		{ID: "ra", PrincipalID: "", RoleDefinitionID: "r"},  // missing principal
		{ID: "ra", PrincipalID: "p", RoleDefinitionID: ""},  // missing role
		{ID: "ok", PrincipalID: "p", RoleDefinitionID: "r"}, // valid
	}}
	got, err := entra.Pull(context.Background(), api, "t", fixedNow)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(got) != 1 || got[0].AssignmentID != "ok" {
		t.Fatalf("expected 1 valid assignment; got %+v", got)
	}
}

func TestPull_NormalizesUnknownPrincipalType(t *testing.T) {
	api := &fakeGraph{assignments: []entra.RawAssignment{
		{ID: "ra", PrincipalID: "p", PrincipalType: "device", RoleDefinitionID: "r"},
	}}
	got, _ := entra.Pull(context.Background(), api, "t", fixedNow)
	if got[0].PrincipalType != entra.PrincipalUnknown {
		t.Errorf("principal_type = %q; want unknown", got[0].PrincipalType)
	}
}

func TestPull_PropagatesListError(t *testing.T) {
	sentinel := errors.New("graph 403")
	_, err := entra.Pull(context.Background(), &fakeGraph{err: sentinel}, "t", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestPull_NilAPIRejected(t *testing.T) {
	_, err := entra.Pull(context.Background(), nil, "t", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

// P0-486-3: the Assignment shape must carry NO PII beyond the display name
// needed to name the assignment. This pins that no mailbox/profile fields can
// be populated — the struct simply has no field for them.
func TestAssignment_NoPIIFields(t *testing.T) {
	api := &fakeGraph{assignments: []entra.RawAssignment{
		{ID: "ra", PrincipalID: "p", PrincipalType: "user",
			PrincipalDisplayName: "Alice", RoleDefinitionID: "r", RoleDisplayName: "Reader"},
	}}
	got, _ := entra.Pull(context.Background(), api, "t", fixedNow)
	a := got[0]
	// display name is the only human-identifying field; assert it is the
	// assignment label, not an email/UPN/mailbox.
	if a.PrincipalDisplayName != "Alice" {
		t.Errorf("display_name = %q; want Alice", a.PrincipalDisplayName)
	}
}

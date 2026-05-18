package authz_test

// Slice 148 — OPA matrix tests for the `calendar` resource type.
//
// Slice 094 shipped the compliance calendar handler + frontend with
// AC-9 stating "accessible to all signed-in users (RBAC: all roles,
// no admin gate)" but never updated the OPA policies to admit the
// new `"calendar"` resource type for non-grc roles. The operator on
// v1.10.0 hits `/v1/calendar` with their issued credential, OPA
// returns `allow=false`, and the React Query in
// `web/app/(authed)/calendar/page.tsx` enters the `isError` branch,
// surfacing "Failed to load calendar events."
//
// This test pins the slice-094-intended admit set at the rego layer
// so a future maintainer accidentally removing one of the new
// admits trips a unit-test failure.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestSlice148_CalendarReadAdmitMatchesAC9 asserts every signed-in
// role admits `GET /v1/calendar`. Slice 094 AC-9 + slice 148 D3.
func TestSlice148_CalendarReadAdmitMatchesAC9(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name      string
		roles     []authz.Role
		wantAllow bool
		reason    string
	}{
		{
			name:      "admin-allow-read",
			roles:     []authz.Role{authz.RoleAdmin},
			wantAllow: true,
			reason:    "admin.rego wildcard allows every action on every resource within tenant",
		},
		{
			name:      "grc_engineer-allow-read",
			roles:     []authz.Role{authz.RoleGRCEngineer},
			wantAllow: true,
			reason:    "grc_engineer.rego wildcard read across tenant-scoped resources — covers calendar without enumeration",
		},
		{
			name:      "auditor-allow-read",
			roles:     []authz.Role{authz.RoleAuditor},
			wantAllow: true,
			reason:    "slice 148 D3: auditor.rego auditor_readable_resources['calendar'] match",
		},
		{
			name:      "viewer-allow-read",
			roles:     []authz.Role{authz.RoleViewer},
			wantAllow: true,
			reason:    "slice 148 D3: viewer.rego viewer_readable_resources['calendar'] match — calendar is cross-business by design",
		},
		{
			name:      "control_owner-allow-read",
			roles:     []authz.Role{authz.RoleControlOwner},
			wantAllow: true,
			reason:    "slice 148 D3: control_owner.rego control_owner_readable_resources['calendar'] match — owners need to see their controls' next-due dates",
		},
		{
			name:      "no-roles-deny-read",
			roles:     nil,
			wantAllow: false,
			reason:    "default-deny baseline when no role rule fires",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-" + tc.name,
					Roles: tc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000148",
				Action:   "read",
				Resource: authz.ResourceInput{Type: "calendar"},
				Request:  authz.RequestInput{Method: "GET", Path: "/v1/calendar"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != tc.wantAllow {
				t.Errorf("allow = %v; want %v; (%s); reason: %s", d.Allow, tc.wantAllow, tc.reason, d.Reason)
			}
		})
	}
}

// TestSlice148_CalendarSubscriptionWriteAdmitMatchesAC14 asserts every
// signed-in role can mint a per-user ICS URL via
// `POST /v1/calendar/subscription`. Slice 094 AC-14 ("Subscribe in
// your calendar" link → every signed-in user) + slice 148 D3.
//
// The admit is path-predicated (`request.path == "/v1/calendar/subscription"`)
// rather than resource-type-wide so future writes on /v1/calendar/*
// do not silently widen the surface — adding a new write path
// requires an explicit rego edit.
func TestSlice148_CalendarSubscriptionWriteAdmitMatchesAC14(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name      string
		roles     []authz.Role
		wantAllow bool
	}{
		{name: "admin", roles: []authz.Role{authz.RoleAdmin}, wantAllow: true},
		{name: "grc_engineer", roles: []authz.Role{authz.RoleGRCEngineer}, wantAllow: true},
		{name: "auditor", roles: []authz.Role{authz.RoleAuditor}, wantAllow: true},
		{name: "viewer", roles: []authz.Role{authz.RoleViewer}, wantAllow: true},
		{name: "control_owner", roles: []authz.Role{authz.RoleControlOwner}, wantAllow: true},
		{name: "no-roles", roles: nil, wantAllow: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-" + tc.name,
					Roles: tc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000148",
				Action:   "write",
				Resource: authz.ResourceInput{Type: "calendar", ID: "subscription"},
				Request:  authz.RequestInput{Method: "POST", Path: "/v1/calendar/subscription"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != tc.wantAllow {
				t.Errorf("allow = %v; want %v; reason: %s", d.Allow, tc.wantAllow, d.Reason)
			}
		})
	}
}

// TestSlice148_CalendarUnscopedWriteDenied pins the slice-094
// constitutional-invariant-2 contract: the calendar package is
// read-only over the four source tables. No role should be admitted
// on a hypothetical `PATCH /v1/calendar` or `POST /v1/calendar`
// write that is NOT the subscription mint — the narrow path
// predicate on the slice-148 write admit is load-bearing here.
//
// The grc_engineer wildcard-read rule does NOT fire on action=write,
// and the slice-148 admit rules all gate on
// `request.path == "/v1/calendar/subscription"` — together this
// keeps every non-admin role default-denied for the unscoped write.
// (Admin's wildcard intentionally admits because admin is by design
// the most permissive role; that matches the slice 094 + 148 P0
// anti-criteria.)
func TestSlice148_CalendarUnscopedWriteDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)

	denied := []authz.Role{
		authz.RoleGRCEngineer,
		authz.RoleAuditor,
		authz.RoleViewer,
		authz.RoleControlOwner,
	}

	for _, role := range denied {
		role := role
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-write-attempt",
					Roles: []authz.Role{role},
				},
				TenantID: "00000000-0000-0000-0000-000000000148",
				Action:   "write",
				Resource: authz.ResourceInput{Type: "calendar"},
				Request:  authz.RequestInput{Method: "POST", Path: "/v1/calendar"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow {
				t.Errorf("role %q should be denied on unscoped POST /v1/calendar; got allow; reason: %s", role, d.Reason)
			}
		})
	}
}

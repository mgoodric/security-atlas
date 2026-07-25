package ssoconfig

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	providers []RawProvider
	teams     RawTeamStats
	roles     RawRoleStats
	provErr   error
	teamErr   error
	roleErr   error
}

func (f *fakeAPI) ListSSOProviders(_ context.Context) ([]RawProvider, error) {
	return f.providers, f.provErr
}
func (f *fakeAPI) TeamStats(_ context.Context) (RawTeamStats, error) { return f.teams, f.teamErr }
func (f *fakeAPI) RoleAssignmentStats(_ context.Context) (RawRoleStats, error) {
	return f.roles, f.roleErr
}

func fixedClock() time.Time { return time.Date(2026, 6, 8, 12, 30, 0, 0, time.UTC) }

func sampleAPI() *fakeAPI {
	return &fakeAPI{
		providers: []RawProvider{
			{Type: "SAML", Enabled: true, RoleMappings: []string{"Editor", "Admin", "Editor"}},
			{Type: "oauth", Enabled: false},
		},
		teams: RawTeamStats{TeamCount: 3, TotalMemberships: 17},
		roles: RawRoleStats{UserAssignments: 12, TeamAssignments: 4, BuiltinAssignments: 2},
	}
}

func TestCollect_HappyPath(t *testing.T) {
	t.Parallel()
	got, err := Collect(context.Background(), sampleAPI(), fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !got.SSOEnabled {
		t.Error("SSOEnabled should be true (SAML enabled)")
	}
	if len(got.Providers) != 2 {
		t.Fatalf("providers = %d; want 2", len(got.Providers))
	}
	// Providers sorted by type: oauth < saml.
	if got.Providers[0].Type != "oauth" || got.Providers[1].Type != "saml" {
		t.Errorf("providers not sorted/normalized: %+v", got.Providers)
	}
	// Role mappings de-duped + sorted: Admin, Editor.
	saml := got.Providers[1]
	if len(saml.RoleMappings) != 2 || saml.RoleMappings[0] != "Admin" || saml.RoleMappings[1] != "Editor" {
		t.Errorf("role mappings not deduped/sorted: %+v", saml.RoleMappings)
	}
	if got.TeamCount != 3 || got.TotalTeamMemberships != 17 {
		t.Errorf("team stats wrong: count=%d members=%d", got.TeamCount, got.TotalTeamMemberships)
	}
	if got.UserRoleAssignments != 12 || got.TeamRoleAssignments != 4 || got.BuiltinRoleAssignments != 2 {
		t.Errorf("role stats wrong: %+v", got)
	}
	if !got.ObservedAt.Equal(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at not truncated to hour: %v", got.ObservedAt)
	}
}

func TestCollect_SSODisabledWhenNoneEnabled(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{providers: []RawProvider{{Type: "ldap", Enabled: false}}}
	got, err := Collect(context.Background(), api, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got.SSOEnabled {
		t.Error("SSOEnabled should be false when no provider is enabled")
	}
}

func TestCollect_DropsEmptyAndNegative(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		providers: []RawProvider{{Type: "  ", Enabled: true}, {Type: "saml", Enabled: true}},
		teams:     RawTeamStats{TeamCount: -1, TotalMemberships: -5},
		roles:     RawRoleStats{UserAssignments: -3},
	}
	got, err := Collect(context.Background(), api, fixedClock)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Type != "saml" {
		t.Errorf("empty-type provider not dropped: %+v", got.Providers)
	}
	if got.TeamCount != 0 || got.TotalTeamMemberships != 0 || got.UserRoleAssignments != 0 {
		t.Errorf("negative counts not clamped: %+v", got)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, fixedClock); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesErrors(t *testing.T) {
	t.Parallel()
	provErr := errors.New("403 sso")
	teamErr := errors.New("403 teams")
	roleErr := errors.New("403 roles")
	if _, err := Collect(context.Background(), &fakeAPI{provErr: provErr}, fixedClock); !errors.Is(err, provErr) {
		t.Errorf("want wrapped provider error; got %v", err)
	}
	if _, err := Collect(context.Background(), &fakeAPI{teamErr: teamErr}, fixedClock); !errors.Is(err, teamErr) {
		t.Errorf("want wrapped team error; got %v", err)
	}
	if _, err := Collect(context.Background(), &fakeAPI{roleErr: roleErr}, fixedClock); !errors.Is(err, roleErr) {
		t.Errorf("want wrapped role error; got %v", err)
	}
}

func TestCollect_DefaultClock(t *testing.T) {
	t.Parallel()
	got, err := Collect(context.Background(), &fakeAPI{}, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got.ObservedAt.IsZero() {
		t.Error("default clock should stamp observed_at")
	}
	if got.ObservedAt.Location() != time.UTC {
		t.Errorf("observed_at not UTC: %v", got.ObservedAt.Location())
	}
}

func TestSanitizeMappings(t *testing.T) {
	t.Parallel()
	if sanitizeMappings(nil) != nil {
		t.Error("nil input should return nil")
	}
	if got := sanitizeMappings([]string{"  ", ""}); got != nil {
		t.Errorf("all-empty input should return nil; got %+v", got)
	}
	got := sanitizeMappings([]string{"  Viewer ", "Editor", "Viewer"})
	if len(got) != 2 || got[0] != "Editor" || got[1] != "Viewer" {
		t.Errorf("dedup/trim/sort failed: %+v", got)
	}
}

// TestStructuralOverCollectionGuard is the load-bearing over-collection guard
// (P0-534): it pins, via reflection, that NONE of the secret-free structs has a
// field capable of holding a SAML private key, an OAuth client secret, an LDAP
// bind password, a signing certificate, OR a user identity (name / email /
// login / user id). If a future edit adds such a field, this test fails — the
// struct field set IS the structural guard.
func TestStructuralOverCollectionGuard(t *testing.T) {
	t.Parallel()
	allowed := map[string]map[string]bool{
		"RawProvider":  {"Type": true, "Enabled": true, "RoleMappings": true},
		"RawTeamStats": {"TeamCount": true, "TotalMemberships": true},
		"RawRoleStats": {"UserAssignments": true, "TeamAssignments": true, "BuiltinAssignments": true},
		"Provider":     {"Type": true, "Enabled": true, "RoleMappings": true},
		"AccessConfig": {
			"SSOEnabled": true, "Providers": true, "TeamCount": true,
			"TotalTeamMemberships": true, "UserRoleAssignments": true,
			"TeamRoleAssignments": true, "BuiltinRoleAssignments": true, "ObservedAt": true,
		},
	}
	// Substrings that must NOT appear in any field name — the excluded secret /
	// identity surfaces. ("assignment(s)" counts are allowed; the banned tokens
	// below are chosen to avoid colliding with the count field names.)
	banned := []string{
		"secret", "token", "key", "password", "passwd", "certificate", "cert",
		"credential", "private", "signing", "email", "login", "username",
		"membername", "memberemail", "principal", "identity", "displayname",
	}
	check := func(typ reflect.Type) {
		perm, ok := allowed[typ.Name()]
		if !ok {
			t.Fatalf("unexpected struct %q under over-collection guard", typ.Name())
		}
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if !perm[name] {
				t.Errorf("%s has un-allow-listed field %q — possible over-collection surface", typ.Name(), name)
			}
			low := strings.ToLower(name)
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%s field %q contains banned substring %q (over-collection)", typ.Name(), name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(RawProvider{}))
	check(reflect.TypeOf(RawTeamStats{}))
	check(reflect.TypeOf(RawRoleStats{}))
	check(reflect.TypeOf(Provider{}))
	check(reflect.TypeOf(AccessConfig{}))
}

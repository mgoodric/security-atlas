package gcpauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewCredential(t *testing.T) {
	t.Parallel()
	if _, err := NewCredential(""); err == nil {
		t.Error("empty credential should error")
	}
	c, err := NewCredential("ya29.secret-access-token-123")
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	if c.Value() != "ya29.secret-access-token-123" {
		t.Errorf("Value() = %q; want raw token back", c.Value())
	}
}

// TestCredentialNeverLogged is the load-bearing secret-redaction guard (slice
// 442 AC-4 / P0-442-4 / threat-model I). Every fmt verb a logger might use on
// a Credential must redact the secret. Only Value() may return it.
func TestCredentialNeverLogged(t *testing.T) {
	t.Parallel()
	const secret = "ya29.super-secret-deadbeef"
	c, err := NewCredential(secret)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, secret) {
			t.Errorf("fmt %q leaked the credential: %q", verb, out)
		}
		if !strings.Contains(strings.ToLower(out), "redact") {
			t.Errorf("fmt %q did not produce a redaction marker: %q", verb, out)
		}
	}
}

// TestRequiredRoles_ReadOnly asserts the documented minimal role set is
// read-only and never includes a banned write/admin/data-read role (slice 442
// threat-model E / P0-442-2 / P0-442-3). storage.objectViewer (data-plane
// read) must NOT be in the required set.
func TestRequiredRoles_ReadOnly(t *testing.T) {
	t.Parallel()
	banned := make(map[string]bool, len(BannedRoles))
	for _, b := range BannedRoles {
		banned[b] = true
	}
	for _, r := range RequiredRoles {
		if banned[r] {
			t.Errorf("RequiredRoles contains banned write/admin/data-read role %q", r)
		}
	}
	// The data-plane object-read role must be explicitly banned.
	if !banned["roles/storage.objectViewer"] {
		t.Error("roles/storage.objectViewer must be a BannedRole — the connector must never read object data")
	}
	if len(RequiredRoles) == 0 {
		t.Error("RequiredRoles must document at least one read-only role")
	}
}

type fakeIdentityAPI struct {
	id  Identity
	err error
}

func (f *fakeIdentityAPI) ResolveProject(_ context.Context) (Identity, error) {
	return f.id, f.err
}

func TestResolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		api     *fakeIdentityAPI
		want    string
		wantErr bool
	}{
		{"ok", &fakeIdentityAPI{id: Identity{ProjectID: "prod-123"}}, "prod-123", false},
		{"api error", &fakeIdentityAPI{err: errors.New("boom")}, "", true},
		{"empty project", &fakeIdentityAPI{id: Identity{ProjectID: ""}}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(context.Background(), tc.api)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.ProjectID != tc.want {
				t.Errorf("ProjectID = %q; want %q", got.ProjectID, tc.want)
			}
		})
	}
}

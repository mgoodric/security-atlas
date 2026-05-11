package githubscim_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubscim"
)

func TestReconcile_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/scim/v2/organizations/example/Users", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/scim+json" {
			t.Errorf("Accept = %q; want application/scim+json", got)
		}
		_, _ = w.Write([]byte(`{
			"schemas": ["urn:ietf:params:scim:api:messages:2.0:ListResponse"],
			"totalResults": 2,
			"Resources": [
				{
					"id": "scim-uuid-1",
					"externalId": "okta-1",
					"userName": "alice@example.com",
					"active": true,
					"emails": [{"value": "alice@example.com", "primary": true}]
				},
				{
					"id": "scim-uuid-2",
					"userName": "bob@example.com",
					"active": false,
					"emails": []
				}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, _ := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_scim"})
	c := githubscim.NewClient(srv.Client(), srv.URL, creds)

	users, err := githubscim.Reconcile(context.Background(), c, "example", func() time.Time {
		return time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len = %d; want 2", len(users))
	}
	if users[0].PrimaryEmail != "alice@example.com" {
		t.Errorf("primary_email = %q; want alice@example.com", users[0].PrimaryEmail)
	}
	if users[1].Active {
		t.Errorf("bob.active = true; want false")
	}
	if users[1].Org != "example" {
		t.Errorf("org = %q", users[1].Org)
	}
}

func TestReconcile_NotFoundIsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	creds, _ := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_nf"})
	c := githubscim.NewClient(srv.Client(), srv.URL, creds)

	_, err := githubscim.Reconcile(context.Background(), c, "noscim", nil)
	if !errors.Is(err, githubscim.ErrSCIMUnavailable) {
		t.Fatalf("err = %v; want ErrSCIMUnavailable", err)
	}
}

func TestReconcile_RejectsEmptyOrg(t *testing.T) {
	creds, _ := githubauth.Resolve(githubauth.ResolveOpts{PAT: "github_pat_empty"})
	c := githubscim.NewClient(http.DefaultClient, "http://example.invalid", creds)
	if _, err := githubscim.Reconcile(context.Background(), c, "", nil); err == nil {
		t.Fatal("expected error on empty org")
	}
}

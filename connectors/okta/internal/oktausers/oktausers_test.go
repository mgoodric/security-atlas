package oktausers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktausers"
)

func TestPull_ActiveUserWithMFAPasses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{
				"id": "u1",
				"status": "ACTIVE",
				"created": "2026-01-01T00:00:00Z",
				"activated": "2026-01-02T00:00:00Z",
				"lastLogin": "2026-05-10T12:00:00Z",
				"profile": {"login": "alice@example.com", "email": "alice@example.com"}
			}
		]`))
	})
	mux.HandleFunc("/api/v1/users/u1/factors", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": "f1", "factorType": "push", "status": "ACTIVE"},
			{"id": "f2", "factorType": "email", "status": "ACTIVE"}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, err := oktausers.Pull(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("len(users) = %d", len(users))
	}
	u := users[0]
	if u.UserID != "u1" {
		t.Errorf("UserID = %q", u.UserID)
	}
	if !u.MFAEnrolled {
		t.Errorf("MFAEnrolled = false; want true (push factor present)")
	}
	if u.Result != oktausers.ResultPass {
		t.Errorf("Result = %q; want pass", u.Result)
	}
	if u.PrimaryEmail != "alice@example.com" {
		t.Errorf("PrimaryEmail = %q", u.PrimaryEmail)
	}
}

func TestPull_ActiveUserNoMFAFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "ACTIVE", "profile": {"login": "bob@x"}}]`))
	})
	mux.HandleFunc("/api/v1/users/u1/factors", func(w http.ResponseWriter, _ *http.Request) {
		// Only password (recovery) — not real MFA.
		_, _ = w.Write([]byte(`[{"id": "fX", "factorType": "password", "status": "ACTIVE"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, err := oktausers.Pull(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if users[0].MFAEnrolled {
		t.Errorf("MFAEnrolled = true; want false (password is recovery, not MFA)")
	}
	if users[0].Result != oktausers.ResultFail {
		t.Errorf("Result = %q; want fail", users[0].Result)
	}
}

func TestPull_DeprovisionedUserFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "DEPROVISIONED", "profile": {"login": "x@y"}}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, _ := oktausers.Pull(context.Background(), client, nil)
	if users[0].Result != oktausers.ResultFail {
		t.Errorf("Result = %q; want fail (deprovisioned)", users[0].Result)
	}
}

func TestPull_SkipsUsersWithoutLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "ACTIVE", "profile": {}}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, _ := oktausers.Pull(context.Background(), client, nil)
	if len(users) != 0 {
		t.Fatalf("len(users) = %d; want 0 (missing login)", len(users))
	}
}

func TestPull_NilAPI(t *testing.T) {
	if _, err := oktausers.Pull(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestPull_FactorListErrorBecomesUnenrolled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "ACTIVE", "profile": {"login": "x@y"}}]`))
	})
	mux.HandleFunc("/api/v1/users/u1/factors", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, _ := oktausers.Pull(context.Background(), client, nil)
	if users[0].MFAEnrolled {
		t.Errorf("MFAEnrolled = true; factor list errored, want false")
	}
}

func TestPull_ParsesActivatedAt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "u1", "status": "ACTIVE", "activated": "2026-01-02T03:04:05Z", "profile": {"login": "x@y"}}]`))
	})
	mux.HandleFunc("/api/v1/users/u1/factors", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "f1", "factorType": "push", "status": "ACTIVE"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktausers.NewClient(srv.Client(), srv.URL, creds)
	users, _ := oktausers.Pull(context.Background(), client, nil)
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !users[0].ActivatedAt.Equal(want) {
		t.Errorf("ActivatedAt = %v; want %v", users[0].ActivatedAt, want)
	}
}

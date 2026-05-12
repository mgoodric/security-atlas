package oktaapps_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

func TestPull_ListsAppsAndAssignments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "SSWS test-token" {
			t.Errorf("Authorization = %q; want SSWS test-token", got)
		}
		_, _ = w.Write([]byte(`[
			{"id": "app1", "label": "Slack", "status": "ACTIVE", "signOnMode": "SAML_2_0"},
			{"id": "app2", "name": "Internal Tool", "status": "ACTIVE", "signOnMode": "OPENID_CONNECT"}
		]`))
	})
	mux.HandleFunc("/api/v1/apps/app1/groups", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "g1"}, {"id": "g2"}]`))
	})
	mux.HandleFunc("/api/v1/apps/app2/groups", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "everyone"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktaapps.NewClient(srv.Client(), srv.URL, creds)

	fixed := func() time.Time { return time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC) }
	assignments, err := oktaapps.Pull(context.Background(), client, fixed)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d; want 2", len(assignments))
	}

	a1 := assignments[0]
	if a1.AppID != "app1" {
		t.Errorf("a1.AppID = %q", a1.AppID)
	}
	if a1.AppName != "Slack" {
		t.Errorf("a1.AppName = %q; want Slack (from label)", a1.AppName)
	}
	if a1.SignOnMode != "SAML_2_0" {
		t.Errorf("a1.SignOnMode = %q", a1.SignOnMode)
	}
	if a1.AssignedGroupCount != 2 {
		t.Errorf("a1.AssignedGroupCount = %d; want 2", a1.AssignedGroupCount)
	}
	if len(a1.AssignedGroupIDs) != 2 || a1.AssignedGroupIDs[0] != "g1" {
		t.Errorf("a1.AssignedGroupIDs = %v", a1.AssignedGroupIDs)
	}

	a2 := assignments[1]
	if a2.AppName != "Internal Tool" {
		t.Errorf("a2.AppName = %q; want Internal Tool (from name fallback)", a2.AppName)
	}
}

func TestPull_GroupListErrorBecomesEmptyAssignment(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "app1", "label": "X", "status": "ACTIVE"}]`))
	})
	mux.HandleFunc("/api/v1/apps/app1/groups", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktaapps.NewClient(srv.Client(), srv.URL, creds)
	assignments, err := oktaapps.Pull(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("len(assignments) = %d", len(assignments))
	}
	if assignments[0].AssignedGroupCount != 0 {
		t.Errorf("AssignedGroupCount = %d; want 0 (group list failed)", assignments[0].AssignedGroupCount)
	}
}

func TestPull_NilAPI(t *testing.T) {
	if _, err := oktaapps.Pull(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on nil API")
	}
}

func TestPull_SkipsAppsWithoutName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/apps", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id": "x", "status": "ACTIVE"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	client := oktaapps.NewClient(srv.Client(), srv.URL, creds)
	assignments, err := oktaapps.Pull(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(assignments) != 0 {
		t.Fatalf("len(assignments) = %d; want 0 (app missing name+label)", len(assignments))
	}
}

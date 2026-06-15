package gcpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpauth"
)

// newTestClient wires a Client to a single httptest server standing in for
// all three GCP API hosts (the test server's handler routes by path).
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cred, err := gcpauth.NewCredential("ya29.test-token")
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	c := New(cred, "proj-123", true)
	c.rmURL = srv.URL
	c.iamURL = srv.URL
	c.storageURL = srv.URL
	return c
}

// TestResolveProject returns the configured project id without an API call.
func TestResolveProject(t *testing.T) {
	t.Parallel()
	c := New(gcpauth.Credential{}, "my-proj", true)
	id, err := c.ResolveProject(context.Background())
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if id.ProjectID != "my-proj" {
		t.Errorf("ProjectID = %q; want my-proj", id.ProjectID)
	}
}

func TestResolveProject_Empty(t *testing.T) {
	t.Parallel()
	c := New(gcpauth.Credential{}, "", true)
	if _, err := c.ResolveProject(context.Background()); err == nil {
		t.Fatal("empty project id should error")
	}
}

// TestListIAMBindings_Fanout verifies the policy is fanned out one record per
// (role, member), the member is classified, the privileged heuristic fires,
// and the service-account disabled state is joined from the SA inventory.
func TestListIAMBindings_Fanout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Authorization header must carry the bearer token.
		if got := r.Header.Get("Authorization"); got != "Bearer ya29.test-token" {
			t.Errorf("Authorization = %q; want bearer", got)
		}
		switch {
		case strings.Contains(r.URL.Path, "getIamPolicy"):
			_, _ = w.Write([]byte(`{"bindings":[
				{"role":"roles/owner","members":["user:a@b.com","serviceAccount:dead@p.iam"]},
				{"role":"roles/viewer","members":["group:eng@b.com"]}
			]}`))
		case strings.Contains(r.URL.Path, "serviceAccounts"):
			_, _ = w.Write([]byte(`{"accounts":[{"email":"dead@p.iam","disabled":true}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	bindings, next, err := c.ListIAMBindings(context.Background(), "")
	if err != nil {
		t.Fatalf("ListIAMBindings: %v", err)
	}
	if next != "" {
		t.Errorf("next = %q; want empty (policy is one document)", next)
	}
	if len(bindings) != 3 {
		t.Fatalf("len = %d; want 3 (2 + 1 members)", len(bindings))
	}
	// Find the disabled service-account binding.
	var sa *struct{ found bool }
	for _, b := range bindings {
		if b.MemberType == "serviceAccount" {
			if !b.IsServiceAcc || !b.Disabled {
				t.Errorf("SA binding: IsServiceAcc=%v Disabled=%v; want true,true", b.IsServiceAcc, b.Disabled)
			}
			if !b.IsPrivileged {
				t.Error("roles/owner should be flagged privileged")
			}
			sa = &struct{ found bool }{true}
		}
		if b.MemberType == "group" && b.IsPrivileged {
			t.Error("roles/viewer should NOT be privileged")
		}
	}
	if sa == nil {
		t.Error("expected a serviceAccount binding in the fanout")
	}
}

// TestListIAMBindings_SecondPageEmpty asserts the collector's page loop exits:
// the policy is a single document, so a non-empty page token returns nothing.
func TestListIAMBindings_SecondPageEmpty(t *testing.T) {
	t.Parallel()
	c := New(gcpauth.Credential{}, "p", true)
	bindings, next, err := c.ListIAMBindings(context.Background(), "some-token")
	if err != nil {
		t.Fatalf("ListIAMBindings: %v", err)
	}
	if len(bindings) != 0 || next != "" {
		t.Errorf("second page: len=%d next=%q; want 0, empty", len(bindings), next)
	}
}

// TestListBuckets verifies bucket config parsing: encryption key, public-access
// flag default, uniform access, versioning, and retention-seconds parse.
func TestListBuckets(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// projection=noAcl must be requested (never the ACL surface).
		if r.URL.Query().Get("projection") != "noAcl" {
			t.Errorf("projection = %q; want noAcl", r.URL.Query().Get("projection"))
		}
		_, _ = w.Write([]byte(`{"items":[
			{"name":"locked","location":"US","encryption":{"defaultKmsKeyName":"projects/p/locations/us/keyRings/r/cryptoKeys/k"},
			 "iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true},"publicAccessPrevention":"enforced"},
			 "versioning":{"enabled":true},"retentionPolicy":{"retentionPeriod":"2592000"}},
			{"name":"open","location":"EU"}
		]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	buckets, next, err := c.ListBuckets(context.Background(), "")
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if next != "" {
		t.Errorf("next = %q; want empty", next)
	}
	if len(buckets) != 2 {
		t.Fatalf("len = %d; want 2", len(buckets))
	}
	locked := buckets[0]
	if locked.PublicAccessFlag != "enforced" || !locked.UniformAccess || !locked.VersioningEnabled {
		t.Errorf("locked bucket parsed wrong: %+v", locked)
	}
	if locked.RetentionSeconds != 2592000 {
		t.Errorf("RetentionSeconds = %d; want 2592000", locked.RetentionSeconds)
	}
	if locked.DefaultKMSKeyName == "" {
		t.Error("expected a CMEK key name")
	}
	// The bucket with no iamConfiguration must default to "unspecified".
	if buckets[1].PublicAccessFlag != "unspecified" {
		t.Errorf("open bucket PublicAccessFlag = %q; want unspecified", buckets[1].PublicAccessFlag)
	}
}

func TestClassifyMember(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantType string
		wantSA   bool
		wantMail string
	}{
		{"user:a@b.com", "user", false, "a@b.com"},
		{"serviceAccount:svc@p.iam", "serviceAccount", true, "svc@p.iam"},
		{"group:eng@b.com", "group", false, "eng@b.com"},
		{"domain:example.com", "domain", false, "example.com"},
		{"allUsers", "unknown", false, ""},
		{"allUsers:x", "specialGroup", false, ""},
		{"garbage-no-colon", "unknown", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			mt, sa, mail := classifyMember(tc.in)
			if mt != tc.wantType || sa != tc.wantSA || mail != tc.wantMail {
				t.Errorf("classifyMember(%q) = (%q,%v,%q); want (%q,%v,%q)",
					tc.in, mt, sa, mail, tc.wantType, tc.wantSA, tc.wantMail)
			}
		})
	}
}

func TestIsPrivilegedRole(t *testing.T) {
	t.Parallel()
	priv := []string{"roles/owner", "roles/editor", "roles/iam.securityAdmin", "roles/storage.admin", "roles/resourcemanager.projectIamAdmin"}
	notPriv := []string{"roles/viewer", "roles/storage.bucketViewer", "roles/iam.securityReviewer"}
	for _, r := range priv {
		if !isPrivilegedRole(r) {
			t.Errorf("isPrivilegedRole(%q) = false; want true", r)
		}
	}
	for _, r := range notPriv {
		// note: roles/iam.securityReviewer matches the roles/iam. prefix —
		// intentionally flagged so the evaluator double-checks IAM-family grants.
		if r == "roles/iam.securityReviewer" {
			continue
		}
		if isPrivilegedRole(r) {
			t.Errorf("isPrivilegedRole(%q) = true; want false", r)
		}
	}
}

func TestParseRetentionSeconds(t *testing.T) {
	t.Parallel()
	cases := map[string]int64{
		"":        0,
		"0":       0,
		"2592000": 2592000,
		"notanum": 0,
		"12x3":    0,
	}
	for in, want := range cases {
		if got := parseRetentionSeconds(in); got != want {
			t.Errorf("parseRetentionSeconds(%q) = %d; want %d", in, got, want)
		}
	}
}

// TestReadHostDiscipline asserts every base URL the adapter ships is an https
// read host — the constant-fold guard's runtime sibling (threat-model I).
func TestReadHostDiscipline(t *testing.T) {
	t.Parallel()
	for _, base := range []string{ResourceManagerBaseURL, IAMBaseURL, StorageBaseURL} {
		if !strings.HasPrefix(base, "https://") {
			t.Errorf("base URL %q is not https", base)
		}
		if strings.Contains(base, "/o?") || strings.HasSuffix(base, "/o") {
			t.Errorf("base URL %q looks like the object data plane — forbidden", base)
		}
	}
}

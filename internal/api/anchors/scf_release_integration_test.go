//go:build integration

package anchors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// releaseServer loads the #1770 two-release fixtures (B current) and boots
// the API on the RLS-enforcing app role.
func releaseServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	scfreleasetest.LoadAB(t, dbtest.NewMigratePool(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(dbtest.NewAppPool(t))
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantA)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return ts, bearer
}

type releaseAnchorResp struct {
	Anchor struct {
		ID          string `json:"id"`
		SCFID       string `json:"scf_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"anchor"`
	SCFRelease string `json:"scf_release"`
}

func getReleaseAnchor(t *testing.T, ts *httptest.Server, bearer, path string) releaseAnchorResp {
	t.Helper()
	resp, body := get(t, ts, path, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d; body=%s", path, resp.StatusCode, body)
	}
	var out releaseAnchorResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("GET %s: unmarshal: %v", path, err)
	}
	return out
}

// #1770: ?scf_release=A returns A's anchor and echoes A; without it the
// read returns the current release's (B's) anchor and echoes B.
func TestGetAnchor_SCFRelease(t *testing.T) {
	ts, bearer := releaseServer(t)

	a := getReleaseAnchor(t, ts, bearer, "/v1/anchors/IAC-06?scf_release="+scfreleasetest.ReleaseA)
	if a.SCFRelease != scfreleasetest.ReleaseA || !strings.HasPrefix(a.Anchor.Description, "Release A:") {
		t.Fatalf("?scf_release=A: release=%q description=%q; want release A's anchor", a.SCFRelease, a.Anchor.Description)
	}

	cur := getReleaseAnchor(t, ts, bearer, "/v1/anchors/IAC-06")
	if cur.SCFRelease != scfreleasetest.ReleaseB || !strings.HasPrefix(cur.Anchor.Description, "Release B:") {
		t.Fatalf("no scf_release: release=%q description=%q; want current release B's anchor", cur.SCFRelease, cur.Anchor.Description)
	}
	if a.Anchor.ID == cur.Anchor.ID {
		t.Fatal("release A and current reads returned the same anchor row")
	}

	// A uuid with no release reads in the release that holds it.
	byID := getReleaseAnchor(t, ts, bearer, "/v1/anchors/"+a.Anchor.ID)
	if byID.SCFRelease != scfreleasetest.ReleaseA {
		t.Fatalf("uuid read echoed %q; want %q", byID.SCFRelease, scfreleasetest.ReleaseA)
	}

	// The reverse traversal takes the same parameter.
	reqs := getReleaseAnchor(t, ts, bearer, "/v1/anchors/IAC-06/requirements?scf_release="+scfreleasetest.ReleaseA)
	if reqs.SCFRelease != scfreleasetest.ReleaseA || reqs.Anchor.ID != a.Anchor.ID {
		t.Fatalf("/requirements?scf_release=A: release=%q id=%s; want %q %s", reqs.SCFRelease, reqs.Anchor.ID, scfreleasetest.ReleaseA, a.Anchor.ID)
	}
}

// An unknown release and a code missing from the named release are both
// 404, with messages that say which.
func TestGetAnchor_SCFRelease_Errors(t *testing.T) {
	ts, bearer := releaseServer(t)
	for path, want := range map[string]string{
		"/v1/anchors/IAC-06?scf_release=no-such-release":                           `unknown SCF release \"no-such-release\"`,
		"/v1/anchors/CRY-03.1?scf_release=" + scfreleasetest.ReleaseA:              `SCF code not in release`,
		"/v1/anchors/IAC-06/requirements?scf_release=no-such-release":              `unknown SCF release \"no-such-release\"`,
		"/v1/anchors/CRY-03.1/requirements?scf_release=" + scfreleasetest.ReleaseA: `SCF code not in release`,
	} {
		resp, body := get(t, ts, path, bearer)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d; want 404; body=%s", path, resp.StatusCode, body)
		}
		if want != "" && !strings.Contains(string(body), want) {
			t.Errorf("GET %s: body %s does not contain %s", path, body, want)
		}
	}
}

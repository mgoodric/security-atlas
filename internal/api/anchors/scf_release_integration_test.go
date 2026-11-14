//go:build integration

package anchors_test

import (
	"context"
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

// Requirement coverage pins anchors to ?scf_release and echoes it; with no
// parameter it stays unpinned and echoes null (#1770 D1). A requirement
// mapped to IAC-06 in both releases shows one anchor per pin and both
// unpinned.
func TestRequirementCoverage_SCFRelease(t *testing.T) {
	ts, bearer := releaseServer(t)
	admin := dbtest.NewMigratePool(t)
	ctx := t.Context()

	fwID, fvID, reqID := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, '1770 fw', 'test', '')`, fwID, "rel1770-"+fwID.String()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '1', 'current')`, fvID, fwID); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'R-1', 'r', '')`, reqID, fvID); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO fw_to_scf_edges
		(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		SELECT gen_random_uuid(), $1, a.id, 'equal', 1.0, 'community_draft', ''
		FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE a.scf_id = 'IAC-06' AND fv.version IN ($2, $3)`,
		reqID, scfreleasetest.ReleaseA, scfreleasetest.ReleaseB); err != nil {
		t.Fatalf("seed edges: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = admin.Exec(c, `DELETE FROM fw_to_scf_edges WHERE framework_requirement_id = $1`, reqID)
		_, _ = admin.Exec(c, `DELETE FROM framework_requirements WHERE id = $1`, reqID)
		_, _ = admin.Exec(c, `DELETE FROM framework_versions WHERE id = $1`, fvID)
		_, _ = admin.Exec(c, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})

	read := func(query string) (release *string, anchors int) {
		t.Helper()
		path := "/v1/requirements/" + reqID.String() + "/coverage" + query
		resp, body := get(t, ts, path, bearer)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d; body=%s", path, resp.StatusCode, body)
		}
		var out struct {
			Anchors    []json.RawMessage `json:"anchors"`
			SCFRelease *string           `json:"scf_release"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out.SCFRelease, len(out.Anchors)
	}

	if rel, n := read("?scf_release=" + scfreleasetest.ReleaseA); rel == nil || *rel != scfreleasetest.ReleaseA || n != 1 {
		t.Errorf("pinned to A: release=%v anchors=%d; want %q and 1", rel, n, scfreleasetest.ReleaseA)
	}
	if rel, n := read(""); rel != nil || n != 2 {
		t.Errorf("unpinned: release=%v anchors=%d; want null and 2", rel, n)
	}
	resp, body := get(t, ts, "/v1/requirements/"+reqID.String()+"/coverage?scf_release=no-such-release", bearer)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `unknown SCF release \"no-such-release\"`) {
		t.Errorf("unknown release: status %d body %s; want 404 naming the release", resp.StatusCode, body)
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

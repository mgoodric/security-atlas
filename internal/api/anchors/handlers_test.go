package anchors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

func newServerWithBearer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return ts, bearer
}

func get(t *testing.T, ts *httptest.Server, path, bearer string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	body := readAll(t, resp)
	return resp, body
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

func TestListAnchors_Authenticated(t *testing.T) {
	ts, bearer := newServerWithBearer(t)
	resp, body := get(t, ts, "/v1/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchors []struct {
			ID     string `json:"id"`
			SCFID  string `json:"scf_id"`
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Anchors) < 10 {
		t.Fatalf("expected >=10 anchors, got %d", len(got.Anchors))
	}
	for _, a := range got.Anchors {
		if a.ID == "" || a.SCFID == "" || a.Family == "" || a.Name == "" {
			t.Fatalf("anchor missing required field: %+v", a)
		}
	}
}

func TestListAnchors_RejectsMissingBearer(t *testing.T) {
	ts, _ := newServerWithBearer(t)
	resp, _ := get(t, ts, "/v1/anchors", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", resp.StatusCode)
	}
}

func TestListAnchors_RejectsInvalidBearer(t *testing.T) {
	ts, _ := newServerWithBearer(t)
	resp, _ := get(t, ts, "/v1/anchors", "not-a-real-token")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", resp.StatusCode)
	}
}

func TestRequirementsForAnchor_Authenticated(t *testing.T) {
	ts, bearer := newServerWithBearer(t)
	resp, body := get(t, ts, "/v1/anchors/anch_iac06/requirements", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchor struct {
			ID    string `json:"id"`
			SCFID string `json:"scf_id"`
		} `json:"anchor"`
		Requirements []struct {
			Requirement struct {
				ID   string `json:"id"`
				Code string `json:"code"`
				Text string `json:"text"`
			} `json:"requirement"`
			FrameworkVersion struct {
				Framework string `json:"framework"`
				Version   string `json:"version"`
			} `json:"framework_version"`
			STRMType string  `json:"strm_type"`
			Strength float64 `json:"strength"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Anchor.SCFID != "IAC-06" {
		t.Fatalf("anchor scf_id = %q; want IAC-06", got.Anchor.SCFID)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected at least one requirement mapping for IAC-06")
	}
	for _, r := range got.Requirements {
		if r.STRMType != "equal" && r.STRMType != "subset_of" && r.STRMType != "intersects" {
			t.Errorf("invalid strm_type %q", r.STRMType)
		}
		if r.Strength < 0 || r.Strength > 1 {
			t.Errorf("strength %f out of [0,1]", r.Strength)
		}
	}
}

func TestRequirementsForAnchor_UnknownIDReturns404(t *testing.T) {
	ts, bearer := newServerWithBearer(t)
	resp, _ := get(t, ts, "/v1/anchors/anch_does_not_exist/requirements", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
}

func TestCORS_PreflightSucceeds(t *testing.T) {
	ts, _ := newServerWithBearer(t)
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/anchors", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d; want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin = %q; want http://localhost:3000", got)
	}
}

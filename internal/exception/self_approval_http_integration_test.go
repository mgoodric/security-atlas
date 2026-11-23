//go:build integration

// #2101 API tests: PATCH /v1/exceptions/{id}/approve follows the
// organisation's Allow self-approval setting, through the full HTTP stack
// (JWT auth, tenancy middleware, handler, store, exceptions_sod) against
// real Postgres under RLS.

package exception_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

type exceptionAPI struct {
	t      *testing.T
	tenant string
	ctrl   uuid.UUID
	srv    *api.Server
	ts     *httptest.Server
}

func newExceptionAPI(t *testing.T) (*exceptionAPI, func(bool)) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	// Minting a token wires the test signer before the handler is built.
	_ = srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	a := &exceptionAPI{t: t, tenant: tenant, ctrl: ctrl, srv: srv, ts: ts}
	return a, func(allow bool) { setAllowSelfApproval(t, admin, tenant, allow) }
}

// person seeds a user holding roles, on the real engine (#3016), and
// returns their id and a bearer for them.
func (a *exceptionAPI) person(roles ...string) (uuid.UUID, string) {
	a.t.Helper()
	return liveroletest.Person(a.t, a.srv, a.tenant, roles...)
}

// newPerson is a bearer for a fresh live grc_engineer: the operator role,
// and an approver by role (#2428).
func (a *exceptionAPI) newPerson() string {
	a.t.Helper()
	_, bearer := a.person("grc_engineer")
	return bearer
}

// bearer returns a bearer for a non-person credential (a machine subject)
// with the approver claims; a person comes from newPerson.

func (a *exceptionAPI) bearer(subject string) string {
	c := testjwt.ApproverFor(uuid.MustParse(a.tenant))
	c.Subject = subject
	return a.srv.IssueTestJWT(a.t, c)
}

func (a *exceptionAPI) do(method, path, bearer, body string) (int, []byte) {
	a.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.ts.URL+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// request files an exception as bearer and returns its id.
func (a *exceptionAPI) request(bearer string) string {
	a.t.Helper()
	body := fmt.Sprintf(`{"control_id":%q,"justification":"vendor blocker","expires_at":%q}`,
		a.ctrl, time.Now().UTC().Add(30*24*time.Hour).Format(time.RFC3339))
	code, raw := a.do(http.MethodPost, "/v1/exceptions", bearer, body)
	if code != http.StatusCreated {
		a.t.Fatalf("create exception: status %d body=%s", code, raw)
	}
	var out struct {
		Exception struct {
			ID          string `json:"id"`
			RequestedBy string `json:"requested_by"`
		} `json:"exception"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		a.t.Fatalf("decode create: %v body=%s", err, raw)
	}
	return out.Exception.ID
}

type approvedException struct {
	Exception struct {
		Status       string `json:"status"`
		RequestedBy  string `json:"requested_by"`
		ApprovedBy   string `json:"approved_by"`
		SelfApproved bool   `json:"self_approved"`
	} `json:"exception"`
}

func TestHTTP_ExceptionApproval_FollowsSelfApprovalSetting(t *testing.T) {
	a, allowSelf := newExceptionAPI(t)
	// Each person is a live grc_engineer, an approver by role (#2428).
	requester, requesterBearer := a.person("grc_engineer")
	asRequester := func() string { return requesterBearer }

	// Off: the requester, from a second session, is refused.
	allowSelf(false)
	id := a.request(asRequester())
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/approve", asRequester(), ""); code != http.StatusForbidden {
		t.Fatalf("self-approval with the setting off: status %d (want 403) body=%s", code, raw)
	}
	// A second person approves, unflagged.
	second, secondBearer := a.person("grc_engineer")
	code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/approve", secondBearer, "")
	var got approvedException
	_ = json.Unmarshal(raw, &got)
	if code != http.StatusOK || got.Exception.ApprovedBy != second.String() || got.Exception.SelfApproved {
		t.Fatalf("second approver: status %d body=%s, want approved by %s and unflagged", code, raw, second)
	}
	if got.Exception.RequestedBy != requester.String() {
		t.Fatalf("requested_by = %q, want the bare user id %s", got.Exception.RequestedBy, requester)
	}

	// On: the requester approves; the exception and its audit log say so.
	allowSelf(true)
	id = a.request(asRequester())
	code, raw = a.do(http.MethodPatch, "/v1/exceptions/"+id+"/approve", asRequester(), "")
	got = approvedException{}
	_ = json.Unmarshal(raw, &got)
	if code != http.StatusOK || got.Exception.Status != "approved" || got.Exception.ApprovedBy != requester.String() || !got.Exception.SelfApproved {
		t.Fatalf("self-approval with the setting on: status %d body=%s, want approved by the requester and flagged", code, raw)
	}
	code, raw = a.do(http.MethodGet, "/v1/exceptions/"+id+"/audit-log", asRequester(), "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"reason":"self-approved: the organisation allows self-approval"`) {
		t.Fatalf("audit log: status %d body=%s, want the approval marked self-approved", code, raw)
	}

	// Even on, a machine credential never approves.
	id = a.request(asRequester())
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/approve", a.bearer("oauth_client:ci"), ""); code != http.StatusForbidden || !strings.Contains(string(raw), "only a person can approve") {
		t.Fatalf("machine approval with the setting on: status %d (want 403) body=%s", code, raw)
	}
}

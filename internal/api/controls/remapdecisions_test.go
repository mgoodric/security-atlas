package controls

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeRemapDecider records the input it saw and returns err.
type fakeRemapDecider struct {
	err      error
	approve  control.RemapApproveInput
	closeIn  control.RemapCloseInput
	bulkBy   string
	calledBy string
}

func (f *fakeRemapDecider) ApproveRemap(_ context.Context, in control.RemapApproveInput) (control.RemapDecision, error) {
	f.approve, f.calledBy = in, "approve"
	return control.RemapDecision{ProposalID: in.ProposalID, Decision: "approved", DecidedBy: in.DecidedBy}, f.err
}

func (f *fakeRemapDecider) RejectRemap(_ context.Context, in control.RemapCloseInput) (control.RemapDecision, error) {
	f.closeIn, f.calledBy = in, "reject"
	return control.RemapDecision{ProposalID: in.ProposalID, Decision: "rejected"}, f.err
}

func (f *fakeRemapDecider) KeepLegacyRemap(_ context.Context, in control.RemapCloseInput) (control.RemapDecision, error) {
	f.closeIn, f.calledBy = in, "keep-legacy"
	return control.RemapDecision{ProposalID: in.ProposalID, Decision: "kept_legacy"}, f.err
}

func (f *fakeRemapDecider) BulkApproveUnchangedRemaps(_ context.Context, by string) (control.RemapBulkResult, error) {
	f.bulkBy, f.calledBy = by, "approve-unchanged"
	return control.RemapBulkResult{Approved: []control.RemapDecision{}, Skipped: []control.RemapBulkSkip{}}, f.err
}

const remapTestTenant = "00000000-0000-0000-0000-000000001776"

func serveRemap(t *testing.T, f *fakeRemapDecider, cred *credstore.Credential, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	NewRemapDecisionsHandler(f).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ctx := req.Context()
	if cred != nil {
		ctx = authctx.WithCredential(ctx, *cred)
		var err error
		if ctx, err = tenancy.WithTenant(ctx, remapTestTenant); err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func TestRemapDecisions_PersonOnlyAndPrincipalRecorded(t *testing.T) {
	t.Parallel()
	person := credstore.Credential{ID: "key_p", TenantID: remapTestTenant, UserID: "11111111-1111-1111-1111-111111111111"}
	jwt := credstore.Credential{ID: "jwt:abc", TenantID: remapTestTenant, UserID: "user:22222222-2222-2222-2222-222222222222"}
	for _, c := range []struct {
		name string
		cred *credstore.Credential
		code int
		by   string
	}{
		{"no credential", nil, http.StatusUnauthorized, ""},
		{"unbound key", &credstore.Credential{ID: "key_m", TenantID: remapTestTenant, UserID: "key_m"}, http.StatusForbidden, ""},
		{"machine token", &credstore.Credential{ID: "tok", TenantID: remapTestTenant, UserID: "oauth_client:abc"}, http.StatusForbidden, ""},
		{"bootstrap", &credstore.Credential{ID: "boot", TenantID: remapTestTenant, UserID: "bootstrap-admin"}, http.StatusForbidden, ""},
		{"person key", &person, http.StatusOK, person.UserID},
		{"person session", &jwt, http.StatusOK, "22222222-2222-2222-2222-222222222222"},
	} {
		f := &fakeRemapDecider{}
		rr := serveRemap(t, f, c.cred, "/v1/controls/remap-proposals/p1/approve", `{"scf_id":"IAC-60"}`)
		if rr.Code != c.code {
			t.Errorf("%s: status %d; want %d (%s)", c.name, rr.Code, c.code, rr.Body.String())
		}
		if c.code == http.StatusOK && (f.approve.DecidedBy != c.by || f.approve.SCFID != "IAC-60" || f.approve.ProposalID != "p1") {
			t.Errorf("%s: store saw %+v", c.name, f.approve)
		}
		if c.code != http.StatusOK && f.calledBy != "" {
			t.Errorf("%s: store was called", c.name)
		}
	}
}

func TestRemapDecisions_RoutesAndErrorMapping(t *testing.T) {
	t.Parallel()
	person := credstore.Credential{ID: "key_p", TenantID: remapTestTenant, UserID: "11111111-1111-1111-1111-111111111111"}
	for _, verb := range []string{"reject", "keep-legacy"} {
		f := &fakeRemapDecider{}
		rr := serveRemap(t, f, &person, "/v1/controls/remap-proposals/p2/"+verb, `{"reason":"no"}`)
		if rr.Code != http.StatusOK || f.calledBy != verb || f.closeIn.Reason != "no" || f.closeIn.ProposalID != "p2" {
			t.Errorf("%s: %d, store %s %+v", verb, rr.Code, f.calledBy, f.closeIn)
		}
	}
	f := &fakeRemapDecider{}
	if rr := serveRemap(t, f, &person, "/v1/controls/remap-proposals/approve-unchanged", ""); rr.Code != http.StatusOK || f.bulkBy != person.UserID {
		t.Errorf("approve-unchanged: %d by %q", rr.Code, f.bulkBy)
	}
	if rr := serveRemap(t, &fakeRemapDecider{}, &person, "/v1/controls/remap-proposals/p1/approve", `{"bogus":1}`); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d; want 400", rr.Code)
	}

	for _, c := range []struct {
		err  error
		code int
	}{
		{control.ErrRemapProposalNotFound, http.StatusNotFound},
		{control.ErrRemapProposalDecided, http.StatusConflict},
		{control.ErrRemapControlChanged, http.StatusConflict},
		{control.ErrRemapBulkRunning, http.StatusConflict},
		{control.ErrRemapAnchorRequired, http.StatusBadRequest},
		{control.ErrRemapAnchorNotInRelease, http.StatusBadRequest},
		{control.ErrRemapReasonTooLong, http.StatusBadRequest},
		{control.ErrRemapDeciderRequired, http.StatusForbidden},
		{errors.New("boom"), http.StatusInternalServerError},
	} {
		rr := serveRemap(t, &fakeRemapDecider{err: c.err}, &person, "/v1/controls/remap-proposals/p1/approve", "")
		if rr.Code != c.code {
			t.Errorf("%v: status %d; want %d", c.err, rr.Code, c.code)
		}
		if c.code == http.StatusInternalServerError && strings.Contains(rr.Body.String(), "boom") {
			t.Errorf("internal error leaked: %s", rr.Body.String())
		}
	}
}

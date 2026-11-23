//go:build integration

// #2140: GET /v1/exceptions?control_id= returns one control's exceptions,
// through the full HTTP stack (JWT auth, tenancy, authz, handler, store)
// against real Postgres under RLS. The control page lists them; before
// this filter the parameter was ignored and every control's page listed
// every exception in the tenant.

package exception_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHTTP_ListExceptions_FiltersByControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenant)
	ctrlB := seedControl(t, admin, tenant)
	app := dbtest.NewAppPool(t)
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	liveroletest.Attach(t, srv, app)
	_ = srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	a := &exceptionAPI{t: t, tenant: tenant, ctrl: ctrlA, srv: srv, ts: ts}

	requester := a.newPerson()
	onA := a.request(requester)
	a.ctrl = ctrlB
	onB := a.request(requester)

	// A second tenant's exception on its own control never shows here.
	otherTenant := freshTenant(t, admin)
	otherCtrl := seedControl(t, admin, otherTenant)
	other := &exceptionAPI{t: t, tenant: otherTenant, ctrl: otherCtrl, srv: srv, ts: ts}
	onOther := other.request(other.newPerson())

	list := func(query string) []string {
		t.Helper()
		code, raw := a.do(http.MethodGet, "/v1/exceptions"+query, requester, "")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/exceptions%s: status %d body=%s", query, code, raw)
		}
		var out struct {
			Exceptions []struct {
				ID        string `json:"id"`
				ControlID string `json:"control_id"`
			} `json:"exceptions"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode list: %v body=%s", err, raw)
		}
		if out.Count != len(out.Exceptions) {
			t.Fatalf("count %d, %d rows", out.Count, len(out.Exceptions))
		}
		ids := make([]string, len(out.Exceptions))
		for i, e := range out.Exceptions {
			ids[i] = e.ID
		}
		return ids
	}
	has := func(ids []string, id string) bool {
		for _, got := range ids {
			if got == id {
				return true
			}
		}
		return false
	}

	all := list("")
	if !has(all, onA) || !has(all, onB) || has(all, onOther) {
		t.Fatalf("unfiltered = %v, want %s and %s and not %s", all, onA, onB, onOther)
	}

	if got := list("?control_id=" + ctrlA.String()); len(got) != 1 || got[0] != onA {
		t.Fatalf("control A = %v, want only %s", got, onA)
	}
	if got := list("?control_id=" + ctrlB.String()); len(got) != 1 || got[0] != onB {
		t.Fatalf("control B = %v, want only %s", got, onB)
	}
	// Another tenant's control id reads as no exceptions, not theirs.
	if got := list("?control_id=" + otherCtrl.String()); len(got) != 0 {
		t.Fatalf("other tenant's control = %v, want none", got)
	}
	// The filters compose.
	if got := list("?status=requested&control_id=" + ctrlA.String()); len(got) != 1 || got[0] != onA {
		t.Fatalf("requested on control A = %v, want only %s", got, onA)
	}
	if got := list("?status=active&control_id=" + ctrlA.String()); len(got) != 0 {
		t.Fatalf("active on control A = %v, want none", got)
	}

	if code, raw := a.do(http.MethodGet, "/v1/exceptions?control_id=not-a-uuid", requester, ""); code != http.StatusBadRequest {
		t.Fatalf("bad control_id: status %d (want 400) body=%s", code, raw)
	}
}

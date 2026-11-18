//go:build integration

// #2113 API tests, through the full HTTP stack (JWT auth, tenancy, authz,
// handler, store) against real Postgres under RLS:
//
//   - PATCH /v1/exceptions/{id}/revoke ends an active exception, records
//     who revoked it, and writes a 'revoked' audit row carrying the reason;
//   - a non-approver cannot revoke, and nothing changes;
//   - POST /v1/exceptions refuses a null scope predicate (the picker's
//     "incomplete" value, which the evaluator would read as every cell)
//     and stores the explicit always-true predicate as sent.
//
// The requester-approval rule is covered by
// TestHTTP_ExceptionApproval_FollowsSelfApprovalSetting.

package exception_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

type exceptionBody struct {
	Exception struct {
		ID                 string          `json:"id"`
		Status             string          `json:"status"`
		RevokedBy          string          `json:"revoked_by"`
		RevokedAt          *time.Time      `json:"revoked_at"`
		ExpiresAt          time.Time       `json:"expires_at"`
		ScopeCellPredicate json.RawMessage `json:"scope_cell_predicate"`
	} `json:"exception"`
}

func decodeException(t *testing.T, raw []byte) exceptionBody {
	t.Helper()
	var out exceptionBody
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode exception: %v body=%s", err, raw)
	}
	return out
}

// activeException files, approves (as a second person) and activates an
// exception, returning its id.
func (a *exceptionAPI) activeException(requester, approver string) string {
	a.t.Helper()
	id := a.request(requester)
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/approve", approver, ""); code != http.StatusOK {
		a.t.Fatalf("approve: status %d body=%s", code, raw)
	}
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/activate", approver, "{}"); code != http.StatusOK {
		a.t.Fatalf("activate: status %d body=%s", code, raw)
	}
	return id
}

func TestHTTP_ExceptionRevoke_EndsActiveAndLogs(t *testing.T) {
	a, allowSelf := newExceptionAPI(t)
	allowSelf(false)
	requester := a.bearer("user:" + uuid.New().String())
	approverID := uuid.New().String()
	approver := a.bearer("user:" + approverID)
	id := a.activeException(requester, approver)

	// A reason is required.
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/revoke", approver, `{"reason":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("revoke without a reason: status %d (want 400) body=%s", code, raw)
	}

	code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/revoke", approver, `{"reason":"MFA rolled out early"}`)
	if code != http.StatusOK {
		t.Fatalf("revoke: status %d body=%s", code, raw)
	}
	got := decodeException(t, raw)
	if got.Exception.Status != "revoked" || got.Exception.RevokedBy != approverID || got.Exception.RevokedAt == nil {
		t.Fatalf("revoke: body=%s, want revoked by %s with a revoked_at", raw, approverID)
	}
	if !got.Exception.ExpiresAt.After(*got.Exception.RevokedAt) {
		t.Fatalf("revoke: expires_at %s not after revoked_at %s; revoke must end it early, not move the expiry",
			got.Exception.ExpiresAt, got.Exception.RevokedAt)
	}

	// It no longer counts as active.
	code, raw = a.do(http.MethodGet, "/v1/exceptions?status=active", approver, "")
	if code != http.StatusOK || strings.Contains(string(raw), id) {
		t.Fatalf("active list after revoke: status %d body=%s, want %s absent", code, raw, id)
	}

	// The audit log carries the revoke, its actor and its reason.
	code, raw = a.do(http.MethodGet, "/v1/exceptions/"+id+"/audit-log", approver, "")
	if code != http.StatusOK {
		t.Fatalf("audit log: status %d body=%s", code, raw)
	}
	var log struct {
		Entries []struct {
			Action    string `json:"action"`
			Actor     string `json:"actor"`
			FromState string `json:"from_state"`
			ToState   string `json:"to_state"`
			Reason    string `json:"reason"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("decode audit log: %v", err)
	}
	last := log.Entries[len(log.Entries)-1]
	if last.Action != "revoked" || last.Actor != approverID || last.FromState != "active" ||
		last.ToState != "revoked" || last.Reason != "MFA rolled out early" {
		t.Fatalf("last audit row = %+v, want revoked active->revoked by %s with the reason", last, approverID)
	}

	// Revoked is terminal: a second revoke is a wrong-state 409.
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/revoke", approver, `{"reason":"again"}`); code != http.StatusConflict {
		t.Fatalf("second revoke: status %d (want 409) body=%s", code, raw)
	}
}

func TestHTTP_ExceptionRevoke_NonApproverRefused(t *testing.T) {
	a, allowSelf := newExceptionAPI(t)
	allowSelf(false)
	approver := a.bearer("user:" + uuid.New().String())
	id := a.activeException(a.bearer("user:"+uuid.New().String()), approver)

	tenant := uuid.MustParse(a.tenant)
	for name, claims := range map[string]func() string{
		// The authz layer admits grc_engineer to revoke; the handler
		// still requires the approver flag, which this JWT lacks.
		"grc_engineer without approver": func() string {
			c := testjwt.OwnerFor(tenant, []string{"grc_engineer"})
			c.Subject = "user:" + uuid.New().String()
			return a.srv.IssueTestJWT(t, c)
		},
		"viewer": func() string {
			c := testjwt.ViewerFor(tenant)
			c.Subject = "user:" + uuid.New().String()
			return a.srv.IssueTestJWT(t, c)
		},
	} {
		code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+id+"/revoke", claims(), `{"reason":"not mine to end"}`)
		if code != http.StatusForbidden {
			t.Fatalf("%s revoke: status %d (want 403) body=%s", name, code, raw)
		}
	}

	code, raw := a.do(http.MethodGet, "/v1/exceptions/"+id, approver, "")
	if code != http.StatusOK || decodeException(t, raw).Exception.Status != "active" {
		t.Fatalf("after refused revokes: status %d body=%s, want still active", code, raw)
	}
	code, raw = a.do(http.MethodGet, "/v1/exceptions/"+id+"/audit-log", approver, "")
	if code != http.StatusOK || strings.Contains(string(raw), `"revoked"`) {
		t.Fatalf("audit log after refused revokes: status %d body=%s, want no revoked row", code, raw)
	}
}

func TestHTTP_ExceptionCreate_RefusesNullPredicate(t *testing.T) {
	a, _ := newExceptionAPI(t)
	requester := a.bearer("user:" + uuid.New().String())
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	body := func(pred string) string {
		return fmt.Sprintf(`{"control_id":%q,"scope_cell_predicate":%s,"justification":"vendor blocker","expires_at":%q}`,
			a.ctrl, pred, expires)
	}

	for _, pred := range []string{`null`, `[]`, `"everything"`, `{"op":"bogus"}`, `{"op":"eq","dim":"environment"}`} {
		code, raw := a.do(http.MethodPost, "/v1/exceptions", requester, body(pred))
		if code != http.StatusBadRequest {
			t.Fatalf("predicate %s: status %d (want 400) body=%s", pred, code, raw)
		}
	}
	code, raw := a.do(http.MethodGet, "/v1/exceptions", requester, "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"count":0`) {
		t.Fatalf("after refused creates: status %d body=%s, want no exceptions stored", code, raw)
	}

	// The whole control is the explicit always-true predicate, stored as sent.
	code, raw = a.do(http.MethodPost, "/v1/exceptions", requester, body(`{"op":"true"}`))
	if code != http.StatusCreated {
		t.Fatalf(`predicate {"op":"true"}: status %d body=%s`, code, raw)
	}
	var stored map[string]any
	if err := json.Unmarshal(decodeException(t, raw).Exception.ScopeCellPredicate, &stored); err != nil || stored["op"] != "true" {
		t.Fatalf("stored predicate = %s, want {\"op\":\"true\"}", decodeException(t, raw).Exception.ScopeCellPredicate)
	}
}

// #2343: approve, deny, activate and revoke are people-only moves. A
// machine credential with the approver role gets 403 on each, and the
// exception stays where it was.
func TestHTTP_ExceptionLifecycle_MachineCredentialRefused(t *testing.T) {
	a, allowSelf := newExceptionAPI(t)
	allowSelf(false)
	requester := a.bearer("user:" + uuid.New().String())
	approver := a.bearer("user:" + uuid.New().String())
	machine := a.bearer("oauth_client:ci")

	requested := a.request(requester)
	approved := a.request(requester)
	if code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+approved+"/approve", approver, ""); code != http.StatusOK {
		t.Fatalf("approve: status %d body=%s", code, raw)
	}
	active := a.activeException(requester, approver)

	for _, tc := range []struct {
		verb, id, body, state string
	}{
		{"approve", requested, "", "requested"},
		{"deny", requested, `{"reason":"no"}`, "requested"},
		{"activate", approved, "{}", "approved"},
		{"revoke", active, `{"reason":"done"}`, "active"},
	} {
		code, raw := a.do(http.MethodPatch, "/v1/exceptions/"+tc.id+"/"+tc.verb, machine, tc.body)
		if code != http.StatusForbidden || !strings.Contains(string(raw), "only a person can "+tc.verb) {
			t.Fatalf("machine %s: status %d (want 403) body=%s", tc.verb, code, raw)
		}
		code, raw = a.do(http.MethodGet, "/v1/exceptions/"+tc.id, approver, "")
		if code != http.StatusOK || decodeException(t, raw).Exception.Status != tc.state {
			t.Fatalf("after machine %s: status %d body=%s, want still %s", tc.verb, code, raw, tc.state)
		}
	}
}

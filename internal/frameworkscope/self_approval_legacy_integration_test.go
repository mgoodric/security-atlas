//go:build integration

// #2101 integration tests, database legs and pre-#2101 rows: the
// self_approval_requires_setting trigger refuses a direct self_approved
// write while the setting is off, and a scope in review from before #2101
// (no recorded submitter, or one recorded by session) must be re-submitted
// before approval. The migration backfill maps a submit made with a key
// bound to a user to that user.

package frameworkscope_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDB_SelfApprovedFlagNeedsTheSetting(t *testing.T) {
	h := newSelfApprovalHarness(t)
	id := h.submitted("Direct write scope", h.person(uuid.New()))
	h.call(http.MethodPatch, "/v1/framework-scopes/"+id+"/approve", h.person(uuid.New()), http.StatusOK)

	const flag = `UPDATE framework_scopes SET self_approved = true WHERE id = $1`
	_, err := h.admin.Exec(context.Background(), flag, id)
	if err == nil || !strings.Contains(err.Error(), "Allow self-approval") {
		t.Fatalf("direct self_approved write with the setting off: err = %v, want the trigger to refuse", err)
	}
	h.allowSelfApproval(true)
	if _, err := h.admin.Exec(context.Background(), flag, id); err != nil {
		t.Fatalf("direct self_approved write with the setting on: %v", err)
	}
}

func TestHTTP_Approve_UnrecordedSubmitterMustResubmit(t *testing.T) {
	h := newSelfApprovalHarness(t)
	ctx := context.Background()
	submitter := uuid.New()
	second := uuid.New()
	approve := func(scope string, who uuid.UUID) (int, string) {
		resp, body := doJSON(t, http.MethodPatch, h.ts.URL+"/v1/framework-scopes/"+scope+"/approve", h.person(who), "")
		return resp.StatusCode, string(body)
	}
	clearSubmitter := func(scope string) {
		if _, err := h.admin.Exec(ctx, `UPDATE framework_scopes SET submitted_by = NULL WHERE id = $1`, scope); err != nil {
			t.Fatalf("clear submitted_by: %v", err)
		}
	}
	submittedBy := func(scope string) string {
		var s string
		if err := h.admin.QueryRow(ctx, `SELECT submitted_by FROM framework_scopes WHERE id = $1`, scope).Scan(&s); err != nil {
			t.Fatalf("read submitted_by: %v", err)
		}
		return s
	}

	// No recorded submitter: refused, then re-submitted and approved.
	unrecorded := h.submitted("Unrecorded scope", h.person(submitter))
	clearSubmitter(unrecorded)
	if code, body := approve(unrecorded, second); code != http.StatusConflict || !strings.Contains(body, "re-submitted") {
		t.Fatalf("approve with no recorded submitter: status %d (want 409) body=%s", code, body)
	}
	h.call(http.MethodPatch, "/v1/framework-scopes/"+unrecorded+"/submit", h.person(submitter), http.StatusOK)
	h.call(http.MethodPatch, "/v1/framework-scopes/"+unrecorded+"/approve", h.person(second), http.StatusOK)

	// Two pre-#2101 scopes: one last submitted with a key bound to the
	// submitter, one under a session id. The backfill fills both.
	keyID := uuid.New()
	if _, err := h.admin.Exec(ctx, `
		INSERT INTO api_keys (id, tenant_id, token_hash, issued_by)
		VALUES ($1, $2, sha256(gen_random_uuid()::text::bytea), $3)`,
		keyID, h.tenant, submitter); err != nil {
		t.Fatalf("seed bound key: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.admin.Exec(context.Background(), `DELETE FROM api_keys WHERE id = $1`, keyID)
	})
	keyed := h.submitted("Key-submitted scope", h.person(submitter))
	session := h.submitted("Session-submitted scope", h.person(submitter))
	for scope, actor := range map[string]string{keyed: "key_" + keyID.String(), session: "jwt:pre-2101-token"} {
		if _, err := h.admin.Exec(ctx, `
			INSERT INTO framework_scope_workflow_log
			    (tenant_id, framework_scope_id, action, from_state, to_state, after, actor, occurred_at)
			VALUES ($1, $2, 'submit', 'draft', 'review', '{}', $3, now() + interval '1 second')`,
			h.tenant, scope, actor); err != nil {
			t.Fatalf("seed legacy submit row: %v", err)
		}
		clearSubmitter(scope)
	}
	if _, err := h.admin.Exec(ctx, `SELECT framework_scopes_backfill_submitted_by()`); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := submittedBy(keyed); got != submitter.String() {
		t.Fatalf("key-submitted scope: submitted_by %q, want the key's user %s", got, submitter)
	}
	if got := submittedBy(session); got != "jwt:pre-2101-token" {
		t.Fatalf("session-submitted scope: submitted_by %q, want the session id kept", got)
	}

	// The mapped submitter cannot approve their own scope; a second person can.
	if code, body := approve(keyed, submitter); code != http.StatusForbidden {
		t.Fatalf("mapped submitter approving: status %d (want 403) body=%s", code, body)
	}
	h.call(http.MethodPatch, "/v1/framework-scopes/"+keyed+"/approve", h.person(second), http.StatusOK)

	// The session-submitted scope must be re-submitted first.
	if code, body := approve(session, second); code != http.StatusConflict {
		t.Fatalf("session-submitted approve: status %d (want 409) body=%s", code, body)
	}
	h.call(http.MethodPatch, "/v1/framework-scopes/"+session+"/submit", h.person(submitter), http.StatusOK)
	h.call(http.MethodPatch, "/v1/framework-scopes/"+session+"/approve", h.person(second), http.StatusOK)
}

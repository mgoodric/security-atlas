//go:build integration

// #2910 integration tests — the sync drift comparison ignores whitespace
// (line endings, trailing whitespace, blank-line runs, trailing newlines)
// and still reports a content change, on a takeover in place and on a
// same-version re-sync. Recorded hashes stay those of the body as sent.
// The takeover audit rows carry the drifted page hash (in place) and the
// imported row's status change (chained). Real Postgres under RLS through
// the full HTTP stack, as sync_takeover_2854_integration_test.go.

package policies_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// reflow is body as a different Markdown converter might emit it: CRLF
// line endings, trailing spaces, doubled blank lines and trailing
// newlines, with the same content.
func reflow(body string) string {
	return strings.ReplaceAll(strings.ReplaceAll(body, "\n\n", "\n\n\n"), "\n", "  \r\n") + "\r\n\r\n"
}

type takeoverHashes struct {
	body, drift, previousState, newState string
}

func takeoverAuditHashes(t *testing.T, s setupResult, policyID string) takeoverHashes {
	t.Helper()
	var h takeoverHashes
	if err := s.admin.QueryRow(context.Background(), `
		SELECT COALESCE(body_sha256, ''), COALESCE(sync_drift_body_sha256, ''),
		       COALESCE(previous_state, ''), COALESCE(new_state, '')
		FROM policy_audit_log
		WHERE tenant_id = $1 AND policy_id = $2 AND action = 'sync_takeover'`, s.tenant, policyID).
		Scan(&h.body, &h.drift, &h.previousState, &h.newState); err != nil {
		t.Fatalf("read takeover audit row: %v", err)
	}
	return h
}

// A page whose body differs from the imported text only in whitespace is
// taken over without drift, and its audit row carries no drifted hash.
func TestSync2910_TakeOverWhitespaceOnlyIsNotDrift(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, body := importPolicy(t, s, "Access Control Policy", "1.0")
	page := reflow(body)
	if page == body {
		t.Fatalf("reflow left the body unchanged; the test proves nothing")
	}

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6001", "Access Control Policy", "1.0", page, nil))
	res := resp.Results[0]
	// Body drift only: the page's properties differ from the import
	// fixture's, which is property drift (#2877) and not under test here.
	if resp.TakenOver != 1 || res.PolicyID != imported || res.DriftDetectedAt != nil || strings.Contains(res.Reason, "page body") {
		t.Fatalf("sync = %+v; want taken_over of %s without body drift", resp, imported)
	}
	v := getPolicyVersion(t, s.setupResult, imported)
	if v.BodyMd != body || v.SyncDrift != nil {
		t.Errorf("policy = %+v; want the imported body kept and no drift", v)
	}
	if h := takeoverAuditHashes(t, s.setupResult, imported); h.body != sha(body) || h.drift != "" {
		t.Errorf("takeover audit = %+v; want body_sha256 %s and no drifted hash", h, sha(body))
	}

	again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6001", "Access Control Policy", "1.0", page, nil))
	if r := again.Results[0]; r.DriftDetectedAt != nil || strings.Contains(r.Reason, "page body") {
		t.Errorf("re-push = %+v; want no body drift", again)
	}
}

// A content change is still drift on a takeover in place, and the audit
// row records the page's hash next to the row's own.
func TestSync2910_TakeOverContentChangeIsDriftAndAudited(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, body := importPolicy(t, s, "Data Retention Policy", "3.0")
	edited := reflow(body) + "Records are kept for seven years."

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6101", "Data Retention Policy", "3.0", edited, nil))
	if res := resp.Results[0]; resp.TakenOver != 1 || res.DriftDetectedAt == nil {
		t.Fatalf("sync = %+v; want taken_over with drift", resp)
	}
	if v := getPolicyVersion(t, s.setupResult, imported); v.SyncDrift == nil || v.SyncDrift.SourceBodySHA256 != sha(edited) {
		t.Errorf("policy drift = %+v; want the page's hash as sent, %s", v.SyncDrift, sha(edited))
	}
	if h := takeoverAuditHashes(t, s.setupResult, imported); h.body != sha(body) || h.drift != sha(edited) {
		t.Errorf("takeover audit = %+v; want body_sha256 %s and drifted hash %s", h, sha(body), sha(edited))
	}
}

// The chained takeover's audit row on the imported policy records its
// status change, published to superseded.
func TestSync2910_TakeOverChainedAuditsStateChange(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, body := importPolicy(t, s, "Incident Response Policy", "1.0")

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6201", "Incident Response Policy", "2.0", body, nil))
	if resp.TakenOver != 1 || resp.Results[0].PredecessorID != imported {
		t.Fatalf("sync = %+v; want taken_over chained to %s", resp, imported)
	}
	if h := takeoverAuditHashes(t, s.setupResult, imported); h.previousState != "published" || h.newState != "superseded" || h.drift != "" {
		t.Errorf("takeover audit = %+v; want published to superseded, no drifted hash", h)
	}
}

// On a synced page's same-version re-sync, a whitespace-only difference
// is unchanged and clears nothing it should not; a content change is
// drift; reverting to a whitespace variant of the synced body clears it.
func TestSync2910_SameVersionWhitespaceIsUnchanged(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	// No surrounding whitespace: the endpoint trims the body at parse.
	const body = "# Vendor Policy\n\nVendors are reviewed yearly.\n\n- Tier 1: annual\n- Tier 2: biennial"
	first := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6301", "Vendor Policy", "1.0", body, nil))
	if first.Created != 1 {
		t.Fatalf("first sync = %+v; want created", first)
	}
	id := first.Results[0].PolicyID

	ws := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6301", "Vendor Policy", "1.0", reflow(body), nil))
	if ws.Unchanged != 1 || ws.Drift != 0 || ws.Results[0].DriftDetectedAt != nil {
		t.Fatalf("whitespace-only re-sync = %+v; want unchanged", ws)
	}
	if v := getPolicyVersion(t, s.setupResult, id); v.BodyMd != body {
		t.Errorf("body after whitespace-only re-sync = %q; want %q kept", v.BodyMd, body)
	}

	changed := strings.Replace(body, "yearly", "quarterly", 1)
	drift := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6301", "Vendor Policy", "1.0", changed, nil))
	if drift.Drift != 1 || drift.Results[0].DriftDetectedAt == nil {
		t.Fatalf("content re-sync = %+v; want drift", drift)
	}

	back := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6301", "Vendor Policy", "1.0", reflow(body), nil))
	if back.Unchanged != 1 || back.Results[0].DriftDetectedAt != nil {
		t.Errorf("revert to a whitespace variant = %+v; want unchanged with drift cleared", back)
	}
}

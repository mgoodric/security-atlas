//go:build integration

// #2877 integration tests — POST /v1/policies:sync property drift. A page
// whose Page Properties changed without a version bump writes no version
// and applies nothing in place (Matt, 2026-11-21): it is reported as
// drift naming the changed keys, and recorded on the policy separately
// from body drift, until the properties match again or a new version
// arrives. Real Postgres under RLS through the full HTTP stack, as
// sync_integration_test.go.

package policies_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// nextReviewAt reads a policy's stored next_review_at as a date.
func nextReviewAt(t *testing.T, s setupResult, id string) string {
	t.Helper()
	var at *time.Time
	if err := s.admin.QueryRow(context.Background(),
		`SELECT next_review_at FROM policies WHERE tenant_id = $1 AND id = $2`, s.tenant, uuid.MustParse(id)).Scan(&at); err != nil {
		t.Fatalf("read next_review_at: %v", err)
	}
	if at == nil {
		return ""
	}
	return at.UTC().Format("2006-01-02")
}

// A property-only change without a bump is drift naming the keys: no
// version, no audit row, the stored properties untouched, the drift on
// GET and in both list shapes, and no body drift. A second change keeps
// the first detection time and updates the keys. Reverting clears it,
// and a bump then publishes the new properties as the next version with
// no drift on either row.
func TestSync2877_PropertyOnlyChangeIsDrift(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const body = "# Access Control\n\nEveryone uses MFA."
	props := map[string]any{"next_review_at": "2026-03-01"}
	page := syncPage("7001", "Access Control Policy", "2.0", body, props)
	id := postSync(t, s.setupResult, key, http.StatusOK, page).Results[0].PolicyID
	if d := getPolicyVersion(t, s.setupResult, id).SyncPropertyDrift; d != nil {
		t.Fatalf("a fresh synced policy has property drift %+v", d)
	}
	policies := countPolicies(t, s.setupResult, s.tenant)

	edited := syncPage("7001", "Access Control Policy", "2.0", body, map[string]any{
		"next_review_at":                "2026-09-01",
		"acknowledgment_required_roles": []string{"everyone"},
	})
	resp := postSync(t, s.setupResult, key, http.StatusOK, edited)
	if resp.Drift != 1 || resp.Unchanged != 0 || resp.NewVersion != 0 || resp.Created != 0 || resp.Skipped != 0 {
		t.Fatalf("property-only sync = %+v; want one drift", resp)
	}
	res := resp.Results[0]
	wantKeys := []string{"acknowledgment_required_roles", "next_review_at"}
	if res.Status != "drift" || res.PolicyID != id || res.Version != "2.0" ||
		!slices.Equal(res.PropertyDriftKeys, wantKeys) || res.PropertyDriftDetectedAt == nil || res.DriftDetectedAt != nil ||
		!strings.Contains(res.Reason, "acknowledgment_required_roles, next_review_at") {
		t.Fatalf("drift result = %+v; want property drift %v and no body drift", res, wantKeys)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies after property drift = %d, want %d (no version written)", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != 1 {
		t.Errorf("synced audit rows after property drift = %d, want 1", n)
	}
	if got := nextReviewAt(t, s.setupResult, id); got != "2026-03-01" {
		t.Errorf("next_review_at after property drift = %q; want 2026-03-01, nothing applied in place", got)
	}
	got := getPolicyVersion(t, s.setupResult, id)
	if got.Status != "published" || got.Version != "2.0" || got.BodyMd != body || got.SyncDrift != nil {
		t.Errorf("drifted policy = %+v; want published 2.0, body untouched, no body drift", got)
	}
	if d := got.SyncPropertyDrift; d == nil || !slices.Equal(d.Keys, wantKeys) || !d.DetectedAt.Equal(*res.PropertyDriftDetectedAt) {
		t.Fatalf("sync_property_drift = %+v; want keys %v detected at %v", d, wantKeys, res.PropertyDriftDetectedAt)
	}
	for _, path := range []string{"/v1/policies", "/v1/policies?include=ack_rate"} {
		if d := listedPolicy(t, s.setupResult, path, id).SyncPropertyDrift; d == nil || !slices.Equal(d.Keys, wantKeys) {
			t.Errorf("%s sync_property_drift = %+v; want keys %v", path, d, wantKeys)
		}
	}
	firstSeen := got.SyncPropertyDrift.DetectedAt

	// Re-pushing the same edit is still drift and writes nothing new.
	if again := postSync(t, s.setupResult, key, http.StatusOK, edited); again.Drift != 1 {
		t.Fatalf("same edit again = %+v; want drift", again)
	}
	// Half the edit reverted: the keys narrow, first detection time kept.
	narrowed := syncPage("7001", "Access Control Policy", "2.0", body, map[string]any{"next_review_at": "2026-09-01"})
	if r := postSync(t, s.setupResult, key, http.StatusOK, narrowed); r.Drift != 1 || !slices.Equal(r.Results[0].PropertyDriftKeys, []string{"next_review_at"}) {
		t.Fatalf("narrowed edit = %+v; want drift on next_review_at", r)
	}
	if d := getPolicyVersion(t, s.setupResult, id).SyncPropertyDrift; d == nil || !slices.Equal(d.Keys, []string{"next_review_at"}) || !d.DetectedAt.Equal(firstSeen) {
		t.Errorf("sync_property_drift after narrowing = %+v; want [next_review_at] detected at %v", d, firstSeen)
	}

	// Reverting clears the drift.
	reverted := postSync(t, s.setupResult, key, http.StatusOK, page)
	if reverted.Unchanged != 1 || reverted.Drift != 0 || reverted.Results[0].PolicyID != id || reverted.Results[0].PropertyDriftKeys != nil {
		t.Fatalf("reverted sync = %+v; want unchanged", reverted)
	}
	if d := getPolicyVersion(t, s.setupResult, id).SyncPropertyDrift; d != nil {
		t.Errorf("sync_property_drift after revert = %+v; want none", d)
	}

	// Drift again, then a bump: the new version carries the new
	// properties, and neither row keeps the drift.
	if r := postSync(t, s.setupResult, key, http.StatusOK, edited); r.Drift != 1 {
		t.Fatalf("second edit = %+v; want drift", r)
	}
	bumped := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7001", "Access Control Policy", "2.1", body, map[string]any{
		"next_review_at":                "2026-09-01",
		"acknowledgment_required_roles": []string{"everyone"},
	}))
	if bumped.NewVersion != 1 || bumped.Drift != 0 || bumped.Results[0].PredecessorID != id {
		t.Fatalf("bumped sync = %+v; want new_version chained to %s", bumped, id)
	}
	v2 := bumped.Results[0].PolicyID
	if p := getPolicyVersion(t, s.setupResult, v2); p.SyncPropertyDrift != nil || p.Status != "published" {
		t.Errorf("new version = %+v; want published with no property drift", p)
	}
	if got := nextReviewAt(t, s.setupResult, v2); got != "2026-09-01" {
		t.Errorf("new version next_review_at = %q; want 2026-09-01", got)
	}
	if p := getPolicyVersion(t, s.setupResult, id); p.SyncPropertyDrift != nil || p.Status != "superseded" {
		t.Errorf("superseded version = %+v; want superseded with no property drift", p)
	}
	if again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7001", "Access Control Policy", "2.1", body, map[string]any{
		"next_review_at":                "2026-09-01",
		"acknowledgment_required_roles": []string{"everyone"},
	})); again.Unchanged != 1 {
		t.Errorf("re-push of the bumped page = %+v; want unchanged", again)
	}
}

// Body and property drift are recorded separately: a run with both
// reports both, and reverting only the body leaves the property drift.
func TestSync2877_BodyAndPropertyDriftAreSeparate(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const body = "Body."
	id := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7101", "Vendor Policy", "1.0", body, nil)).Results[0].PolicyID

	const edited = "Body, edited."
	moved := map[string]any{"effective_date": "2025-04-01"}
	both := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7101", "Vendor Policy", "1.0", edited, moved))
	res := both.Results[0]
	if both.Drift != 1 || res.DriftDetectedAt == nil || !slices.Equal(res.PropertyDriftKeys, []string{"effective_date"}) ||
		!strings.Contains(res.Reason, "body and property effective_date") {
		t.Fatalf("body and property sync = %+v; want both drifts", both)
	}
	got := getPolicyVersion(t, s.setupResult, id)
	if got.SyncDrift == nil || got.SyncDrift.SourceBodySHA256 != sha(edited) || got.SyncPropertyDrift == nil {
		t.Fatalf("policy = %+v; want body and property drift", got)
	}

	onlyProps := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7101", "Vendor Policy", "1.0", body, moved))
	if onlyProps.Drift != 1 || onlyProps.Results[0].DriftDetectedAt != nil || !slices.Equal(onlyProps.Results[0].PropertyDriftKeys, []string{"effective_date"}) {
		t.Fatalf("body reverted = %+v; want property drift only", onlyProps)
	}
	if got := getPolicyVersion(t, s.setupResult, id); got.SyncDrift != nil || got.SyncPropertyDrift == nil {
		t.Errorf("policy after body revert = %+v; want property drift only", got)
	}
}

// A page that takes over an imported policy in place with properties
// that differ from the import's is taken over and reported as property
// drift; nothing of the imported row's properties is rewritten.
func TestSync2877_TakeOverInPlaceWithDifferentPropertiesIsDrift(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, body := importPolicy(t, s, "Logging Policy", "1.0")
	props := importedPageProps()
	props["next_review_at"] = "2025-08-01"

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7201", "Logging Policy", "1.0", body, props))
	res := resp.Results[0]
	if resp.TakenOver != 1 || res.PolicyID != imported || res.DriftDetectedAt != nil ||
		!slices.Equal(res.PropertyDriftKeys, []string{"next_review_at"}) || !strings.Contains(res.Reason, "property next_review_at") {
		t.Fatalf("takeover = %+v; want taken_over of %s with property drift on next_review_at", resp, imported)
	}
	if got := nextReviewAt(t, s.setupResult, imported); got != "2025-02-01" {
		t.Errorf("next_review_at after takeover = %q; want the import's 2025-02-01", got)
	}
	if d := getPolicyVersion(t, s.setupResult, imported).SyncPropertyDrift; d == nil || !slices.Equal(d.Keys, []string{"next_review_at"}) {
		t.Errorf("sync_property_drift = %+v; want [next_review_at]", d)
	}
	if again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7201", "Logging Policy", "1.0", body, props)); again.Drift != 1 {
		t.Errorf("re-push = %+v; want drift", again)
	}
}

// The property drift columns are rejected on a row that is not synced,
// must come together, and need at least one key
// (20261296000000_policy_sync_property_drift).
func TestSync2877_PropertyDriftOnlyOnSyncedRows(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	synced := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7301", "Synced", "1.0", "Body.", nil)).Results[0].PolicyID
	authored := createDraftPolicy(t, s.setupResult, "Authored-2877", "1.0", nil)
	for name, c := range map[string]struct {
		id, set string
	}{
		"authored row":      {authored, `sync_property_drift_detected_at = now(), sync_property_drift_keys = ARRAY['owner_role']`},
		"time without key":  {synced, `sync_property_drift_detected_at = now()`},
		"keys without time": {synced, `sync_property_drift_keys = ARRAY['owner_role']`},
		"no keys":           {synced, `sync_property_drift_detected_at = now(), sync_property_drift_keys = '{}'`},
	} {
		if _, err := s.admin.Exec(context.Background(),
			`UPDATE policies SET `+c.set+` WHERE tenant_id = $1 AND id = $2`, s.tenant, uuid.MustParse(c.id)); err == nil {
			t.Errorf("%s: property drift columns accepted", name)
		}
	}
}

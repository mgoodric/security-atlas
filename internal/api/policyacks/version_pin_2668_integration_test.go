//go:build integration

// #2668 — the acknowledge POST pins the version the caller was shown, and
// GET /v1/me/acknowledgments?include=current lists the fresh acks so a
// reload can show "You acknowledged version N".

package policyacks_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAcknowledge_VersionPin_Republished is the race: the caller reads v1
// from the pending list, v2 is published before the click, and the click
// names v1. Neither the superseded row nor the new one records an ack the
// caller never saw; acknowledging v2 as v2 still works.
func TestAcknowledge_VersionPin_Republished(t *testing.T) {
	s := setup(t)
	v1 := createAndPublishPolicy(t, s, "Pin Policy", "1", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "pin@test", []string{s.requiredRole}, false)

	// Page load: the pending item names v1.
	var before struct {
		Pending []struct {
			PolicyVersionID string `json:"policy_version_id"`
		} `json:"pending"`
	}
	raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
	if err := json.Unmarshal(raw, &before); err != nil || len(before.Pending) != 1 {
		t.Fatalf("want one pending item, got %s (err %v)", raw, err)
	}
	shown := before.Pending[0].PolicyVersionID
	if shown != v1 {
		t.Fatalf("pending version %s != v1 %s", shown, v1)
	}

	// Re-published between load and click.
	v2 := s.forkAndPublishV2(t, v1, "2")
	pin, _ := json.Marshal(map[string]string{"policy_version_id": shown})

	ackRows := countAckRows(t, s.admin, s.tenant)
	s.postJSON(t, "/v1/policies/"+v2+"/acknowledge", pin, bearer, http.StatusConflict)
	s.postJSON(t, "/v1/policies/"+v1+"/acknowledge", pin, bearer, http.StatusConflict)
	if got := countAckRows(t, s.admin, s.tenant); got != ackRows {
		t.Fatalf("a refused ack wrote %d rows", got-ackRows)
	}

	pinV2, _ := json.Marshal(map[string]string{"policy_version_id": v2})
	s.postJSON(t, "/v1/policies/"+v2+"/acknowledge", pinV2, bearer, http.StatusCreated)
}

func TestAcknowledge_VersionPin_Malformed(t *testing.T) {
	s := setup(t)
	id := createAndPublishPolicy(t, s, "Pin Malformed", "1", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "pinbad@test", []string{s.requiredRole}, false)

	s.postJSON(t, "/v1/policies/"+id+"/acknowledge", []byte(`{"policy_version_id":"nope"}`), bearer, http.StatusBadRequest)
	s.postJSON(t, "/v1/policies/"+id+"/acknowledge", []byte(`[`), bearer, http.StatusBadRequest)
	// No body still acknowledges the policy id, as before #2668.
	s.postJSON(t, "/v1/policies/"+id+"/acknowledge", nil, bearer, http.StatusCreated)
}

func TestMyAcknowledgments_IncludeCurrent(t *testing.T) {
	s := setup(t)
	id := createAndPublishPolicy(t, s, "Current Policy", "3", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "current@test", []string{s.requiredRole}, false)

	type item struct {
		PolicyID        string `json:"policy_id"`
		PolicyVersionID string `json:"policy_version_id"`
		Version         string `json:"version"`
		AcknowledgedAt  string `json:"acknowledged_at"`
	}
	type resp struct {
		Pending []item  `json:"pending"`
		Current *[]item `json:"current"`
	}
	read := func(path string) resp {
		t.Helper()
		raw := s.getJSON(t, path, bearer, http.StatusOK)
		var r resp
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		return r
	}

	if r := read("/v1/me/acknowledgments?include=current"); r.Current == nil || len(*r.Current) != 0 || len(r.Pending) != 1 {
		t.Fatalf("before ack: want 1 pending and an empty current, got %+v", r)
	}

	s.postJSON(t, "/v1/policies/"+id+"/acknowledge", nil, bearer, http.StatusCreated)

	r := read("/v1/me/acknowledgments?include=current")
	if len(r.Pending) != 0 {
		t.Fatalf("after ack: want no pending, got %+v", r.Pending)
	}
	if r.Current == nil || len(*r.Current) != 1 {
		t.Fatalf("after ack: want one current item, got %+v", r.Current)
	}
	got := (*r.Current)[0]
	if got.PolicyID != id || got.PolicyVersionID != id || got.Version != "3" || got.AcknowledgedAt == "" {
		t.Fatalf("current item %+v does not name the acknowledged version", got)
	}

	if r := read("/v1/me/acknowledgments"); r.Current != nil {
		t.Fatalf("without ?include=current the response must not carry current, got %+v", r.Current)
	}
}

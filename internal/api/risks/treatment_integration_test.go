//go:build integration

// #1746: POST /v1/risks never records a treatment decision the caller did
// not make, and rejects the states the domain layer forbids with a 400.
//
// Run with:  go test -tags=integration ./internal/api/risks/...

package risks_test

import (
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestCreateRisk_TreatmentStates(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	base := func(extra map[string]any) map[string]any {
		body := map[string]any{
			"title":          "Intake risk",
			"category":       "operational",
			"inherent_score": map[string]int{"likelihood": 2, "impact": 3},
		}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}

	// Omitted treatment stores untreated, with no acceptance status.
	resp, body := post(t, env, "/v1/risks", base(nil), env.bearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create without treatment: status %d (body %v)", resp.StatusCode, body)
	}
	rk, _ := body["risk"].(map[string]any)
	if rk["treatment"] != "untreated" {
		t.Fatalf("treatment = %v; want untreated", rk["treatment"])
	}
	if _, present := rk["acceptance_status"]; present {
		t.Fatalf("acceptance_status present on an untreated risk: %v", rk["acceptance_status"])
	}

	// A proposed acceptance needs no accepter or expiry.
	resp, body = post(t, env, "/v1/risks",
		base(map[string]any{"treatment": "accept", "acceptance_status": "proposed"}), env.bearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("proposed accept: status %d (body %v)", resp.StatusCode, body)
	}
	rk, _ = body["risk"].(map[string]any)
	if rk["acceptance_status"] != "proposed" {
		t.Fatalf("acceptance_status = %v; want proposed", rk["acceptance_status"])
	}

	for name, extra := range map[string]map[string]any{
		"approved accept without accepter or expiry": {"treatment": "accept", "acceptance_status": "approved"},
		"accept without acceptance status":           {"treatment": "accept"},
		"mitigate with no control or action plan":    {"treatment": "mitigate"},
		"acceptance status on untreated":             {"acceptance_status": "proposed"},
	} {
		resp, body := post(t, env, "/v1/risks", base(extra), env.bearer)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %v)", name, resp.StatusCode, body)
		}
	}
}

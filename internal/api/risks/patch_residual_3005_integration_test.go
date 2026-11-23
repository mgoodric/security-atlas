//go:build integration

// #3005 — PATCH /v1/risks/{id} that changes the inherent score of a risk
// with linked controls recomputes its stored residual, rather than leaving
// it on the old score until the next evidence for one of its controls. The
// response carries the risk as it stands after that write, so its
// updated_at (the ETag) matches a following GET.
//
// Run with:
//
//	go test -tags=integration -count=1 -run TestPatchResidual_3005 ./internal/api/risks/

package risks_test

import (
	"context"
	"net/http"
	"testing"
)

func TestPatchResidual_3005_ScoreChangeRecomputesStoredResidual(t *testing.T) {
	env := newControlLinkEnv(t)
	id := env.newRisk(t) // likelihood 3 x impact 4
	env.link(t, id, seedControl(t, env.admin, env.tenant), http.StatusOK)

	storedInherent := func() float64 {
		t.Helper()
		var v float64
		if err := env.admin.QueryRow(context.Background(),
			`SELECT (residual_score->>'inherent_score')::float8 FROM risks WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatalf("read residual_score: %v", err)
		}
		return v
	}
	if got := storedInherent(); got != 12 {
		t.Fatalf("residual inherent_score after link = %v, want 12", got)
	}

	code, body := env.do(t, http.MethodPatch, "/v1/risks/"+id, env.ownerJWT,
		map[string]any{"inherent_score": map[string]int{"likelihood": 5, "impact": 5}})
	if code != http.StatusOK {
		t.Fatalf("PATCH: status %d: %v", code, body)
	}
	if got := storedInherent(); got != 25 {
		t.Fatalf("residual inherent_score after PATCH = %v, want 25 (recomputed)", got)
	}

	code, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET: status %d: %v", code, got)
	}
	if p, g := riskField(t, body, "updated_at"), riskField(t, got, "updated_at"); p != g {
		t.Fatalf("PATCH updated_at %v != GET updated_at %v: the ETag would be stale", p, g)
	}
}

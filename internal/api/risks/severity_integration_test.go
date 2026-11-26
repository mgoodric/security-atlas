//go:build integration

// #2490 — ?severity_band= counts on the server what the sidebar badge used
// to count on the client, and a non-numeric score is refused on write.
//
// Run with:
//
//	go test -tags=integration -count=1 -run 'TestSeverityBand|TestScoreWrite' ./internal/api/risks/
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package risks_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// TestSeverityBand_MatchesWireSeverity: for every band, the filter returns
// exactly the non-FAIR rows whose wire `severity` SeverityBand puts in that
// band, and `total` counts them. The seeds cover the shapes where the SQL
// could drift from risk.AssessedSeverity.
func TestSeverityBand_MatchesWireSeverity(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)

	seed := func(title, methodology, inherent string) {
		t.Helper()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO risks (id, tenant_id, title, category_id, methodology, treatment, inherent_score, residual_score)
			VALUES ($1, $2, $3, risk_category_id($2, 'operational'), $4::risk_methodology, 'untreated', $5::jsonb, '{}'::jsonb)
		`, uuid.New(), tenant, title, methodology, inherent); err != nil {
			t.Fatalf("seed %s: %v", title, err)
		}
	}
	seed("5x5 at the high edge", "nist_800_30", `{"likelihood":3,"impact":5}`)
	seed("5x5 just under high", "qualitative_5x5", `{"likelihood":2,"impact":7}`)
	seed("5x5 top", "nist_800_30", `{"likelihood":5,"impact":5}`)
	seed("medium edge", "nist_800_30", `{"likelihood":2,"impact":4}`)
	seed("low", "nist_800_30", `{"likelihood":1,"impact":1}`)
	seed("explicit severity wins", "cis_ram", `{"severity":16,"likelihood":1,"impact":1}`)
	seed("fractional factors truncate", "cis_ram", `{"likelihood":3.9,"impact":4.9}`)
	seed("fractional severity truncates", "cis_ram", `{"severity":14.9}`)
	seed("string severity is unscored", "cis_ram", `{"severity":"20","likelihood":5,"impact":5}`)
	seed("over the grid stays high", "iso_27005", `{"severity":40}`)
	seed("fair with a 5x5 shape", "fair", `{"loss_event_frequency":1,"loss_magnitude":10,"likelihood":5,"impact":5}`)
	seed("array score", "iso_27005", `[5,5]`)

	type row struct {
		id, methodology string
		severity        float64
	}
	read := func(query string) ([]row, int) {
		t.Helper()
		resp, body := get(t, env, "/v1/risks?"+query)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/risks?%s: status %d: %v", query, resp.StatusCode, body)
		}
		var out []row
		for _, r := range body["risks"].([]any) {
			m := r.(map[string]any)
			out = append(out, row{m["id"].(string), m["methodology"].(string), m["severity"].(float64)})
		}
		return out, int(body["total"].(float64))
	}
	all, _ := read("sort=id")
	if len(all) != 12 {
		t.Fatalf("seeded 12 risks, listed %d", len(all))
	}
	for band, label := range map[string]string{"high": "High", "medium": "Medium", "low": "Low"} {
		var want []string
		for _, r := range all {
			if r.methodology != "fair" && risk.SeverityBand(r.severity) == label {
				want = append(want, r.id)
			}
		}
		got, total := read("sort=id&severity_band=" + band)
		var ids []string
		for _, r := range got {
			ids = append(ids, r.id)
		}
		if !slices.Equal(ids, want) {
			t.Errorf("severity_band=%s: got %v, want %v", band, ids, want)
		}
		if total != len(want) {
			t.Errorf("severity_band=%s: total %d, want %d", band, total, len(want))
		}
		// The badge reads only the total, off a one-row page.
		if _, oneTotal := read("limit=1&severity_band=" + band); oneTotal != len(want) {
			t.Errorf("severity_band=%s&limit=1: total %d, want %d", band, oneTotal, len(want))
		}
	}
	if _, high := read("severity_band=high"); high != 4 {
		t.Errorf("high total %d, want 4 (high edge, top, explicit severity, over the grid)", high)
	}
	if resp, _ := get(t, env, "/v1/risks?severity_band=critical"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("severity_band=critical: status %d, want 400", resp.StatusCode)
	}
}

// TestScoreWrite_RefusesNonNumericScores: create refuses a non-FAIR
// residual or inherent score field that is not a JSON number in range with
// a 422, and PATCH refuses such an inherent score. FAIR keeps its own
// validation.
func TestScoreWrite_RefusesNonNumericScores(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)

	for name, extra := range map[string]map[string]any{
		"numeric string residual": {"residual_score": map[string]any{"score": "20"}},
		"residual out of range":   {"residual_score": map[string]any{"score": 30}},
		"residual with no score":  {"residual_score": map[string]any{"note": "later"}},
		"string inherent severity": {"methodology": "cis_ram",
			"inherent_score": map[string]any{"severity": "20"}},
	} {
		resp, body := createPlaced(t, env, extra)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("create with %s: status %d, want 422: %v", name, resp.StatusCode, body)
		}
	}
	for name, extra := range map[string]map[string]any{
		"no residual":      nil,
		"empty residual":   {"residual_score": map[string]any{}},
		"numeric residual": {"residual_score": map[string]any{"score": 12.5}},
	} {
		if resp, body := createPlaced(t, env, extra); resp.StatusCode != http.StatusCreated {
			t.Errorf("create with %s: status %d, want 201: %v", name, resp.StatusCode, body)
		}
	}

	id := createRecordRisk(t, env, map[string]any{"methodology": "cis_ram", "inherent_score": map[string]any{"severity": 10}})
	resp, body := patch(t, env, "/v1/risks/"+id, `{"inherent_score":{"severity":"20"}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("patch with string severity: status %d, want 422: %v", resp.StatusCode, body)
	}
	if resp, body := patch(t, env, "/v1/risks/"+id, `{"inherent_score":{"severity":20}}`); resp.StatusCode != http.StatusOK {
		t.Errorf("patch with numeric severity: status %d, want 200: %v", resp.StatusCode, body)
	}
}

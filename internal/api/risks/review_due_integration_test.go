//go:build integration

// #2059 — POST /v1/risks defaults review_due_at from the organisation risk
// review cadence (tenants.risk_review_cadence_days, PATCH /v1/tenants/{id}).
//
//   - a risk created without review_due_at is due created_at + cadence;
//   - a risk created with review_due_at keeps it;
//   - changing the cadence affects risks created afterwards only.

package risks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// cadenceTenant returns a tenant with a tenants row (so it has a cadence),
// cleaning up its risks, categories, audit rows and the row itself.
func cadenceTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := dbtest.SeedTenant(t, admin,
		"risks",
		"org_risk_categories",
		"me_audit_log",
	)
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenant, "cadence-2059-"+tenant[:8]); err != nil {
		t.Fatalf("seed tenants row: %v", err)
	}
	// Registered after SeedTenant's cleanup, so it runs first; the risks
	// and categories carry no FK to tenants.
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant)
	})
	return tenant
}

// adminServer is testServer with an admin bearer, which PATCH /v1/tenants
// requires.
func adminServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// createRiskDue POSTs a risk and returns its id and review_due_at, failing
// the test on any non-201.
func createRiskDue(t *testing.T, env testEnv, reviewDue string) (string, time.Time) {
	t.Helper()
	payload := map[string]any{
		"title":          "review cadence risk",
		"category":       "operational",
		"inherent_score": map[string]int{"likelihood": 3, "impact": 3},
	}
	if reviewDue != "" {
		payload["review_due_at"] = reviewDue
	}
	resp, body := post(t, env, "/v1/risks", payload, env.bearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/risks: status %d, want 201 (%v)", resp.StatusCode, body)
	}
	r, _ := body["risk"].(map[string]any)
	raw, ok := r["review_due_at"].(string)
	if !ok {
		t.Fatalf("created risk has no review_due_at: %v", r)
	}
	due, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parse review_due_at %q: %v", raw, err)
	}
	return r["id"].(string), due
}

// assertCadence checks GET /v1/risks/review-cadence reports want days.
func assertCadence(t *testing.T, env testEnv, want int) {
	t.Helper()
	resp, body := get(t, env, "/v1/risks/review-cadence")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks/review-cadence: status %d (%v)", resp.StatusCode, body)
	}
	if got := body["risk_review_cadence_days"]; got != float64(want) {
		t.Errorf("risk_review_cadence_days = %v, want %d", got, want)
	}
}

func createdAtOf(t *testing.T, admin *pgxpool.Pool, id string) time.Time {
	t.Helper()
	var created time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT created_at FROM risks WHERE id = $1`, id).Scan(&created); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	return created
}

func TestCreateRisk_ReviewDueDefaultsToCreatedPlusCadence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := cadenceTenant(t, admin)
	env := testServer(t, app, tenant)

	id, due := createRiskDue(t, env, "")
	want := createdAtOf(t, admin, id).AddDate(0, 0, 365)
	if !due.Equal(want) {
		t.Errorf("review_due_at = %s, want created_at + 365 days = %s", due.UTC(), want.UTC())
	}
}

func TestCreateRisk_ReviewDueGivenIsKept(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := cadenceTenant(t, admin)
	env := testServer(t, app, tenant)

	given := "2027-02-15T00:00:00Z"
	_, due := createRiskDue(t, env, given)
	if want, _ := time.Parse(time.RFC3339, given); !due.Equal(want) {
		t.Errorf("review_due_at = %s, want the given %s", due.UTC(), given)
	}
}

func TestRiskReviewCadence_ChangeAffectsNewRisksOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := cadenceTenant(t, admin)
	env := adminServer(t, app, tenant)

	assertCadence(t, env, 365)
	before, beforeDue := createRiskDue(t, env, "")

	resp, body := patch(t, env, "/v1/tenants/"+tenant, `{"risk_review_cadence_days": 90}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/tenants: status %d, want 200 (%v)", resp.StatusCode, body)
	}
	if got := body["tenant"].(map[string]any)["risk_review_cadence_days"]; got != float64(90) {
		t.Fatalf("PATCH response risk_review_cadence_days = %v, want 90", got)
	}

	assertCadence(t, env, 90)

	after, afterDue := createRiskDue(t, env, "")
	if want := createdAtOf(t, admin, after).AddDate(0, 0, 90); !afterDue.Equal(want) {
		t.Errorf("new risk review_due_at = %s, want created_at + 90 days = %s", afterDue.UTC(), want.UTC())
	}

	resp, body = get(t, env, "/v1/risks/"+before)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET existing risk: status %d (%v)", resp.StatusCode, body)
	}
	raw, _ := body["risk"].(map[string]any)["review_due_at"].(string)
	kept, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parse existing review_due_at %q: %v", raw, err)
	}
	if !kept.Equal(beforeDue) {
		t.Errorf("existing risk review_due_at moved from %s to %s after the cadence change", beforeDue.UTC(), kept.UTC())
	}
}

func TestRiskReviewCadence_OutOfRangeRejected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := cadenceTenant(t, admin)
	env := adminServer(t, app, tenant)

	for _, raw := range []string{`{"risk_review_cadence_days": 0}`, `{"risk_review_cadence_days": 3651}`} {
		resp, body := patch(t, env, "/v1/tenants/"+tenant, raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PATCH %s: status %d, want 400 (%v)", raw, resp.StatusCode, body)
		}
	}
}

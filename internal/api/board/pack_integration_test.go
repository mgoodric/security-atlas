//go:build integration

// Slice 032 — integration tests for the quarterly board pack HTTP API. Real
// Postgres + the assembled platform router so the tests exercise the full
// request path (tenancy middleware, RLS, the board PackGenerator + PackStore,
// the slice-016 freshness/drift read models, the slice-012 control_evaluations
// the open-findings section reads from). The DB is never mocked.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/board/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
// Reuses the harness in integration_test.go (openPool / testServer / doJSON /
// doRaw / seedFramework / ctxFor) — those live in the same package + build tag.
//
// Coverage (the 8 ACs from docs/issues/032-quarterly-board-pack.md):
//
//	AC-1  POST /v1/board-packs generates a DRAFT pack with all fixed sections
//	AC-2  PUT a section overrides the templated narrative
//	AC-3  PUT the investment section accepts $ spend and recomputes the delta
//	AC-4  the asks-of-the-board section is freeform-editable, no AI
//	AC-5  per-section approve -> publish -> frozen artifact
//	AC-6  GET .../{id}.md and .../{id}/pdf return Markdown / PDF
//	AC-7  a published pack is immutable — PUT / approve return 409
//	AC-8  the open-findings section is populated from failing control_evaluations
//	D6    publish is rejected until EVERY section is approved
//	RLS   cross-tenant isolation — tenant A never sees tenant B's packs

package board_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ----- slice-032 harness extensions -----

// freshPackTenant is freshTenant plus board_packs / control_evaluations
// cleanup so the slice-032 tests leave no residue.
func freshPackTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM board_packs WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("pack cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedFailingEvaluation appends one `fail` control_evaluations row for the
// control as of `evaluatedAt`. The open-findings section (AC-8) reads these.
// A failing evaluation IS a finding for v1.
func seedFailingEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, evaluatedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, last_observed_at, freshness_class, trigger
		)
		VALUES ($1, $2, $3, NULL, $4, $5, 'fail', 'fresh', 1, $5, 'monthly', 'manual')
	`, id, tenant, ctrlID, runID, evaluatedAt); err != nil {
		t.Fatalf("seed failing evaluation: %v", err)
	}
	return id
}

// generateDraftPack POSTs a draft pack for periodEnd and returns its id.
func generateDraftPack(t *testing.T, env testEnv, periodEnd string) string {
	t.Helper()
	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-packs",
		map[string]any{"period_end": periodEnd})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/board-packs = %d, want 201 (body: %v)", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("generated pack has no id: %v", body)
	}
	return id
}

// approveAllSections walks the fixed section keys and approves each one.
// Slice 273 added `vendor_burndown` as the 8th canonical section between
// `open_findings` and `operational_metrics`.
func approveAllSections(t *testing.T, env testEnv, packID string) {
	t.Helper()
	for _, key := range []string{
		"posture", "top_risks", "coverage_trend", "open_findings",
		"vendor_burndown", "operational_metrics", "investment", "asks",
	} {
		resp, body := doJSON(t, env, http.MethodPost,
			"/v1/board-packs/"+packID+"/sections/"+key+"/approve", map[string]any{})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve section %q = %d, want 200 (body: %v)", key, resp.StatusCode, body)
		}
	}
}

// ===== AC-1: POST generates a DRAFT pack with all fixed sections =====

func TestPackGenerate_DraftWithAllSections(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	seedFramework(t, admin, tenant, "soc2", "SOC 2")
	ctrl := seedControl(t, admin, tenant, "monthly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().AddDate(0, 0, -5))
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("freshness refresh: %v", err)
	}

	resp, body := doJSON(t, env, http.MethodPost, "/v1/board-packs",
		map[string]any{"period_end": "2026-03-31"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/board-packs = %d, want 201 (body: %v)", resp.StatusCode, body)
	}
	if body["status"] != "draft" {
		t.Errorf("new pack status = %v, want draft", body["status"])
	}
	content, _ := body["content"].(map[string]any)
	sections, _ := content["sections"].(map[string]any)
	for _, key := range []string{
		"posture", "top_risks", "coverage_trend", "open_findings",
		"vendor_burndown", "operational_metrics", "investment", "asks",
	} {
		if _, ok := sections[key]; !ok {
			t.Errorf("generated pack missing fixed section %q", key)
		}
	}
	// The narrative is templated and non-empty.
	if nm, _ := body["narrative_md"].(string); nm == "" {
		t.Error("generated pack has empty narrative_md")
	}
}

// AC-1 edge cases: malformed period_end -> 400; missing bearer -> 401.
func TestPackGenerate_BadInput(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, _ := doJSON(t, env, http.MethodPost, "/v1/board-packs",
		map[string]any{"period_end": "not-a-date"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed period_end = %d, want 400", resp.StatusCode)
	}
	resp, _ = doJSON(t, env, http.MethodPost, "/v1/board-packs", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing period_end = %d, want 400", resp.StatusCode)
	}

	// Missing bearer -> 401.
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/board-packs", nil)
	bare, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bare request: %v", err)
	}
	_ = bare.Body.Close()
	if bare.StatusCode != http.StatusUnauthorized {
		t.Errorf("no bearer = %d, want 401", bare.StatusCode)
	}
}

// ===== AC-2: PUT a section overrides the templated narrative =====

func TestPackUpdateSection_OverridesTemplatedText(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")

	override := "Posture is strong; SOC 2 readiness is on track for the Q3 audit."
	resp, body := doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/posture",
		map[string]any{"override_text": override})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT posture section = %d, want 200 (body: %v)", resp.StatusCode, body)
	}
	content, _ := body["content"].(map[string]any)
	sections, _ := content["sections"].(map[string]any)
	posture, _ := sections["posture"].(map[string]any)
	if posture["override_text"] != override {
		t.Errorf("posture override_text = %v, want %q", posture["override_text"], override)
	}
	// The whole-pack narrative now contains the override.
	if nm, _ := body["narrative_md"].(string); !contains(nm, override) {
		t.Error("narrative_md does not reflect the section override")
	}
}

// PUT an unknown section key -> 404.
func TestPackUpdateSection_UnknownKey(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")
	resp, _ := doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/bogus_section",
		map[string]any{"override_text": "x"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("PUT unknown section = %d, want 404", resp.StatusCode)
	}
}

// ===== AC-3: PUT the investment section accepts $ spend and recomputes =====

func TestPackUpdateSection_InvestmentSpendRecomputes(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	seedFramework(t, admin, tenant, "soc2", "SOC 2")
	ctrl := seedControl(t, admin, tenant, "monthly")
	seedEvidence(t, admin, tenant, ctrl, time.Now().UTC().AddDate(0, 0, -5))
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("freshness refresh: %v", err)
	}
	packID := generateDraftPack(t, env, "2026-03-31")

	// Set the coverage baseline to 40 so the delta is meaningful, then enter
	// $30000 spend. With one fresh control with evidence, coverage is 100%;
	// delta = 100 - 40 = 60; cost-per-point = 30000/60 = 500.
	resp, _ := doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/coverage_trend",
		map[string]any{"inputs": map[string]any{"baseline_coverage_pct": 40}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT coverage_trend baseline = %d, want 200", resp.StatusCode)
	}
	resp, body := doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/investment",
		map[string]any{"inputs": map[string]any{"spend_usd": 30000}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT investment spend = %d, want 200 (body: %v)", resp.StatusCode, body)
	}
	content, _ := body["content"].(map[string]any)
	sections, _ := content["sections"].(map[string]any)
	inv, _ := sections["investment"].(map[string]any)
	data, _ := inv["data"].(map[string]any)
	if spend, _ := data["spend_usd"].(float64); spend != 30000 {
		t.Errorf("investment spend_usd = %v, want 30000", data["spend_usd"])
	}
	if delta, _ := data["coverage_delta"].(float64); delta != 60 {
		t.Errorf("investment coverage_delta = %v, want 60", data["coverage_delta"])
	}
	if cpp, _ := data["cost_per_coverage_point"].(float64); cpp != 500 {
		t.Errorf("cost_per_coverage_point = %v, want 500", data["cost_per_coverage_point"])
	}
}

// ===== AC-4: the asks section is freeform-editable =====

func TestPackUpdateSection_AsksIsFreeform(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")
	asksText := "We ask the board to approve a $250k budget for two security hires in Q3."
	resp, body := doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/asks",
		map[string]any{"override_text": asksText})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT asks section = %d, want 200", resp.StatusCode)
	}
	if nm, _ := body["narrative_md"].(string); !contains(nm, asksText) {
		t.Error("narrative_md does not contain the operator-authored asks text")
	}
}

// ===== AC-5 + D6: per-section approve -> publish -> frozen artifact =====

func TestPackPublish_GatedOnEverySectionApproved(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")

	// Publish with NOTHING approved -> 409 (D6 gate).
	resp, body := doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/publish",
		map[string]any{"published_by": "sec-lead"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("publish with no sections approved = %d, want 409 (body: %v)", resp.StatusCode, body)
	}

	// Approve all but one -> still 409. Slice 273 added vendor_burndown to
	// the canonical eight-section set; approve all eight EXCEPT asks here.
	for _, key := range []string{
		"posture", "top_risks", "coverage_trend", "open_findings",
		"vendor_burndown", "operational_metrics", "investment",
	} {
		resp, _ := doJSON(t, env, http.MethodPost,
			"/v1/board-packs/"+packID+"/sections/"+key+"/approve", map[string]any{})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve %q = %d, want 200", key, resp.StatusCode)
		}
	}
	resp, _ = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/publish",
		map[string]any{"published_by": "sec-lead"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("publish with the asks section unapproved = %d, want 409", resp.StatusCode)
	}

	// Approve the last section, then publish -> 200, status published.
	resp, _ = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/sections/asks/approve", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve asks = %d, want 200", resp.StatusCode)
	}
	resp, body = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/publish",
		map[string]any{"published_by": "sec-lead"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish with every section approved = %d, want 200 (body: %v)", resp.StatusCode, body)
	}
	if body["status"] != "published" {
		t.Errorf("published pack status = %v, want published", body["status"])
	}
	if body["published_by"] != "sec-lead" {
		t.Errorf("published_by = %v, want sec-lead", body["published_by"])
	}

	// Publish requires a published_by -> 400 without it.
	packID2 := generateDraftPack(t, env, "2026-06-30")
	resp, _ = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID2+"/publish", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("publish without published_by = %d, want 400", resp.StatusCode)
	}
}

// ===== AC-6: GET .md and /pdf =====

func TestPackOutputs_MarkdownAndPDF(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")

	// Markdown.
	resp, raw := doRaw(t, env, "/v1/board-packs/"+packID+".md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .md = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !contains(ct, "text/markdown") {
		t.Errorf("Markdown content-type = %q, want text/markdown", ct)
	}
	if !contains(string(raw), "# Quarterly Board Pack") {
		t.Error("Markdown body missing the pack title")
	}

	// PDF — exactly 200 (%PDF- magic) or 503 graceful degradation, never a
	// 500 / hang (slice 475 AC-1). Shared assertion with the brief PDF test.
	resp, raw = doRaw(t, env, "/v1/board-packs/"+packID+"/pdf")
	assertPDFOrServiceUnavailable(t, resp, raw)
}

// ===== AC-7: a published pack is immutable =====

func TestPackPublished_IsImmutable(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	packID := generateDraftPack(t, env, "2026-03-31")
	approveAllSections(t, env, packID)
	resp, _ := doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/publish",
		map[string]any{"published_by": "sec-lead"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish = %d, want 200", resp.StatusCode)
	}

	// PUT a section on the published pack -> 409.
	resp, _ = doJSON(t, env, http.MethodPut,
		"/v1/board-packs/"+packID+"/sections/asks",
		map[string]any{"override_text": "trying to mutate a frozen pack"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("PUT on published pack = %d, want 409", resp.StatusCode)
	}
	// Approve a section on the published pack -> 409.
	resp, _ = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/sections/posture/approve", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("approve on published pack = %d, want 409", resp.StatusCode)
	}
	// Re-publish a published pack -> 409.
	resp, _ = doJSON(t, env, http.MethodPost,
		"/v1/board-packs/"+packID+"/publish",
		map[string]any{"published_by": "sec-lead"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-publish = %d, want 409", resp.StatusCode)
	}

	// A future quarter regenerates a NEW pack with a NEW id (the published
	// one is untouched).
	packID2 := generateDraftPack(t, env, "2026-06-30")
	if packID2 == packID {
		t.Error("a new quarter must generate a new pack id, not reuse the published one")
	}
}

// ===== AC-8: open-findings section is populated from failing evaluations =====

func TestPackOpenFindings_FromFailingEvaluations(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshPackTenant(t, admin)
	env := testServer(t, app, tenant)

	// Two controls, each with a failing evaluation BEFORE the quarter end,
	// and one failing evaluation AFTER the quarter end (must be excluded —
	// the findings read is bounded by period_end, decision D4).
	c1 := seedControl(t, admin, tenant, "monthly")
	c2 := seedControl(t, admin, tenant, "monthly")
	c3 := seedControl(t, admin, tenant, "monthly")
	beforeQ := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	afterQ := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	seedFailingEvaluation(t, admin, tenant, c1, beforeQ)
	seedFailingEvaluation(t, admin, tenant, c2, beforeQ)
	seedFailingEvaluation(t, admin, tenant, c3, afterQ)

	packID := generateDraftPack(t, env, "2026-03-31")
	resp, body := doJSON(t, env, http.MethodGet, "/v1/board-packs/"+packID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET pack = %d, want 200", resp.StatusCode)
	}
	content, _ := body["content"].(map[string]any)
	sections, _ := content["sections"].(map[string]any)
	findings, _ := sections["open_findings"].(map[string]any)
	data, _ := findings["data"].(map[string]any)
	count, _ := data["findings_count"].(float64)
	if int(count) != 2 {
		t.Errorf("open findings count = %v, want 2 (the after-quarter failure must be excluded)", count)
	}
}

// ===== RLS: cross-tenant isolation =====

func TestPackRLS_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshPackTenant(t, admin)
	tenantB := freshPackTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	packA := generateDraftPack(t, envA, "2026-03-31")

	// Tenant B cannot GET tenant A's pack -> 404 (RLS makes it invisible).
	resp, _ := doJSON(t, envB, http.MethodGet, "/v1/board-packs/"+packA, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant B GET tenant A's pack = %d, want 404", resp.StatusCode)
	}
	// Tenant B cannot PUT a section on tenant A's pack -> 404.
	resp, _ = doJSON(t, envB, http.MethodPut,
		"/v1/board-packs/"+packA+"/sections/asks",
		map[string]any{"override_text": "cross-tenant write attempt"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant B PUT on tenant A's pack = %d, want 404", resp.StatusCode)
	}
	// Tenant B's pack list does not include tenant A's pack.
	resp, body := doJSON(t, envB, http.MethodGet, "/v1/board-packs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant B list = %d, want 200", resp.StatusCode)
	}
	packs, _ := body["packs"].([]any)
	for _, p := range packs {
		pm, _ := p.(map[string]any)
		if pm["id"] == packA {
			t.Error("tenant B's pack list leaked tenant A's pack")
		}
	}
}

// ===== slice 273: vendor_burndown section =====

// seedHighCritVendor inserts one high-criticality vendor with a configurable
// last_review_date + review_cadence so the burndown SQL classifies it as
// on-time or past-due relative to a chosen asOf. `lastReview == nil` seeds
// a "never reviewed" vendor (always overdue).
func seedHighCritVendor(t *testing.T, admin *pgxpool.Pool, tenant, name string, lastReview *time.Time, cadence string) {
	t.Helper()
	var lastReviewVal any
	if lastReview != nil {
		lastReviewVal = *lastReview
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO vendors (
			id, tenant_id, name, criticality, review_cadence, last_review_date
		)
		VALUES ($1, $2, $3, 'high', $4, $5)
	`, uuid.New(), tenant, name, cadence, lastReviewVal); err != nil {
		t.Fatalf("seed high-criticality vendor %q: %v", name, err)
	}
}

// freshPackVendorTenant extends freshPackTenant with a vendors-table cleanup
// — slice 273 seeds vendors, so they need to be torn down too.
func freshPackVendorTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := freshPackTenant(t, admin)
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := admin.Exec(ctx,
			`DELETE FROM vendors WHERE tenant_id = $1`, tenant); err != nil {
			t.Logf("vendor cleanup: %v", err)
		}
	})
	return tenant
}

// Slice 273 AC-3 + AC-4: the generated pack's vendor_burndown section
// carries the three scalars from the slice-122 high-criticality burndown
// surface, and its templated narrative names the same numbers. Three
// scenarios in one test:
//
//   - no high-criticality vendors        -> Total = 0, narrative "no vendors registered"
//   - all on time (zero past-due)        -> OnTime == Total, narrative "All N ... 100% on-time"
//   - partial overdue                    -> mixed counts, narrative names the gap
func TestPackVendorBurndown_PopulatedFromHighCriticalitySurface(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	t.Run("no_vendors_registered", func(t *testing.T) {
		tenant := freshPackVendorTenant(t, admin)
		env := testServer(t, app, tenant)

		packID := generateDraftPack(t, env, "2026-03-31")
		resp, body := doJSON(t, env, http.MethodGet, "/v1/board-packs/"+packID, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET pack = %d, want 200", resp.StatusCode)
		}
		content, _ := body["content"].(map[string]any)
		sections, _ := content["sections"].(map[string]any)
		vb, ok := sections["vendor_burndown"].(map[string]any)
		if !ok {
			t.Fatalf("pack missing vendor_burndown section; sections=%v", sections)
		}
		data, _ := vb["data"].(map[string]any)
		// Total absent or zero (omitempty drops zero from the JSON).
		if v, present := data["vendor_burndown_total"]; present && v.(float64) != 0 {
			t.Errorf("vendor_burndown_total = %v, want 0 (no vendors seeded)", v)
		}
		// Narrative names the empty state.
		nm, _ := body["narrative_md"].(string)
		if !contains(nm, "No high-criticality vendors") {
			t.Errorf("narrative does not name the empty vendor state; got:\n%s", nm)
		}
	})

	t.Run("all_on_time_zero_past_due", func(t *testing.T) {
		tenant := freshPackVendorTenant(t, admin)
		env := testServer(t, app, tenant)
		// Three high-criticality vendors reviewed 10 days ago, annual cadence
		// -> all within window relative to 2026-03-31 -> 100% on-time.
		periodEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
		recent := periodEnd.AddDate(0, 0, -10)
		seedHighCritVendor(t, admin, tenant, "Acme", &recent, "annual")
		seedHighCritVendor(t, admin, tenant, "Globex", &recent, "annual")
		seedHighCritVendor(t, admin, tenant, "Initech", &recent, "annual")

		packID := generateDraftPack(t, env, "2026-03-31")
		resp, body := doJSON(t, env, http.MethodGet, "/v1/board-packs/"+packID, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET pack = %d, want 200", resp.StatusCode)
		}
		content, _ := body["content"].(map[string]any)
		sections, _ := content["sections"].(map[string]any)
		vb, _ := sections["vendor_burndown"].(map[string]any)
		data, _ := vb["data"].(map[string]any)
		if total, _ := data["vendor_burndown_total"].(float64); total != 3 {
			t.Errorf("vendor_burndown_total = %v, want 3", data["vendor_burndown_total"])
		}
		if onTime, _ := data["vendor_burndown_on_time"].(float64); onTime != 3 {
			t.Errorf("vendor_burndown_on_time = %v, want 3", data["vendor_burndown_on_time"])
		}
		// past_due omitempty -> absent when zero, which is correct.
		if pastDue, present := data["vendor_burndown_past_due"]; present && pastDue.(float64) != 0 {
			t.Errorf("vendor_burndown_past_due = %v, want 0 (or absent)", pastDue)
		}
		if pct, _ := data["vendor_burndown_on_time_pct"].(float64); pct != 100 {
			t.Errorf("vendor_burndown_on_time_pct = %v, want 100", data["vendor_burndown_on_time_pct"])
		}
		nm, _ := body["narrative_md"].(string)
		if !contains(nm, "All 3 high-criticality vendors") || !contains(nm, "100% on-time") {
			t.Errorf("narrative missing 'all-on-time' shape; got:\n%s", nm)
		}
	})

	t.Run("partial_overdue", func(t *testing.T) {
		tenant := freshPackVendorTenant(t, admin)
		env := testServer(t, app, tenant)
		// Mix: 2 on-time (recent annual), 2 past-due (reviewed >400 days ago
		// against annual cadence, OR never reviewed).
		periodEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
		recent := periodEnd.AddDate(0, 0, -10)
		ancient := periodEnd.AddDate(-2, 0, 0) // ~730 days ago
		seedHighCritVendor(t, admin, tenant, "Acme", &recent, "annual")
		seedHighCritVendor(t, admin, tenant, "Globex", &recent, "annual")
		seedHighCritVendor(t, admin, tenant, "OldVendor", &ancient, "annual")
		seedHighCritVendor(t, admin, tenant, "NeverReviewed", nil, "annual")

		packID := generateDraftPack(t, env, "2026-03-31")
		resp, body := doJSON(t, env, http.MethodGet, "/v1/board-packs/"+packID, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET pack = %d, want 200", resp.StatusCode)
		}
		content, _ := body["content"].(map[string]any)
		sections, _ := content["sections"].(map[string]any)
		vb, _ := sections["vendor_burndown"].(map[string]any)
		data, _ := vb["data"].(map[string]any)
		if total, _ := data["vendor_burndown_total"].(float64); total != 4 {
			t.Errorf("vendor_burndown_total = %v, want 4", data["vendor_burndown_total"])
		}
		if onTime, _ := data["vendor_burndown_on_time"].(float64); onTime != 2 {
			t.Errorf("vendor_burndown_on_time = %v, want 2 (Acme + Globex)", data["vendor_burndown_on_time"])
		}
		if pastDue, _ := data["vendor_burndown_past_due"].(float64); pastDue != 2 {
			t.Errorf("vendor_burndown_past_due = %v, want 2 (OldVendor + NeverReviewed)", data["vendor_burndown_past_due"])
		}
		if pct, _ := data["vendor_burndown_on_time_pct"].(float64); pct != 50 {
			t.Errorf("vendor_burndown_on_time_pct = %v, want 50", data["vendor_burndown_on_time_pct"])
		}
		nm, _ := body["narrative_md"].(string)
		if !contains(nm, "2 of 4 high-criticality vendors") || !contains(nm, "2 vendors are past due") {
			t.Errorf("narrative does not name the partial-overdue shape; got:\n%s", nm)
		}
	})
}

// Slice 273 RLS: cross-tenant isolation — tenant A's high-criticality vendors
// never leak into tenant B's board-pack vendor_burndown section. The
// vendor.Store.Burndown surface is tenant-RLS-scoped (slice 122); the new
// adapter inherits that scoping through the tenant GUC on ctx.
func TestPackVendorBurndown_RLSCrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshPackVendorTenant(t, admin)
	tenantB := freshPackVendorTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	// Tenant A: 5 high-criticality vendors, all on-time.
	periodEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	recent := periodEnd.AddDate(0, 0, -10)
	for i := 0; i < 5; i++ {
		seedHighCritVendor(t, admin, tenantA, "TenantA-Vendor-"+string(rune('A'+i)), &recent, "annual")
	}
	// Tenant B: zero high-criticality vendors.

	packA := generateDraftPack(t, envA, "2026-03-31")
	packB := generateDraftPack(t, envB, "2026-03-31")

	// Tenant A's pack: total = 5.
	_, bodyA := doJSON(t, envA, http.MethodGet, "/v1/board-packs/"+packA, nil)
	contentA, _ := bodyA["content"].(map[string]any)
	sectionsA, _ := contentA["sections"].(map[string]any)
	vbA, _ := sectionsA["vendor_burndown"].(map[string]any)
	dataA, _ := vbA["data"].(map[string]any)
	if total, _ := dataA["vendor_burndown_total"].(float64); total != 5 {
		t.Errorf("tenant A vendor_burndown_total = %v, want 5", dataA["vendor_burndown_total"])
	}

	// Tenant B's pack: total = 0 (RLS made tenant A's rows invisible). The
	// omitempty JSON tag drops zero scalars; presence + zero or absence
	// both satisfy "no leak".
	_, bodyB := doJSON(t, envB, http.MethodGet, "/v1/board-packs/"+packB, nil)
	contentB, _ := bodyB["content"].(map[string]any)
	sectionsB, _ := contentB["sections"].(map[string]any)
	vbB, _ := sectionsB["vendor_burndown"].(map[string]any)
	dataB, _ := vbB["data"].(map[string]any)
	if total, present := dataB["vendor_burndown_total"]; present && total.(float64) != 0 {
		t.Errorf("tenant B vendor_burndown_total = %v, want 0 — RLS leak from tenant A", total)
	}
	// Narrative reflects the empty state, not tenant A's numbers.
	nmB, _ := bodyB["narrative_md"].(string)
	if !contains(nmB, "No high-criticality vendors") {
		t.Errorf("tenant B narrative does not name the empty state — possible RLS leak; got:\n%s", nmB)
	}
	// Tenant A's vendor names must never appear in tenant B's pack output.
	if contains(nmB, "TenantA-Vendor") {
		t.Error("tenant B narrative contains tenant A's vendor name — RLS leak")
	}
}

// contains is a tiny substring helper for the response-body assertions.
func contains(haystack, needle string) bool {
	return len(needle) == 0 ||
		(len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

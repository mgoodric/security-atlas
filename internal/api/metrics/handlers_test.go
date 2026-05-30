// Unit tests for internal/api/metrics (slice 076's HTTP surface).
//
// Load-bearing functions covered here:
//
//   - New, tenantContext — constructor + the credential/tenancy gate every
//     RLS-bound route routes through
//   - ListObservations, CreateInput, GetTarget, UpsertTarget — auth + admin
//     + URL-param + JSON-body + direction + UUID-parse branches, every
//     pre-DB rejection path (401, 403, 400)
//   - numericPtr, numericString, numericStringMaybe, uuidString — pure
//     helpers: valid/invalid, NaN-Numeric, zero-Numeric, valid/invalid
//     UUID, nil-pointer → empty pgtype.Numeric
//   - metricWireFromRow, observationWireFromRow, inputWireFromRow,
//     targetWireFromRow — pure wire mappers: present/absent
//     compute_evaluator, empty/non-empty Dimensions, present/absent
//     OwnerUserID, present/absent threshold pointers
//   - writeJSON, writeError — JSON serialization + status code +
//     Content-Type
//
// The DB-touching branches (ListCatalog, GetCatalog, GetCascade, the
// post-auth path of ListObservations / CreateInput / GetTarget /
// UpsertTarget) need a real Postgres + RLS context and are out of scope
// for this unit suite — they are exercised through the connector +
// frontend integration paths and can be enrolled in the CI integration
// list in a follow-on slice if the merged coverage warrants it.

package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---- fixtures ----

const (
	testTenantID = "00000000-0000-0000-0000-000000000001"
	testUserID   = "11111111-2222-3333-4444-555555555555"
	testMetricID = "ms.security.posture.score"
)

func adminCred() credstore.Credential {
	return credstore.Credential{
		ID:       "key_admin",
		TenantID: testTenantID,
		UserID:   testUserID,
		IsAdmin:  true,
	}
}

func nonAdminCred() credstore.Credential {
	return credstore.Credential{
		ID:       "user-002",
		TenantID: testTenantID,
		UserID:   testUserID,
		IsAdmin:  false,
	}
}

// withAuthAndTenant mirrors what tenancy.Middleware does in production:
// attaches the credential AND seeds the tenant GUC on the context. Unit
// tests that drive a handler directly need this manual wiring so the
// tenantContext check passes through to the next branch.
func withAuthAndTenant(ctx context.Context, cred credstore.Credential) context.Context {
	ctx = authctx.WithCredential(ctx, cred)
	out, err := tenancy.WithTenant(ctx, cred.TenantID)
	if err != nil {
		panic("test fixture: WithTenant: " + err.Error())
	}
	return out
}

// withURLParam wires up a chi URLParam so chi.URLParam(r, "id") works
// when the handler is driven without going through the chi router.
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ---- New + tenantContext ----

func TestNew_ReturnsHandler(t *testing.T) {
	t.Parallel()
	h := New(nil)
	if h == nil {
		t.Fatal("New returned nil")
	}
}

func TestTenantContext_NoCredential(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations", nil)
	_, _, ok := h.tenantContext(r)
	if ok {
		t.Fatal("expected ok=false when credential missing")
	}
}

func TestTenantContext_EmptyTenantID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations", nil)
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{ID: "k1", TenantID: ""})
	r = r.WithContext(ctx)
	_, _, ok := h.tenantContext(r)
	if ok {
		t.Fatal("expected ok=false when tenant id missing")
	}
}

func TestTenantContext_MissingTenantGUC(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations", nil)
	// Credential present but tenant GUC NOT applied — should fail.
	ctx := authctx.WithCredential(r.Context(), adminCred())
	r = r.WithContext(ctx)
	_, _, ok := h.tenantContext(r)
	if ok {
		t.Fatal("expected ok=false when tenancy.WithTenant has not been applied")
	}
}

func TestTenantContext_HappyPath(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations", nil)
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	_, cred, ok := h.tenantContext(r)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cred.TenantID != testTenantID {
		t.Fatalf("expected tenant %q; got %q", testTenantID, cred.TenantID)
	}
}

// ---- ListObservations (pre-DB branches) ----

func TestListObservations_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations", nil)
	h.ListObservations(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestListObservations_MissingID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics//observations", nil)
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	// No chi URLParam => chi.URLParam returns "".
	h.ListObservations(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "id required") {
		t.Fatalf("expected 'id required' in error; got %s", rr.Body.String())
	}
}

func TestListObservations_BadSince(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations?since=not-a-date", nil)
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.ListObservations(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "since must be RFC3339") {
		t.Fatalf("expected since-RFC3339 error; got %s", rr.Body.String())
	}
}

func TestListObservations_BadUntil(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/observations?until=garbage", nil)
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.ListObservations(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "until must be RFC3339") {
		t.Fatalf("expected until-RFC3339 error; got %s", rr.Body.String())
	}
}

// ---- CreateInput (pre-DB branches) ----

func TestCreateInput_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/metrics/x/inputs", strings.NewReader(`{}`))
	h.CreateInput(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCreateInput_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/metrics/x/inputs", strings.NewReader(`{}`))
	r = r.WithContext(withAuthAndTenant(r.Context(), nonAdminCred()))
	h.CreateInput(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "admin role required") {
		t.Fatalf("expected admin-required error; got %s", rr.Body.String())
	}
}

func TestCreateInput_MissingID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/metrics//inputs", strings.NewReader(`{}`))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	h.CreateInput(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCreateInput_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/metrics/x/inputs", strings.NewReader(`{not json}`))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.CreateInput(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Fatalf("expected invalid-JSON error; got %s", rr.Body.String())
	}
}

// ---- GetTarget (pre-DB branches) ----

func TestGetTarget_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics/x/target", nil)
	h.GetTarget(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestGetTarget_MissingID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/metrics//target", nil)
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	h.GetTarget(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

// ---- UpsertTarget (pre-DB branches) ----

func TestUpsertTarget_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(`{}`))
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestUpsertTarget_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(`{}`))
	r = r.WithContext(withAuthAndTenant(r.Context(), nonAdminCred()))
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestUpsertTarget_MissingID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics//target", strings.NewReader(`{}`))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestUpsertTarget_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := New(nil)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(`{not-json`))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid JSON body") {
		t.Fatalf("expected invalid-JSON error; got %s", rr.Body.String())
	}
}

func TestUpsertTarget_BadDirection(t *testing.T) {
	t.Parallel()
	h := New(nil)
	body := `{"direction":"sideways"}`
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(body))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "direction must be") {
		t.Fatalf("expected direction error; got %s", rr.Body.String())
	}
}

func TestUpsertTarget_BadOwnerUUID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	body := `{"direction":"higher_is_better","owner_user_id":"not-a-uuid"}`
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(body))
	r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
	r = withURLParam(r, "id", testMetricID)
	h.UpsertTarget(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "owner_user_id must be a UUID") {
		t.Fatalf("expected owner-UUID error; got %s", rr.Body.String())
	}
}

// All three valid directions are accepted (pre-DB) — this checks the
// switch-statement covers each `case`. The handler then falls through
// to the (nil-pool) DB call which would panic, so we don't drive past
// the validation step. Instead we send the request with a bogus owner
// to force a hard fail BEFORE the DB call. That keeps the switch
// exercised without hitting the pool.
func TestUpsertTarget_AcceptedDirections(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"higher_is_better", "lower_is_better", "target_is_better"} {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			body := `{"direction":"` + dir + `","owner_user_id":"not-a-uuid"}`
			rr := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/v1/metrics/x/target", strings.NewReader(body))
			r = r.WithContext(withAuthAndTenant(r.Context(), adminCred()))
			r = withURLParam(r, "id", testMetricID)
			New(nil).UpsertTarget(rr, r)
			// Direction parsed cleanly => falls through to owner-UUID
			// check, which fails with 400 (not 400 direction).
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("dir=%s expected 400; got %d", dir, rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "owner_user_id") {
				t.Fatalf("dir=%s expected owner-UUID error (means direction was accepted); got %s", dir, rr.Body.String())
			}
		})
	}
}

// ---- pure helpers: numericPtr / numericString / numericStringMaybe / uuidString ----

func TestNumericPtr_Nil(t *testing.T) {
	t.Parallel()
	n := numericPtr(nil)
	if n.Valid {
		t.Fatal("expected nil pointer → invalid pgtype.Numeric")
	}
}

func TestNumericPtr_NonNil(t *testing.T) {
	t.Parallel()
	v := 3.14
	n := numericPtr(&v)
	if !n.Valid {
		t.Fatal("expected non-nil pointer → valid pgtype.Numeric")
	}
}

func TestNumericString_Invalid(t *testing.T) {
	t.Parallel()
	var n pgtype.Numeric // zero-value → !Valid
	if got := numericString(n); got != "0" {
		t.Fatalf("expected '0' for invalid Numeric; got %q", got)
	}
}

func TestNumericString_Valid(t *testing.T) {
	t.Parallel()
	var n pgtype.Numeric
	if err := n.Scan("12.5"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	got := numericString(n)
	if got == "0" || got == "" {
		t.Fatalf("expected non-zero string; got %q", got)
	}
}

func TestNumericStringMaybe_Invalid(t *testing.T) {
	t.Parallel()
	var n pgtype.Numeric
	if got := numericStringMaybe(n); got != "" {
		t.Fatalf("expected empty string for invalid Numeric; got %q", got)
	}
}

func TestNumericStringMaybe_Valid(t *testing.T) {
	t.Parallel()
	var n pgtype.Numeric
	if err := n.Scan("0.5"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := numericStringMaybe(n); got == "" {
		t.Fatal("expected non-empty string for valid Numeric")
	}
}

func TestUUIDString_Invalid(t *testing.T) {
	t.Parallel()
	var u pgtype.UUID // zero-value → !Valid
	if got := uuidString(u); got != "" {
		t.Fatalf("expected empty string for invalid UUID; got %q", got)
	}
}

func TestUUIDString_Valid(t *testing.T) {
	t.Parallel()
	parsed := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	u := pgtype.UUID{Bytes: parsed, Valid: true}
	got := uuidString(u)
	if got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("expected canonical UUID; got %q", got)
	}
}

// ---- pure wire mappers ----

func TestMetricWireFromRow_NoEvaluator(t *testing.T) {
	t.Parallel()
	row := dbx.MetricsCatalog{
		ID:              "ms.x",
		Level:           "board",
		Category:        "security",
		Name:            "X",
		Description:     "desc",
		Unit:            "%",
		Cadence:         "weekly",
		ComputeStrategy: "manual_input",
		SourceSlices:    []string{"076"},
		Notes:           "n",
	}
	wire := metricWireFromRow(row)
	if wire.ID != "ms.x" || wire.Level != "board" || wire.Category != "security" {
		t.Fatalf("bad wire mapping: %+v", wire)
	}
	if wire.ComputeEvaluator != "" {
		t.Fatalf("expected ComputeEvaluator empty when row pointer is nil; got %q", wire.ComputeEvaluator)
	}
	if len(wire.SourceSlices) != 1 || wire.SourceSlices[0] != "076" {
		t.Fatalf("expected slice copy; got %+v", wire.SourceSlices)
	}
}

func TestMetricWireFromRow_WithEvaluator(t *testing.T) {
	t.Parallel()
	eval := "ratio:closed_findings_30d/findings_30d"
	row := dbx.MetricsCatalog{
		ID:               "ms.y",
		Level:            "team",
		Category:         "vuln",
		Name:             "Y",
		ComputeStrategy:  "external_integration",
		ComputeEvaluator: &eval,
		SourceSlices:     []string{},
	}
	wire := metricWireFromRow(row)
	if wire.ComputeEvaluator != eval {
		t.Fatalf("expected evaluator passed through; got %q", wire.ComputeEvaluator)
	}
}

func TestObservationWireFromRow_EmptyDimensions(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	row := dbx.MetricObservation{
		ID:         pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true},
		MetricID:   "ms.x",
		ObservedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Dimensions: nil, // ← empty path
		Source:     "manual",
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	}
	wire := observationWireFromRow(row)
	if string(wire.Dimensions) != "{}" {
		t.Fatalf("expected '{}' dimensions; got %s", string(wire.Dimensions))
	}
	if wire.Source != "manual" {
		t.Fatalf("expected source passed through; got %q", wire.Source)
	}
}

func TestObservationWireFromRow_PresentDimensions(t *testing.T) {
	t.Parallel()
	row := dbx.MetricObservation{
		ID:         pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true},
		MetricID:   "ms.x",
		Dimensions: []byte(`{"env":"prod"}`),
		Source:     "evaluator",
	}
	wire := observationWireFromRow(row)
	if !bytes.Contains(wire.Dimensions, []byte("prod")) {
		t.Fatalf("expected dimensions preserved; got %s", string(wire.Dimensions))
	}
}

func TestInputWireFromRow_EmptyDimensions(t *testing.T) {
	t.Parallel()
	row := dbx.MetricInput{
		ID:              pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true},
		MetricID:        "ms.x",
		EnteredByUserID: pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true},
		Dimensions:      nil,
	}
	wire := inputWireFromRow(row)
	if string(wire.Dimensions) != "{}" {
		t.Fatalf("expected '{}' dimensions; got %s", string(wire.Dimensions))
	}
	if wire.EnteredByUserID == "" {
		t.Fatal("expected EnteredByUserID rendered")
	}
}

func TestInputWireFromRow_PresentDimensions(t *testing.T) {
	t.Parallel()
	row := dbx.MetricInput{
		ID:         pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true},
		MetricID:   "ms.x",
		Dimensions: []byte(`{"region":"us-east-1"}`),
	}
	wire := inputWireFromRow(row)
	if !bytes.Contains(wire.Dimensions, []byte("us-east-1")) {
		t.Fatalf("expected dimensions preserved; got %s", string(wire.Dimensions))
	}
}

func TestTargetWireFromRow_AllFieldsAbsent(t *testing.T) {
	t.Parallel()
	row := dbx.MetricTarget{
		MetricID:  "ms.x",
		Direction: "higher_is_better",
		Notes:     "",
	}
	wire := targetWireFromRow(row)
	if wire.TargetValue != nil || wire.WarningThreshold != nil || wire.CriticalThreshold != nil {
		t.Fatal("expected nil threshold pointers when all Numerics invalid")
	}
	if wire.OwnerUserID != "" {
		t.Fatalf("expected empty owner; got %q", wire.OwnerUserID)
	}
	if wire.Direction != "higher_is_better" {
		t.Fatalf("expected direction passed through; got %q", wire.Direction)
	}
}

func TestTargetWireFromRow_AllFieldsPresent(t *testing.T) {
	t.Parallel()
	var target, warn, crit pgtype.Numeric
	if err := target.Scan("0.95"); err != nil {
		t.Fatalf("scan target: %v", err)
	}
	if err := warn.Scan("0.85"); err != nil {
		t.Fatalf("scan warn: %v", err)
	}
	if err := crit.Scan("0.70"); err != nil {
		t.Fatalf("scan crit: %v", err)
	}
	row := dbx.MetricTarget{
		MetricID:          "ms.x",
		TargetValue:       target,
		WarningThreshold:  warn,
		CriticalThreshold: crit,
		Direction:         "higher_is_better",
		OwnerUserID:       pgtype.UUID{Bytes: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Valid: true},
		Notes:             "review quarterly",
	}
	wire := targetWireFromRow(row)
	if wire.TargetValue == nil || wire.WarningThreshold == nil || wire.CriticalThreshold == nil {
		t.Fatalf("expected non-nil threshold pointers; got %+v", wire)
	}
	if wire.OwnerUserID == "" {
		t.Fatal("expected OwnerUserID rendered when row.OwnerUserID.Valid")
	}
	if wire.Notes != "review quarterly" {
		t.Fatalf("expected notes passed through; got %q", wire.Notes)
	}
}

// ---- httpresp.WriteJSON + httpresp.WriteError ----
//
// Slice 369 — AC-5 contract lock. These two tests previously asserted the
// metrics package's local writeJSON/writeError helpers; after the slice 369
// consolidation they assert the shared internal/api/httpresp helpers
// directly, pinning the 2xx/4xx wire shape (status code, Content-Type, and
// the {"error": msg} envelope) that every migrated handler now emits.

func TestWriteJSON_SetsStatusAndContentType(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	httpresp.WriteJSON(rr, http.StatusAccepted, map[string]string{"hello": "world"})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202; got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type; got %q", got)
	}
	var decoded map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["hello"] != "world" {
		t.Fatalf("expected body decoded; got %+v", decoded)
	}
}

func TestWriteError_RendersErrorEnvelope(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	httpresp.WriteError(rr, http.StatusTeapot, "bad coffee")
	if rr.Code != http.StatusTeapot {
		t.Fatalf("expected 418; got %d", rr.Code)
	}
	var decoded map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["error"] != "bad coffee" {
		t.Fatalf("expected error envelope; got %+v", decoded)
	}
}

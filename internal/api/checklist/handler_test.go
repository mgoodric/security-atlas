// Pure-Go unit tests for the checklist HTTP handler (no Postgres, no build
// tag). They drive the wire-shape transformation, the authz guards, the
// approval-gated markdown export, and the role-section rendering through fake
// service + loader seams. The full request path (real RLS, real model stub,
// cross-tenant) lives in the integration tier.
package checklist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- fakes -----

type fakeGenerator struct {
	cl       checklist.Checklist
	genErr   error
	approved checklist.ApprovedSection
	appErr   error
}

func (f fakeGenerator) Generate(_ context.Context) (checklist.Checklist, error) {
	return f.cl, f.genErr
}
func (f fakeGenerator) ApproveSection(_ context.Context, _ uuid.UUID, approver string) (checklist.ApprovedSection, error) {
	if f.appErr != nil {
		return checklist.ApprovedSection{}, f.appErr
	}
	out := f.approved
	out.HumanApprover = approver
	return out, nil
}

type fakeLoader struct {
	sections []checklist.Section
	err      error
}

func (f fakeLoader) LoadGeneration(_ context.Context, _ uuid.UUID) ([]checklist.Section, error) {
	return f.sections, f.err
}

// routerFor wires the routes behind a grc_engineer credential + a tenant.
func routerFor(svc generator, store loader, tenant string) http.Handler {
	h := newHandlerWith(svc, store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "key_grc_engineer",
				TenantID:   tenant,
				IsApprover: true,
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.RegisterRoutes(r)
	return r
}

// routerNoAuthz wires the routes behind a credential with NO control-write role.
func routerNoAuthz(svc generator, store loader, tenant string) http.Handler {
	h := newHandlerWith(svc, store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "viewer", TenantID: tenant,
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.RegisterRoutes(r)
	return r
}

func do(t *testing.T, h http.Handler, method, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func sampleChecklist() checklist.Checklist {
	cid := uuid.New()
	return checklist.Checklist{
		GenerationID: uuid.New().String(),
		Sections: []checklist.Section{{
			SectionID:  uuid.New().String(),
			Role:       checklist.RoleInfra,
			AIAssisted: true,
			Items: []checklist.Item{{
				ControlID:  cid.String(),
				Task:       "Enable MFA (" + cid.String() + ").",
				Citations:  []checklist.Citation{{Kind: checklist.KindControl, ID: cid.String(), Ref: cid.String()}},
				NoEvidence: false,
			}},
			ModelName: "stub", ModelVersion: "0", ModelProvider: "stub",
		}},
	}
}

// ----- generate -----

func TestGenerate_RendersNonBindingDraft(t *testing.T) {
	tenant := uuid.NewString()
	h := routerFor(fakeGenerator{cl: sampleChecklist()}, fakeLoader{}, tenant)
	code, body := do(t, h, http.MethodPost, "/v1/controls/checklist:generate")
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", code, body)
	}
	var out checklistWire
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Binding {
		t.Error("checklist must be non-binding (binding=false)")
	}
	if out.Disclosure == "" {
		t.Error("checklist must carry a non-binding disclosure (AC-12)")
	}
	if len(out.Sections) != 1 || out.Sections[0].Role != "infra" || !out.Sections[0].AIAssisted {
		t.Fatalf("unexpected sections: %+v", out.Sections)
	}
}

func TestGenerate_RoleGated(t *testing.T) {
	tenant := uuid.NewString()
	h := routerNoAuthz(fakeGenerator{cl: sampleChecklist()}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist:generate")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
}

func TestGenerate_NoControls422(t *testing.T) {
	tenant := uuid.NewString()
	h := routerFor(fakeGenerator{genErr: checklist.ErrNoControls}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist:generate")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
}

func TestGenerate_TooMany422(t *testing.T) {
	tenant := uuid.NewString()
	h := routerFor(fakeGenerator{genErr: checklist.ErrTooManyControls}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist:generate")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
}

// ----- approve -----

func TestApproveSection_RecordsServerDerivedApprover(t *testing.T) {
	tenant := uuid.NewString()
	secID := uuid.New()
	h := routerFor(fakeGenerator{approved: checklist.ApprovedSection{SectionID: secID.String(), Role: checklist.RoleInfra, HumanApproved: true}}, fakeLoader{}, tenant)
	code, body := do(t, h, http.MethodPost, "/v1/controls/checklist/sections/"+secID.String()+":approve")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, body)
	}
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out["human_approver"] != "key_grc_engineer" {
		t.Fatalf("approver must be server-derived credential, got %v", out["human_approver"])
	}
	if out["human_approved"] != true {
		t.Fatal("section must be approved")
	}
}

func TestApproveSection_RoleGated(t *testing.T) {
	tenant := uuid.NewString()
	secID := uuid.New()
	h := routerNoAuthz(fakeGenerator{}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist/sections/"+secID.String()+":approve")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
}

func TestApproveSection_NotFound404(t *testing.T) {
	tenant := uuid.NewString()
	secID := uuid.New()
	h := routerFor(fakeGenerator{appErr: checklist.ErrSectionNotFound}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist/sections/"+secID.String()+":approve")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestApproveSection_BadUUID400(t *testing.T) {
	tenant := uuid.NewString()
	h := routerFor(fakeGenerator{}, fakeLoader{}, tenant)
	code, _ := do(t, h, http.MethodPost, "/v1/controls/checklist/sections/not-a-uuid:approve")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// ----- markdown export gated on approval -----

func TestExportMarkdown_RefusesUnapprovedDraft(t *testing.T) {
	tenant := uuid.NewString()
	genID := uuid.New()
	// One AI section, NOT approved.
	sections := []checklist.Section{{
		SectionID: uuid.New().String(), Role: checklist.RoleInfra, AIAssisted: true, HumanApproved: false,
		Items: []checklist.Item{{ControlID: uuid.New().String(), Task: "do x"}},
	}}
	h := routerFor(fakeGenerator{}, fakeLoader{sections: sections}, tenant)
	code, _ := do(t, h, http.MethodGet, "/v1/controls/checklist/"+genID.String()+"/export.md")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (a draft cannot be exported, P0-471-1)", code)
	}
}

func TestExportMarkdown_ApprovedOnly(t *testing.T) {
	tenant := uuid.NewString()
	genID := uuid.New()
	approvedTask := "Approved task line"
	unapprovedTask := "Unapproved leak line"
	sections := []checklist.Section{
		{SectionID: uuid.New().String(), Role: checklist.RoleInfra, AIAssisted: true, HumanApproved: true, HumanApprover: "key_grc",
			Items: []checklist.Item{{ControlID: uuid.New().String(), Task: approvedTask}}},
		{SectionID: uuid.New().String(), Role: checklist.RoleSecurity, AIAssisted: true, HumanApproved: false,
			Items: []checklist.Item{{ControlID: uuid.New().String(), Task: unapprovedTask}}},
	}
	h := routerFor(fakeGenerator{}, fakeLoader{sections: sections}, tenant)
	code, body := do(t, h, http.MethodGet, "/v1/controls/checklist/"+genID.String()+"/export.md")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	md := string(body)
	if !strings.Contains(md, approvedTask) {
		t.Error("approved section must be exported")
	}
	if strings.Contains(md, unapprovedTask) {
		t.Fatal("LEAK: an unapproved section was exported (P0-471-1)")
	}
}

func TestRenderMarkdown_NoEvidenceMarker(t *testing.T) {
	t.Parallel()
	sections := []checklist.Section{{
		Role: checklist.RoleInfra, AIAssisted: true, HumanApproved: true,
		Items: []checklist.Item{{ControlID: uuid.New().String(), Task: "Establish backups", NoEvidence: true}},
	}}
	md, n := renderMarkdown("gen-1", sections)
	if n != 1 {
		t.Fatalf("approved count = %d, want 1", n)
	}
	if !strings.Contains(md, "no evidence yet") {
		t.Error("no-evidence item must carry the marker in the export")
	}
}

func TestLoadGeneration_RendersSections(t *testing.T) {
	tenant := uuid.NewString()
	genID := uuid.New()
	sections := []checklist.Section{{SectionID: uuid.New().String(), Role: checklist.RoleEngineering, AIAssisted: true, Items: []checklist.Item{{ControlID: uuid.New().String(), Task: "x"}}}}
	h := routerFor(fakeGenerator{}, fakeLoader{sections: sections}, tenant)
	code, body := do(t, h, http.MethodGet, "/v1/controls/checklist/"+genID.String())
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, body)
	}
	var out checklistWire
	_ = json.Unmarshal(body, &out)
	if out.Binding || out.Disclosure == "" {
		t.Error("load view must carry non-binding disclosure")
	}
	if len(out.Sections) != 1 || out.Sections[0].Role != "engineering" {
		t.Fatalf("unexpected sections: %+v", out.Sections)
	}
}

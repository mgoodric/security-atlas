package mappingoverlays

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #1804: every refusal the remap routes make before reaching the store.
// The handler has no store, so reaching it would panic.
func TestRemapRoutes_RefuseBeforeTheStore(t *testing.T) {
	t.Parallel()
	const tenant = "11111111-1111-1111-1111-111111111804"
	person := credstore.Credential{ID: "jwt:1804", TenantID: tenant, UserID: uuid.NewString(), IsAdmin: true}
	machine := credstore.Credential{ID: "key_1804", TenantID: tenant, UserID: "key_1804", IsAdmin: true}
	viewer := credstore.Credential{ID: "jwt:viewer", TenantID: tenant, UserID: uuid.NewString(), OwnerRoles: []string{"viewer"}}
	id := uuid.NewString()

	cases := []struct {
		name, method, path, body string
		cred                     *credstore.Credential
		noTenant                 bool
		want                     int
	}{
		{"list without a tenant", http.MethodGet, "/v1/mapping-overlays/remap-proposals", "", &person, true, http.StatusUnauthorized},
		{"approve without a credential", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", `{}`, nil, false, http.StatusForbidden},
		{"approve as a viewer", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", `{}`, &viewer, false, http.StatusForbidden},
		{"approve without a tenant", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", `{}`, &person, true, http.StatusUnauthorized},
		{"approve with a machine credential", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", `{}`, &machine, false, http.StatusForbidden},
		{"reject with an unknown field", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/reject", `{"why":"x"}`, &person, false, http.StatusBadRequest},
		{"reject naming an anchor", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/reject", `{"scf_id":"TRM-01"}`, &person, false, http.StatusBadRequest},
		{"keep legacy naming an anchor", http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/keep-legacy", `{"scf_id":"TRM-01"}`, &person, false, http.StatusBadRequest},
	}
	router := chi.NewRouter()
	New(nil).RegisterRemapRoutes(router)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if !tc.noTenant {
				var err error
				if ctx, err = tenancy.WithTenant(ctx, tenant); err != nil {
					t.Fatalf("WithTenant: %v", err)
				}
			}
			if tc.cred != nil {
				ctx = authctx.WithCredential(ctx, *tc.cred)
			}
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestRemapDecidingPerson(t *testing.T) {
	t.Parallel()
	user := uuid.NewString()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{ID: "jwt:x", UserID: user}))
	w := httptest.NewRecorder()
	if got, ok := remapDecidingPerson(w, r); !ok || got != user {
		t.Fatalf("remapDecidingPerson = %q, %v; want %s, true", got, ok, user)
	}
	w = httptest.NewRecorder()
	if _, ok := remapDecidingPerson(w, httptest.NewRequest(http.MethodPost, "/", nil)); ok || w.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: ok=%v status=%d; want false, 401", ok, w.Code)
	}
}

func TestRemapWireFrom(t *testing.T) {
	t.Parallel()
	edge := uuid.New()
	p := mappingoverlay.RemapProposal{ID: uuid.New(), Class: "no_successor", Status: "open", CreatedAt: time.Unix(0, 0)}
	if w := remapWireFrom(p); w.SuccessorSCFIDs == nil || len(w.SuccessorSCFIDs) != 0 || w.NewOverlayEdgeID != "" {
		t.Fatalf("open proposal wire = %+v; want empty successors and no new edge", w)
	}
	p.Status, p.NewOverlayEdgeID, p.TargetSCFID, p.Successors = "approved", &edge, "TRM-41", []string{"TRM-41"}
	if w := remapWireFrom(p); w.NewOverlayEdgeID != edge.String() || w.TargetSCFID != "TRM-41" || len(w.SuccessorSCFIDs) != 1 {
		t.Fatalf("approved proposal wire = %+v", w)
	}
}

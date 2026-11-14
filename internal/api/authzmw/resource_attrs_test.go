package authzmw_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

type auditorRoles struct{}

func (auditorRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return []authz.Role{authz.RoleAuditor}, nil
}

// assignedTo hands every caller the same assigned periods.
type assignedTo []string

func (a assignedTo) AttrsFor(context.Context, string, string, []authz.Role) (map[string]interface{}, error) {
	ids := make([]interface{}, len(a))
	for i, id := range a {
		ids[i] = id
	}
	return map[string]interface{}{"audit_period_ids": ids}, nil
}

// #2193: the attributes a ResourceAttrsFunc returns reach
// input.resource.attrs before the decision, so the auditor.rego audit-notes
// write rule sees the period the request names.
func TestMiddlewareWithResourceAttrs_AttrsReachTheDecision(t *testing.T) {
	t.Parallel()
	assigned := uuid.NewString()
	engine, err := authz.NewEngine(context.Background(), auditorRoles{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(assignedTo{assigned})

	cases := []struct {
		name  string
		attrs authzmw.ResourceAttrsFunc
		want  int
	}{
		{"assigned period", func(*http.Request) (map[string]any, error) {
			return map[string]any{"audit_period_id": assigned}, nil
		}, http.StatusCreated},
		{"another period", func(*http.Request) (map[string]any, error) {
			return map[string]any{"audit_period_id": uuid.NewString()}, nil
		}, http.StatusForbidden},
		{"no period", func(*http.Request) (map[string]any, error) { return nil, nil }, http.StatusForbidden},
		{"no hydrator", nil, http.StatusForbidden},
		{"hydrator error", func(*http.Request) (map[string]any, error) {
			return nil, errors.New("connection refused")
		}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := authzmw.MiddlewareWithResourceAttrs(engine, nil, tc.attrs)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))
			// A JWT auditor: jwtmw copies the token's roles into OwnerRoles,
			// which derives control_owner (no audit-note write of its own),
			// and the roles resolver adds auditor.
			subject := "user:" + uuid.NewString()
			req := httptest.NewRequest(http.MethodPost, "/v1/audit-notes", nil)
			req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         subject,
				TenantID:   uuid.NewString(),
				UserID:     subject,
				OwnerRoles: []string{"auditor"},
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("POST /v1/audit-notes: status %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

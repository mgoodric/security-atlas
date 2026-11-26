package approvals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: approvals lists only the types whose module is switched on; when
// the flag store cannot read a switch, the list answers 503
// module_state_unavailable, as ModuleGate does, not a plain 500. The pool
// points nowhere: the switch read fails before any query runs.
func TestList_StoreDownIs503(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	h := New(pool, moduleaccesstest.FlagsDown()).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/approvals")
	cred, _ := authctx.CredentialFromContext(req.Context())
	cred.IsApprover = true
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))

	rec := httptest.NewRecorder()
	h.List(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}

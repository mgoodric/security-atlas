package needsattention

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/approvals"
	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: needs-attention leaves out the calendar types whose module is
// switched off; when the flag store cannot read a switch, it answers 503
// module_state_unavailable, as ModuleGate does, not a plain 500. The pool
// points nowhere: the switch read fails before any query runs.
func TestList_StoreDownIs503(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	views := moduleaccesstest.StoreDown()
	approvalsH := approvals.New(pool, moduleaccesstest.FlagsDown()).WithModules(views)
	h := New(pool, calendar.NewStore(pool), approvalsH).WithModules(views)
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/needs-attention")

	rec := httptest.NewRecorder()
	h.List(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}

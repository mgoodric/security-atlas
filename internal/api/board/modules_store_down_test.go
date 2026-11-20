package board

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	board "github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: a board pack leaves out the sections whose module is switched
// off; when the flag store cannot read a switch, generation answers 503
// module_state_unavailable, as ModuleGate does, not a plain 500. The switch
// read runs before any store read, so the generator needs no database.
func TestPackGenerate_StoreDownIs503(t *testing.T) {
	t.Parallel()
	gen := board.NewPackGenerator(nil, nil).WithSwitches(moduleaccesstest.StoreDown())
	h := NewPack(gen, nil)
	ctx := moduleaccesstest.SuperAdminRequest(t, http.MethodPost, "/v1/board-packs").Context()
	req := httptest.NewRequest(http.MethodPost, "/v1/board-packs",
		strings.NewReader(`{"period_end":"2026-09-30"}`)).WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Generate(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}

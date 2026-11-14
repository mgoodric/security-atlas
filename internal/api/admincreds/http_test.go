package admincreds

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #1914: a ttl_seconds whose Duration would overflow is refused with 400
// before the store is touched. The Handler has a nil store, so reaching
// it would panic.
func TestIssue_RejectsOverflowingTTL(t *testing.T) {
	t.Parallel()
	for _, ttl := range []int64{-1, maxTTLSeconds + 1, 1 << 62, math.MaxInt64} {
		body, _ := json.Marshal(IssueRequest{TTLSeconds: ttl})
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
		req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{IsAdmin: true}))
		w := httptest.NewRecorder()
		New(nil).Issue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ttl_seconds=%d: want 400, got %d: %s", ttl, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "ttl_seconds") {
			t.Fatalf("ttl_seconds=%d: body %s does not name the field", ttl, w.Body.String())
		}
	}
}

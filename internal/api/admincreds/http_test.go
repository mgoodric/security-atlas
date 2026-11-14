package admincreds

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// #1953: liveMintedBy keeps only the user's own keys that have not expired.
func TestLiveMintedBy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	const user = "6f1c2b8e-0000-4000-8000-000000000001"
	creds := []credstore.Credential{
		{ID: "key_live", MintedBy: user},
		{ID: "key_ttl_live", MintedBy: user, IssuedAt: now.Add(-time.Hour), TTL: 2 * time.Hour},
		{ID: "key_expired", MintedBy: user, IssuedAt: now.Add(-3 * time.Hour), TTL: time.Hour},
		// A rotated-out predecessor authenticates until retires_at.
		{ID: "key_in_grace", MintedBy: user, RetiresAt: now.Add(time.Hour)},
		{ID: "key_past_grace", MintedBy: user, RetiresAt: now.Add(-time.Minute)},
		{ID: "key_other", MintedBy: "6f1c2b8e-0000-4000-8000-000000000002"},
		{ID: "key_unminted"},
	}
	got := liveMintedBy(creds, user, now)
	ids := make([]string, 0, len(got))
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "key_live,key_ttl_live,key_in_grace" {
		t.Fatalf("liveMintedBy = %v; want [key_live key_ttl_live key_in_grace]", ids)
	}
}

// #1953: a minted_by that is not a uuid is refused before the store is
// touched (the Handler has a nil store, so reaching it would panic).
func TestList_RejectsMalformedMintedBy(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/credentials?minted_by=not-a-uuid", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{IsAdmin: true}))
	w := httptest.NewRecorder()
	New(nil).List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

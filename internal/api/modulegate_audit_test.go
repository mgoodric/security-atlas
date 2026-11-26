package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

func TestGatedAttemptAuditorDedupesPerWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	a := newGatedAttemptAuditor(nil)
	a.now = func() time.Time { return now }

	if !a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("first attempt not recorded")
	}
	if a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("repeat inside the window recorded")
	}
	if !a.first("t\x00other\x00questionnaires.enabled") {
		t.Fatal("another actor's attempt deduped")
	}
	if !a.first("t\x00u\x00risk.enabled") {
		t.Fatal("another switch's attempt deduped")
	}
	now = now.Add(gatedAttemptWindow)
	if !a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("attempt after the window not recorded")
	}
}

// Past the cap the map is pruned or reset, never left to grow, and a reset
// records again rather than skip.
func TestGatedAttemptAuditorCap(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	a := newGatedAttemptAuditor(nil)
	a.now = func() time.Time { return now }
	for i := 0; i < gatedAttemptCap; i++ {
		a.first(string(rune(i)) + "k")
	}
	if !a.first("new") {
		t.Fatal("attempt past the cap not recorded")
	}
	if len(a.seen) > gatedAttemptCap {
		t.Fatalf("seen has %d entries; cap %d", len(a.seen), gatedAttemptCap)
	}
}

// Past the cap, expired entries are pruned and live ones kept: a live
// window survives the prune and still dedupes.
func TestGatedAttemptAuditorCapPrunes(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	a := newGatedAttemptAuditor(nil)
	a.now = func() time.Time { return now }
	for i := 0; i < gatedAttemptCap-1; i++ {
		a.first(string(rune(i)) + "k")
	}
	now = now.Add(gatedAttemptWindow)
	if !a.first("live") {
		t.Fatal("live attempt not recorded")
	}
	if !a.first("new") {
		t.Fatal("attempt past the cap not recorded")
	}
	if len(a.seen) != 2 {
		t.Fatalf("seen has %d entries after the prune; want 2 (live, new)", len(a.seen))
	}
	if a.first("live") {
		t.Fatal("prune dropped a live window")
	}
}

type fakeAuditWriter struct {
	mu    sync.Mutex
	err   error
	calls int
	ctxOK []bool
}

func (f *fakeAuditWriter) Write(ctx context.Context, _ authz.AuditRecord) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.ctxOK = append(f.ctxOK, ctx.Err() == nil)
	return uuid.New(), f.err
}

func gatedRequest(ctx context.Context) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/risks", nil)
	return r.WithContext(authctx.WithCredential(ctx, credstore.Credential{
		ID: "jwt:abc", UserID: "user:u1", TenantID: uuid.NewString(),
	}))
}

// A failed write leaves no window behind: the next attempt writes again
// (#2575).
func TestGatedAttemptAuditorRetriesFailedWrite(t *testing.T) {
	t.Parallel()
	w := &fakeAuditWriter{err: errors.New("db down")}
	a := newGatedAttemptAuditor(nil)
	a.audit = w
	r := gatedRequest(context.Background())
	at := featureflag.GatedAttempt{Module: "risk", FlagKey: "risk.enabled"}
	m := featureflag.Module{Key: "risk"}

	a.Record(r, at)
	a.Record(r, at)
	a.RecordNoAccess(r, m)
	a.RecordNoAccess(r, m)
	if w.calls != 4 {
		t.Fatalf("failed writes: %d calls; want 4 (each failure retried)", w.calls)
	}

	w.err = nil
	a.Record(r, at)
	a.Record(r, at)
	a.RecordNoAccess(r, m)
	a.RecordNoAccess(r, m)
	if w.calls != 6 {
		t.Fatalf("after a good write: %d calls; want 6 (repeat deduped)", w.calls)
	}
}

// A request cancelled before the write (the client hung up after its 404)
// still writes its row (#2575).
func TestGatedAttemptAuditorWritesAfterCancel(t *testing.T) {
	t.Parallel()
	w := &fakeAuditWriter{}
	a := newGatedAttemptAuditor(nil)
	a.audit = w
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Record(gatedRequest(ctx), featureflag.GatedAttempt{Module: "risk", FlagKey: "risk.enabled"})
	if w.calls != 1 || !w.ctxOK[0] {
		t.Fatalf("calls=%d ctxOK=%v; want one write on a live context", w.calls, w.ctxOK)
	}
}

// With no writer configured the write fails and the attempt holds no
// window.
func TestGatedAttemptAuditorNilWriter(t *testing.T) {
	t.Parallel()
	a := newGatedAttemptAuditor(nil)
	a.Record(gatedRequest(context.Background()), featureflag.GatedAttempt{Module: "risk", FlagKey: "risk.enabled"})
	if len(a.seen) != 0 {
		t.Fatalf("seen has %d entries after a failed write; want 0", len(a.seen))
	}
}

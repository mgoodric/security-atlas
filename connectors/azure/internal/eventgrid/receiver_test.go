package eventgrid

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// rereadCall records one Rereader invocation for assertions.
type rereadCall struct {
	resourceType ResourceType
	resourceID   string
}

// recordingReread is a test Rereader that records its calls and returns a
// configurable pushed-count. A pushed-count of 0 models a resource that no longer
// resolves (no-fabrication).
type recordingReread struct {
	mu     sync.Mutex
	calls  []rereadCall
	pushed int // what each call returns
}

func (r *recordingReread) fn(_ context.Context, rt ResourceType, id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, rereadCall{rt, id})
	return r.pushed, nil
}

func (r *recordingReread) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newTestReceiver(t *testing.T, rr Rereader, window time.Duration) *Receiver {
	t.Helper()
	rec, err := NewReceiver(Config{
		Verifier:       NewDeliveryKeyVerifier(CredentialHeader, "Authorization", testDeliveryKey),
		Reread:         rr,
		CoalesceWindow: window,
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return rec
}

func wrapped(rec *Receiver) http.Handler {
	return ValidationHandler{Inner: rec}
}

func storageEventBody(id string) []byte {
	return []byte(`[{"eventType":"Microsoft.Resources.ResourceWriteSuccess","subject":"` + id + `"}]`)
}

const testStorageID = "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct1"

// errReread is a sentinel re-read failure for the swallow test.
var errReread = errTest("reread boom")

type errTest string

func (e errTest) Error() string { return string(e) }

// AC-5 / P0-522-2 (DOMINANT): a delivery with a forged/absent delivery key is
// rejected 401 BEFORE any re-read.
func TestReceiver_ForgedDeliveryKey_401NoReread(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	h := wrapped(rec)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	req.Header.Set("Authorization", "test-forged-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if rr.callCount() != 0 {
		t.Fatalf("forged delivery triggered %d re-reads, want 0", rr.callCount())
	}
}

// AC-5 / P0-522-2: a missing delivery key is rejected 401.
func TestReceiver_MissingDeliveryKey_401(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	h := wrapped(rec)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// AC-1/AC-2: an authenticated change event acks 200 and (after the worker drains)
// triggers ONE re-read routed to the storage reader for the changed resource id.
func TestReceiver_AuthedEvent_RereadsChangedResource(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, 10*time.Millisecond)
	h := wrapped(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	waitForCalls(t, rr, 1)
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.calls[0].resourceType != ResourceStorage || rr.calls[0].resourceID != testStorageID {
		t.Fatalf("re-read routed to %q %q, want storage %q", rr.calls[0].resourceType, rr.calls[0].resourceID, testStorageID)
	}
}

// AC-5 (no-fabrication, the central P0-522-1 test): a forged/payload-only event
// whose resource id resolves to NO real resource produces ZERO records — the
// re-read (not the event payload) is the only source of record data, so the
// Rereader returns pushed=0 and nothing is fabricated.
func TestReceiver_NoFabrication_RereadFindsNothing(t *testing.T) {
	rr := &recordingReread{pushed: 0} // re-read resolves nothing
	rec := newTestReceiver(t, rr.fn, 10*time.Millisecond)
	h := wrapped(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	// Authenticated, well-formed, but the resource id does not resolve on re-read.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ack)", w.Code)
	}
	waitForCalls(t, rr, 1)
	// The re-read ran (it's the only data source) and returned 0 pushed: no record
	// was fabricated from the event payload.
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.pushed != 0 {
		t.Fatal("test misconfigured")
	}
}

// AC-2: an authenticated event for an UNMAPPED resource type is dropped honestly
// (acked 200, NO re-read).
func TestReceiver_UnmappedResource_DroppedNoReread(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, 10*time.Millisecond)
	h := wrapped(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	vmID := "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(vmID)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (honest drop ack)", w.Code)
	}
	// Give the worker a beat; it must NOT re-read an unmapped resource.
	time.Sleep(50 * time.Millisecond)
	if rr.callCount() != 0 {
		t.Fatalf("unmapped resource triggered %d re-reads, want 0", rr.callCount())
	}
}

// D6: N events for the SAME resource within the coalescing window collapse to ONE
// re-read.
func TestReceiver_Coalescing_OneRereadPerResource(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	// A long window so all five events land in the same coalescing batch.
	rec := newTestReceiver(t, rr.fn, 80*time.Millisecond)
	h := wrapped(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
		req.Header.Set("Authorization", testDeliveryKey)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("event %d status = %d", i, w.Code)
		}
	}
	waitForCalls(t, rr, 1)
	// Allow the window to fully close; assert it did NOT fan out to 5 re-reads.
	time.Sleep(150 * time.Millisecond)
	if got := rr.callCount(); got != 1 {
		t.Fatalf("5 same-resource events coalesced to %d re-reads, want 1", got)
	}
}

// D6: distinct resources within a window each get their own re-read.
func TestReceiver_Coalescing_DistinctResourcesEachReread(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, 80*time.Millisecond)
	h := wrapped(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	ids := []string{
		testStorageID,
		"/subscriptions/s1/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv1",
	}
	for _, id := range ids {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(id)))
		req.Header.Set("Authorization", testDeliveryKey)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
	waitForCalls(t, rr, 2)
	if got := rr.callCount(); got != 2 {
		t.Fatalf("2 distinct resources → %d re-reads, want 2", got)
	}
}

// D6: a full queue drops the newest event (the pull backstop covers it) rather than
// blocking the handler.
func TestReceiver_QueueFull_DropsNewest(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec, err := NewReceiver(Config{
		Verifier:       NewDeliveryKeyVerifier(CredentialHeader, "Authorization", testDeliveryKey),
		Reread:         rr.fn,
		QueueDepth:     2,
		CoalesceWindow: time.Second,
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	h := wrapped(rec)
	// Do NOT start the worker: the queue fills and the 3rd distinct event drops.
	for i, id := range []string{
		"/subscriptions/s1/providers/Microsoft.Storage/storageAccounts/a",
		"/subscriptions/s1/providers/Microsoft.Storage/storageAccounts/b",
		"/subscriptions/s1/providers/Microsoft.Storage/storageAccounts/c",
	} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(id)))
		req.Header.Set("Authorization", testDeliveryKey)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("event %d status = %d, want 200", i, w.Code)
		}
	}
	if rec.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", rec.Dropped())
	}
}

// AC-5 / P0 (handshake): the SubscriptionValidation handshake responds 200 with
// {"validationResponse":code}, triggers NO verification, and builds NO record.
func TestReceiver_ValidationHandshake_EchoesNoReread(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	h := wrapped(rec)

	body := []byte(`[{"eventType":"Microsoft.EventGrid.SubscriptionValidationEvent","data":{"validationCode":"test-handshake-code-xyz"}}]`)
	// No Authorization header at all — the handshake must succeed unauthenticated
	// (it establishes the endpoint before the delivery key is wired in).
	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp validationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ValidationResponse != "test-handshake-code-xyz" {
		t.Errorf("validationResponse = %q, want the echoed code", resp.ValidationResponse)
	}
	if rr.callCount() != 0 {
		t.Fatalf("handshake triggered %d re-reads, want 0", rr.callCount())
	}
}

// The query-param delivery-key location authenticates a real delivery.
func TestReceiver_QueryDeliveryKey_Authed(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec, err := NewReceiver(Config{
		Verifier:       NewDeliveryKeyVerifier(CredentialQuery, "code", testDeliveryKey),
		Reread:         rr.fn,
		CoalesceWindow: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	h := wrapped(rec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid?code="+testDeliveryKey, bytes.NewReader(storageEventBody(testStorageID)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	waitForCalls(t, rr, 1)

	// Wrong query code → 401.
	bad := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid?code=test-wrong", bytes.NewReader(storageEventBody(testStorageID)))
	bw := httptest.NewRecorder()
	h.ServeHTTP(bw, bad)
	if bw.Code != http.StatusUnauthorized {
		t.Fatalf("wrong query code status = %d, want 401", bw.Code)
	}
}

// Method other than POST is rejected 405 by the shared skeleton (through the
// handshake adapter's delegation).
func TestReceiver_NonPost_405(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	h := wrapped(rec)
	req := httptest.NewRequest(http.MethodGet, "/webhooks/azure/eventgrid", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// A malformed (authenticated) body is rejected 400.
func TestReceiver_MalformedBody_400(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	h := wrapped(rec)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader([]byte(`{bad`)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// NewReceiver validates required fields.
func TestNewReceiver_Validation(t *testing.T) {
	t.Parallel()
	if _, err := NewReceiver(Config{Reread: func(context.Context, ResourceType, string) (int, error) { return 0, nil }}); err == nil {
		t.Error("want error on missing Verifier")
	}
	if _, err := NewReceiver(Config{Verifier: NewDeliveryKeyVerifier(CredentialHeader, "Authorization", testDeliveryKey)}); err == nil {
		t.Error("want error on missing Reread")
	}
}

// Run exits promptly on context cancel.
func TestReceiver_RunExitsOnCancel(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit on cancel")
	}
}

// The body-too-large path returns 413 on the handshake adapter's size-bounded peek.
func TestReceiver_OversizeBody_413(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec, err := NewReceiver(Config{
		Verifier:     NewDeliveryKeyVerifier(CredentialHeader, "Authorization", testDeliveryKey),
		Reread:       rr.fn,
		MaxBodyBytes: 8,
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	h := ValidationHandler{Inner: rec, MaxBodyBytes: 8}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader([]byte(`[{"eventType":"x"}]`)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

// The worker swallows a Rereader error (the pull profile is the backstop) — a
// failing re-read must not crash the long-lived receiver.
func TestReceiver_RereadError_Swallowed(t *testing.T) {
	called := make(chan struct{}, 1)
	rr := func(_ context.Context, _ ResourceType, _ string) (int, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return 0, errReread
	}
	rec := newTestReceiver(t, rr, 10*time.Millisecond)
	h := wrapped(rec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	req.Header.Set("Authorization", testDeliveryKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("Rereader not invoked")
	}
	// The receiver keeps running after the error (no panic, worker alive): a second
	// event still drains.
	req2 := httptest.NewRequest(http.MethodPost, "/webhooks/azure/eventgrid", bytes.NewReader(storageEventBody(testStorageID)))
	req2.Header.Set("Authorization", testDeliveryKey)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second event status = %d, want 200", w2.Code)
	}
}

// NewServer + Serve lifecycle: the server binds a loopback port and drains on ctx
// cancel.
func TestServerLifecycle(t *testing.T) {
	rr := &recordingReread{pushed: 1}
	rec := newTestReceiver(t, rr.fn, time.Millisecond)
	srv := NewServer("127.0.0.1:0", "/webhooks/azure/eventgrid", wrapped(rec))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv) }()
	// Give it a beat to bind, then cancel for a graceful drain.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on graceful shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

// waitForCalls polls until the recordingReread has at least n calls or the deadline
// passes.
func waitForCalls(t *testing.T, rr *recordingReread, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rr.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d re-read calls (got %d)", n, rr.callCount())
}

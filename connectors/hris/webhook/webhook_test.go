// Tests for the shared source-side HRIS webhook receiver (slice 573).
//
// The load-bearing assertions:
//   - an UNSIGNED delivery is rejected (401) before any record is built/pushed
//   - a FORGED / wrong-signature delivery is rejected (401) before any push
//   - a correctly-signed delivery is accepted (200) and pushes ONE record
//   - the webhook-emitted record's idempotency key COLLIDES with the
//     pull-emitted record for the same (vendor, worker, hour) — dedup vs pull
//   - the emitted record carries NO excluded PII (P0-491-3 guard, reused)
//   - an oversized body is rejected (413) before verification
//
// No vendor-shaped secrets in fixtures — neutral "test-*" strings only.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/hris/idem"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
)

const (
	testSecret = "test-webhook-secret"
	testHeader = "X-Test-Signature"
)

// recordingPusher captures every pushed record so a test can assert on the
// emitted idempotency key + payload.
type recordingPusher struct {
	pushed  []*evidencev1.EvidenceRecord
	pushErr error
}

func (p *recordingPusher) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if p.pushErr != nil {
		return nil, p.pushErr
	}
	p.pushed = append(p.pushed, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}

// staticFetcher returns one canned worker for any id (the trigger+re-read path).
type staticFetcher struct {
	raw     worker.RawWorker
	ok      bool
	fetched []string
	err     error
}

func (f *staticFetcher) FetchWorker(_ context.Context, id string) (worker.RawWorker, bool, error) {
	f.fetched = append(f.fetched, id)
	if f.err != nil {
		return worker.RawWorker{}, false, f.err
	}
	return f.raw, f.ok, nil
}

// idParser pulls worker id(s) out of a trivial test envelope. ids is the slice it
// returns; an empty ids (the zero value) models the "no actionable worker" ack
// path. err short-circuits to 400.
type idParser struct {
	ids []string
	err error
}

func (p idParser) ParseWorkerIDs(_ []byte) ([]string, error) { return p.ids, p.err }

// oneID is a convenience for the single-worker tests carried over from slice 573.
func oneID(id string) idParser { return idParser{ids: []string{id}} }

func hexSig(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 9, 15, 30, 0, 0, time.UTC) }
}

func testReceiver(t *testing.T, p *recordingPusher, f *staticFetcher, parser PayloadParser) *Receiver {
	t.Helper()
	v := NewHMACVerifier(worker.HRISRippling, testSecret, testHeader, "", EncodingHex)
	rec, err := NewReceiver(Config{
		Vendor:      worker.HRISRippling,
		Verifier:    v,
		Parser:      parser,
		Fetcher:     f,
		Pusher:      p,
		ActorID:     "connector:rippling:webhook@test",
		Environment: "prod",
		Now:         fixedClock(),
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return rec
}

func TestReceiver_RejectsUnsigned(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "w1", Status: worker.StatusTerminated}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))

	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	// No signature header.
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned delivery: status = %d; want 401", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("unsigned delivery pushed %d records; want 0", len(pusher.pushed))
	}
	if len(fetcher.fetched) != 0 {
		t.Errorf("unsigned delivery triggered re-read; must reject before re-read")
	}
}

func TestReceiver_RejectsForgedSignature(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "w1", Status: worker.StatusTerminated}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))

	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	// Signature computed with the WRONG secret (attacker does not know testSecret).
	req.Header.Set(testHeader, hexSig("wrong-secret", body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("forged delivery: status = %d; want 401", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("forged delivery pushed %d records; want 0", len(pusher.pushed))
	}
	if len(fetcher.fetched) != 0 {
		t.Errorf("forged delivery triggered re-read; must reject before re-read")
	}
}

func TestReceiver_AcceptsSignedAndPushesOne(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{
		WorkerID: "w1", Status: worker.StatusTerminated, Title: "SWE", Department: "Eng",
	}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))

	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("signed delivery: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 1 {
		t.Fatalf("signed delivery pushed %d records; want 1", len(pusher.pushed))
	}
	if len(fetcher.fetched) != 1 || fetcher.fetched[0] != "w1" {
		t.Errorf("re-read = %v; want [w1]", fetcher.fetched)
	}
	pm := pusher.pushed[0].GetPayload().AsMap()
	if pm["employment_status"] != "terminated" {
		t.Errorf("payload employment_status = %v; want terminated", pm["employment_status"])
	}
}

// idCountingFetcher returns a distinct terminated worker per requested id and
// records the order of re-reads, so a fan-out test can assert one re-read + one
// push per changed employee.
type idCountingFetcher struct {
	fetched []string
	failIDs map[string]error // ids that should error the re-read
	missing map[string]bool  // ids the source no longer returns (ok=false)
}

func (f *idCountingFetcher) FetchWorker(_ context.Context, id string) (worker.RawWorker, bool, error) {
	f.fetched = append(f.fetched, id)
	if err := f.failIDs[id]; err != nil {
		return worker.RawWorker{}, false, err
	}
	if f.missing[id] {
		return worker.RawWorker{}, false, nil
	}
	return worker.RawWorker{WorkerID: id, Status: worker.StatusTerminated}, true, nil
}

func signedReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	return req
}

func fanoutReceiver(t *testing.T, p *recordingPusher, f WorkerFetcher, parser PayloadParser) *Receiver {
	t.Helper()
	v := NewHMACVerifier(worker.HRISBambooHR, testSecret, testHeader, "", EncodingHex)
	rec, err := NewReceiver(Config{
		Vendor:      worker.HRISBambooHR,
		Verifier:    v,
		Parser:      parser,
		Fetcher:     f,
		Pusher:      p,
		ActorID:     "connector:bamboohr:webhook@test",
		Environment: "prod",
		Now:         fixedClock(),
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return rec
}

// TestReceiver_FansOutToEachWorker is the load-bearing slice-655 assertion: a
// signed delivery carrying N>1 changed workers re-reads + pushes a record for
// EACH worker, not just the first.
func TestReceiver_FansOutToEachWorker(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"a", "b", "c"}})

	body := `{"employees":[{"id":"a"},{"id":"b"},{"id":"c"}]}`
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, body))

	if rr.Code != http.StatusOK {
		t.Fatalf("multi-worker delivery: status = %d; want 200", rr.Code)
	}
	if len(fetcher.fetched) != 3 {
		t.Fatalf("re-read %d workers; want 3 (%v)", len(fetcher.fetched), fetcher.fetched)
	}
	if len(pusher.pushed) != 3 {
		t.Fatalf("pushed %d records; want 3 (one per changed worker)", len(pusher.pushed))
	}
	got := map[string]bool{}
	for _, rec := range pusher.pushed {
		got[rec.GetPayload().AsMap()["worker_id"].(string)] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !got[want] {
			t.Errorf("no record pushed for worker %q", want)
		}
	}
}

// TestReceiver_ForgedDeliveryNoFanOut asserts a forged multi-worker delivery is
// rejected BEFORE any employee is processed — signature verify is the first step,
// once per delivery, ahead of the fan-out (P0 anti-criterion).
func TestReceiver_ForgedDeliveryNoFanOut(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"a", "b", "c"}})

	body := `{"employees":[{"id":"a"},{"id":"b"},{"id":"c"}]}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig("wrong-secret", body)) // forged
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("forged multi-worker delivery: status = %d; want 401", rr.Code)
	}
	if len(fetcher.fetched) != 0 {
		t.Errorf("forged delivery re-read %d workers; want 0 (reject before fan-out)", len(fetcher.fetched))
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("forged delivery pushed %d records; want 0", len(pusher.pushed))
	}
}

// TestReceiver_PartialFailureEmitsSuccessesAnd502 asserts one bad worker mid
// fan-out does not drop the whole delivery or fail the others: the healthy
// workers are pushed, and the delivery returns 502 so the vendor retries (the
// successes dedup-collapse on the retry).
func TestReceiver_PartialFailureEmitsSuccessesAnd502(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{failIDs: map[string]error{"b": errors.New("bamboohr 503")}}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"a", "b", "c"}})

	body := `{"employees":[{"id":"a"},{"id":"b"},{"id":"c"}]}`
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, body))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("partial failure: status = %d; want 502", rr.Code)
	}
	if len(fetcher.fetched) != 3 {
		t.Errorf("re-read %d workers; want 3 (the failure must not abort the others)", len(fetcher.fetched))
	}
	if len(pusher.pushed) != 2 {
		t.Fatalf("pushed %d records; want 2 (a + c succeed, b failed)", len(pusher.pushed))
	}
}

// TestReceiver_MissingWorkerMidFanOutIsNotFailure asserts a worker the source no
// longer returns (ok=false) emits nothing but does NOT count as a failure — the
// delivery still acks 200 and the present workers are pushed.
func TestReceiver_MissingWorkerMidFanOutIsNotFailure(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{missing: map[string]bool{"b": true}}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"a", "b", "c"}})

	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, `{"employees":[{"id":"a"},{"id":"b"},{"id":"c"}]}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("missing-worker mid fan-out: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 2 {
		t.Fatalf("pushed %d records; want 2 (a + c; b is gone)", len(pusher.pushed))
	}
}

// TestReceiver_DedupsRepeatedIDsInDelivery asserts a delivery that repeats an id
// re-reads + pushes that worker only ONCE (the dedup-cap collapses duplicates).
func TestReceiver_DedupsRepeatedIDsInDelivery(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"a", "a", "b", "a"}})

	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, `{"employees":[{"id":"a"},{"id":"a"},{"id":"b"}]}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("repeated-id delivery: status = %d; want 200", rr.Code)
	}
	if len(fetcher.fetched) != 2 {
		t.Errorf("re-read %d workers; want 2 distinct (%v)", len(fetcher.fetched), fetcher.fetched)
	}
	if len(pusher.pushed) != 2 {
		t.Errorf("pushed %d records; want 2 distinct", len(pusher.pushed))
	}
}

// TestReceiver_FanOutCapBounds asserts a delivery carrying more than MaxFanOut
// distinct workers is bounded to the cap.
func TestReceiver_FanOutCapBounds(t *testing.T) {
	ids := make([]string, MaxFanOut+10)
	for i := range ids {
		ids[i] = fmt.Sprintf("w%d", i)
	}
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: ids})

	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, `{"employees":[]}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("over-cap delivery: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != MaxFanOut {
		t.Fatalf("pushed %d records; want MaxFanOut=%d", len(pusher.pushed), MaxFanOut)
	}
}

// TestReceiver_EmptyIDsAcks asserts a parser returning an empty slice (no
// actionable worker) acks 200 and emits nothing.
func TestReceiver_EmptyIDsAcks(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: nil})

	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, `{"employees":[]}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("empty-ids delivery: status = %d; want 200", rr.Code)
	}
	if len(fetcher.fetched) != 0 || len(pusher.pushed) != 0 {
		t.Errorf("empty-ids delivery re-read %d / pushed %d; want 0/0", len(fetcher.fetched), len(pusher.pushed))
	}
}

// TestReceiver_BlankIDsSkipped asserts blank ids in the slice are skipped.
func TestReceiver_BlankIDsSkipped(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &idCountingFetcher{}
	rec := fanoutReceiver(t, pusher, fetcher, idParser{ids: []string{"", "a", ""}})

	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, signedReq(t, `{"employees":[]}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("blank-ids delivery: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 1 {
		t.Fatalf("pushed %d records; want 1 (only the non-blank id)", len(pusher.pushed))
	}
}

// TestReceiver_DedupKeyMatchesPull is the load-bearing dedup assertion: the
// webhook-emitted record and a pull-emitted record for the SAME worker within
// the SAME UTC hour must carry the SAME idempotency key, so the ledger collapses
// them and a termination is not double-written via both a webhook and a poll.
func TestReceiver_DedupKeyMatchesPull(t *testing.T) {
	clock := fixedClock()
	raw := worker.RawWorker{WorkerID: "w1", Status: worker.StatusTerminated}

	// Webhook path.
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: raw, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))
	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rec.ServeHTTP(httptest.NewRecorder(), req)
	if len(pusher.pushed) != 1 {
		t.Fatalf("webhook push count = %d; want 1", len(pusher.pushed))
	}
	webhookKey := pusher.pushed[0].GetIdempotencyKey()

	// Pull path: the same worker normalized + built with the same clock.
	wks := worker.Normalize(worker.HRISRippling, []worker.RawWorker{raw}, clock)
	pullRec, err := workerrecord.Build(wks[0], "scf:IAC-22", "connector:rippling:workers@test", "rippling", "prod")
	if err != nil {
		t.Fatalf("pull build: %v", err)
	}
	pullKey := pullRec.GetIdempotencyKey()

	if webhookKey != pullKey {
		t.Fatalf("dedup broken: webhook key %q != pull key %q (same worker+hour must collapse)", webhookKey, pullKey)
	}
	// Sanity: the key is the canonical hour-truncated lifecycle key.
	want := idem.WorkerLifecycleKey("rippling", "w1", clock().UTC().Truncate(time.Hour))
	if webhookKey != want {
		t.Errorf("idempotency key = %q; want %q", webhookKey, want)
	}
}

// TestReceiver_NoSensitivePII reuses the slice-491 over-collection guard: even
// when the re-read carries banned-shaped values in every free-text field, the
// emitted record carries worker-lifecycle facts only.
func TestReceiver_NoSensitivePII(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{
		WorkerID: "1", Status: worker.StatusTerminated, Title: "Software Engineer",
		Department: "Engineering", ManagerAssignmentID: "mgr-9", WorkEmail: "a.engineer@corp.example",
	}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("1"))
	body := `{"worker_id":"1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rec.ServeHTTP(httptest.NewRecorder(), req)
	if len(pusher.pushed) != 1 {
		t.Fatalf("push count = %d; want 1", len(pusher.pushed))
	}
	allowed := map[string]bool{
		"source_hris": true, "worker_id": true, "employment_status": true,
		"start_date": true, "end_date": true, "title": true, "department": true,
		"manager_assignment_id": true, "work_email": true,
	}
	banned := []string{"ssn", "national_id", "salary", "compensation", "comp", "bank",
		"routing", "iban", "address", "benefit", "health", "performance", "rating",
		"dob", "birth", "gender", "ethnicity"}
	assertNoBanned(t, pusher.pushed[0], allowed, banned)
}

func TestReceiver_OversizedBodyRejected(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "w1"}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))

	big := bytes.Repeat([]byte("a"), int(MaxBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", bytes.NewReader(big))
	req.Header.Set(testHeader, hexSig(testSecret, string(big)))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d; want 413", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("oversized body pushed %d records; want 0", len(pusher.pushed))
	}
}

func TestReceiver_RejectsNonPost(t *testing.T) {
	rec := testReceiver(t, &recordingPusher{}, &staticFetcher{ok: true}, oneID("w1"))
	req := httptest.NewRequest(http.MethodGet, "/hooks/hris", nil)
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d; want 405", rr.Code)
	}
}

func TestReceiver_ParserNoWorkerAcksWithoutEmit(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{ok: true}
	rec := testReceiver(t, pusher, fetcher, idParser{})
	body := `{"event":"unrelated"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("no-worker delivery: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("no-worker delivery pushed %d; want 0", len(pusher.pushed))
	}
	if len(fetcher.fetched) != 0 {
		t.Errorf("no-worker delivery re-read; want none")
	}
}

func TestReceiver_ParserErrorIsBadRequest(t *testing.T) {
	rec := testReceiver(t, &recordingPusher{}, &staticFetcher{ok: true}, idParser{err: errors.New("bad json")})
	body := `not-json`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("parser error: status = %d; want 400", rr.Code)
	}
}

func TestReceiver_FetchMissingWorkerEmitsNothing(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{ok: false} // source no longer returns the worker
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))
	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("missing worker: status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("missing worker pushed %d; want 0", len(pusher.pushed))
	}
}

func TestReceiver_FetchErrorIsBadGateway(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{err: errors.New("rippling 503")}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))
	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("fetch error: status = %d; want 502", rr.Code)
	}
}

func TestReceiver_PushErrorIsBadGateway(t *testing.T) {
	pusher := &recordingPusher{pushErr: errors.New("push rejected")}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "w1", Status: worker.StatusTerminated}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))
	body := `{"worker_id":"w1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hexSig(testSecret, body))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("push error: status = %d; want 502", rr.Code)
	}
}

func TestNewReceiver_ValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no vendor", Config{Verifier: stubVerifier{}, Parser: idParser{}, Fetcher: &staticFetcher{}, Pusher: &recordingPusher{}, Environment: "p"}},
		{"no verifier", Config{Vendor: worker.HRISRippling, Parser: idParser{}, Fetcher: &staticFetcher{}, Pusher: &recordingPusher{}, Environment: "p"}},
		{"no parser", Config{Vendor: worker.HRISRippling, Verifier: stubVerifier{}, Fetcher: &staticFetcher{}, Pusher: &recordingPusher{}, Environment: "p"}},
		{"no fetcher", Config{Vendor: worker.HRISRippling, Verifier: stubVerifier{}, Parser: idParser{}, Pusher: &recordingPusher{}, Environment: "p"}},
		{"no pusher", Config{Vendor: worker.HRISRippling, Verifier: stubVerifier{}, Parser: idParser{}, Fetcher: &staticFetcher{}, Environment: "p"}},
		{"no environment", Config{Vendor: worker.HRISRippling, Verifier: stubVerifier{}, Parser: idParser{}, Fetcher: &staticFetcher{}, Pusher: &recordingPusher{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewReceiver(c.cfg); err == nil {
				t.Errorf("NewReceiver(%s): want error", c.name)
			}
		})
	}
}

func TestNewReceiver_DefaultsControlAndClock(t *testing.T) {
	r, err := NewReceiver(Config{
		Vendor: worker.HRISRippling, Verifier: stubVerifier{}, Parser: idParser{},
		Fetcher: &staticFetcher{}, Pusher: &recordingPusher{}, Environment: "prod",
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	if r.cfg.ControlID != "scf:IAC-22" {
		t.Errorf("default ControlID = %q; want scf:IAC-22", r.cfg.ControlID)
	}
	if r.now == nil {
		t.Error("clock not defaulted")
	}
}

type stubVerifier struct{}

func (stubVerifier) Verify([]byte, http.Header) error { return nil }
func (stubVerifier) Vendor() worker.HRIS              { return worker.HRISRippling }

// assertNoBanned walks the payload and asserts only allow-listed keys + no banned
// substring (mirrors the slice-491 cmd-layer guard).
func assertNoBanned(t *testing.T, rec *evidencev1.EvidenceRecord, allowed map[string]bool, banned []string) {
	t.Helper()
	pm := rec.GetPayload().AsMap()
	for k := range pm {
		if !allowed[k] {
			t.Errorf("non-allow-listed payload key %q (over-collection guard P0-491-3)", k)
		}
	}
	walk(t, pm, banned)
}

func walk(t *testing.T, v any, banned []string) {
	t.Helper()
	switch x := v.(type) {
	case string:
		for _, b := range banned {
			if strings.Contains(strings.ToLower(x), b) {
				t.Errorf("payload string %q contains banned substring %q", x, b)
			}
		}
	case map[string]any:
		for k := range x {
			for _, b := range banned {
				if strings.Contains(strings.ToLower(k), b) {
					t.Errorf("payload key %q contains banned substring %q", k, b)
				}
			}
			walk(t, x[k], banned)
		}
	case []any:
		for _, vv := range x {
			walk(t, vv, banned)
		}
	}
}

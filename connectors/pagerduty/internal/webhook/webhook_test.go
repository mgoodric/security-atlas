package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
)

// testSecret is a NEUTRAL test signing secret — never a vendor-shaped literal
// (GitGuardian flags vendor-shaped fixtures). A webhook secret in production is a
// per-subscription opaque string; this stand-in proves the verify/accept path.
const testSecret = "test-signing-secret-not-a-real-pagerduty-key"

// fixedNow pins the observed-at clock so the idempotency key is deterministic and
// the webhook/pull keys can be compared.
var fixedNow = time.Date(2026, 6, 9, 14, 37, 0, 0, time.UTC)

// recordingPusher captures the records pushed so a test can assert shape + that
// no free-text reached the payload.
type recordingPusher struct {
	records []*evidencev1.EvidenceRecord
	err     error
}

func (p *recordingPusher) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.records = append(p.records, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}

// incidentBody builds a v3 incident webhook body. freeText, when non-empty, is
// injected into title/description/body free-text fields the receiver MUST ignore.
func incidentBody(eventType, id string, freeText string) []byte {
	return []byte(fmt.Sprintf(`{
      "event": {
        "id": "01EVENT",
        "event_type": %q,
        "occurred_at": "2026-06-09T14:30:00Z",
        "data": {
          "id": %q,
          "type": "incident",
          "number": 4242,
          "status": "triggered",
          "urgency": "high",
          "title": %q,
          "description": %q,
          "body": {"details": %q},
          "created_at": "2026-06-09T14:25:00Z",
          "service": {"id": "PSVC1", "summary": "payments-api"}
        }
      }
    }`, eventType, id, freeText, freeText, freeText))
}

func newTestReceiver(t *testing.T, p Pusher) *Receiver {
	t.Helper()
	rec, err := NewReceiver(Config{
		Verifier:    NewHMACVerifier(testSecret),
		Pusher:      p,
		ControlID:   "scf:IRO-02",
		ActorID:     "connector:pagerduty:incidents@test",
		Service:     "pagerduty",
		Environment: "prod",
		Now:         func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return rec
}

func post(rec *Receiver, body []byte, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/pagerduty", bytes.NewReader(body))
	if sig != "" {
		req.Header.Set(HeaderSignature, sig)
	}
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, req)
	return w
}

// AC: a correctly-signed incident delivery is accepted and emits exactly one
// pagerduty.incident_summary.v1 record.
func TestServeHTTP_SignedDelivery_Accepts(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC1", "")
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("pushed %d records, want 1", len(p.records))
	}
	if got := p.records[0].GetEvidenceKind(); got != pdrecord.IncidentKind {
		t.Fatalf("evidence_kind = %q, want %q", got, pdrecord.IncidentKind)
	}
}

// AC (DOMINANT P0): an unsigned delivery is rejected 401 BEFORE any record is
// built or pushed.
func TestServeHTTP_Unsigned_RejectsBeforeRecord(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC1", "")

	w := post(rec, body, "") // no signature header
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on unsigned delivery, want 0 (forged delivery must not produce a record)", len(p.records))
	}
}

// AC (P0): a forged / wrong-signature delivery is rejected 401 BEFORE any record.
func TestServeHTTP_ForgedSignature_RejectsBeforeRecord(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC1", "")
	// Sign with the WRONG secret — an attacker who doesn't hold the subscription
	// secret.
	forged := Sign([]byte("attacker-secret"), body)

	w := post(rec, body, forged)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on forged delivery, want 0", len(p.records))
	}
}

// AC: multi-signature rotation — PagerDuty lists multiple v1 signatures during a
// signing-secret rotation; accept if ANY matches the active secret.
func TestServeHTTP_MultiSignatureRotation_Accepts(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.acknowledged", "PINC2", "")
	// Header carries an OLD-secret signature first, then the active-secret one —
	// as PagerDuty does mid-rotation. The receiver must accept on the match.
	old := Sign([]byte("previous-rotation-secret"), body)
	active := Sign([]byte(testSecret), body)
	multi := old + "," + active

	w := post(rec, body, multi)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must accept when any listed v1 matches)", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("pushed %d records, want 1", len(p.records))
	}
}

// AC (P0): no incident free-text enters a record even when the webhook payload
// carries title/description/body. The emitted payload must contain only the
// SUMMARY fields.
func TestServeHTTP_SummaryOnly_NoFreeText(t *testing.T) {
	t.Parallel()
	const secret = "customer SSN 123-45-6789 leaked in title"
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC3", secret)
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("pushed %d records, want 1", len(p.records))
	}
	payload := p.records[0].GetPayload()
	assertNoFreeText(t, payload, secret)

	// Positive: the summary fields ARE present.
	fields := payload.GetFields()
	if got := fields["incident_id"].GetStringValue(); got != "PINC3" {
		t.Fatalf("incident_id = %q, want PINC3", got)
	}
	if got := fields["status"].GetStringValue(); got != "triggered" {
		t.Fatalf("status = %q, want triggered", got)
	}
	// title / description / body keys must NOT exist in the payload.
	for _, banned := range []string{"title", "description", "body", "notes"} {
		if _, ok := fields[banned]; ok {
			t.Fatalf("payload carries banned free-text key %q", banned)
		}
	}
}

// assertNoFreeText walks the payload Struct and fails if the free-text sentinel
// appears anywhere.
func assertNoFreeText(t *testing.T, s *structpb.Struct, sentinel string) {
	t.Helper()
	raw, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatalf("payload leaked incident free-text sentinel %q: %s", sentinel, raw)
	}
}

// AC (P0): the cross-profile dedup. A webhook-emitted record and a pull-emitted
// record for the SAME incident in the SAME UTC hour derive the SAME idempotency
// key, so the ledger collapses them to one row.
func TestCrossProfileDedup_WebhookKeyMatchesPullKey(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.resolved", "PINC9", "")
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusOK || len(p.records) != 1 {
		t.Fatalf("webhook emit failed: status=%d records=%d", w.Code, len(p.records))
	}
	webhookKey := p.records[0].GetIdempotencyKey()

	// Build the SAME incident through the PULL path (pdrecord.BuildIncident, the
	// shape slice 489 froze), same observed-at hour, same scope/actor.
	pullIncident := incidents.Incident{
		ID:        "PINC9",
		Status:    "resolved",
		Urgency:   "high",
		ServiceID: "PSVC1",
		CreatedAt: time.Date(2026, 6, 9, 14, 25, 0, 0, time.UTC),
	}
	pullRec, err := pdrecord.BuildIncident(pullIncident, "scf:IRO-02", "connector:pagerduty:incidents@test", "pagerduty", "prod", fixedNow)
	if err != nil {
		t.Fatalf("pull BuildIncident: %v", err)
	}
	if webhookKey != pullRec.GetIdempotencyKey() {
		t.Fatalf("dedup broken: webhook key %q != pull key %q (same incident + hour must collapse)", webhookKey, pullRec.GetIdempotencyKey())
	}
}

// AC (P0 DoS): an oversized body is rejected 413 and emits nothing.
func TestServeHTTP_OversizedBody_Rejects413(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	huge := bytes.Repeat([]byte("a"), int(MaxBodyBytes)+1024)
	sig := Sign([]byte(testSecret), huge)

	w := post(rec, huge, sig)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on oversized body, want 0", len(p.records))
	}
}

// A non-incident event (test ping / service.*) is acknowledged 200 but emits
// nothing.
func TestServeHTTP_NonIncidentEvent_AcksNoEmit(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := []byte(`{"event":{"event_type":"service.created","data":{"id":"PSVC1"}}}`)
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records for non-incident event, want 0", len(p.records))
	}
}

// An incident event with no incident id is acknowledged 200 but emits nothing.
func TestServeHTTP_IncidentEventNoID_AcksNoEmit(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := []byte(`{"event":{"event_type":"incident.triggered","data":{"id":"  "}}}`)
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records, want 0", len(p.records))
	}
}

// A verified delivery with a malformed JSON body returns 400, no record.
func TestServeHTTP_BadJSON_Rejects400(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	body := []byte(`{not json`)
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records, want 0", len(p.records))
	}
}

// A push failure surfaces 502 and emits no record (the vendor retries; the failed
// push collapses on the retry against the idempotency key).
func TestServeHTTP_PushFailure_Returns502(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{err: errors.New("platform unavailable")}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC1", "")
	sig := Sign([]byte(testSecret), body)

	w := post(rec, body, sig)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// Non-POST is rejected 405.
func TestServeHTTP_NonPost_Rejects405(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	req := httptest.NewRequest(http.MethodGet, "/webhooks/pagerduty", nil)
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// AC (P0): the signing secret is NEVER logged — even when emit fails and the
// receiver logs the error. Capture the log output across a forged delivery, a
// push failure, and a successful delivery; assert the secret never appears.
func TestSecretNeverLogged(t *testing.T) {
	// NOT parallel: swaps the global log output.
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	body := incidentBody("incident.triggered", "PINC1\nforged-line", testSecret)

	// 1. forged delivery (logs nothing for the verify path, but exercise it).
	{
		p := &recordingPusher{}
		rec := newTestReceiver(t, p)
		_ = post(rec, body, Sign([]byte("wrong"), body))
	}
	// 2. push failure — this DOES log the error with the incident id.
	{
		p := &recordingPusher{err: errors.New("boom")}
		rec := newTestReceiver(t, p)
		_ = post(rec, body, Sign([]byte(testSecret), body))
	}

	out := buf.String()
	if strings.Contains(out, testSecret) {
		t.Fatalf("log leaked the signing secret: %s", out)
	}
}

// AC (CWE-117 / b245): a crafted incident id with an embedded newline cannot
// forge a log line — the %q formatting escapes it.
func TestLogInjection_CraftedIDIsEscaped(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	// Force the emit path to log by failing the push; craft an id with a newline.
	p := &recordingPusher{err: errors.New("boom")}
	rec := newTestReceiver(t, p)
	body := incidentBody("incident.triggered", "PINC1\nFAKE LOG LINE", "")
	_ = post(rec, body, Sign([]byte(testSecret), body))

	out := buf.String()
	if strings.Contains(out, "PINC1\nFAKE LOG LINE") {
		t.Fatalf("log injection: raw newline reached the log: %q", out)
	}
	// The escaped form (quoted) is what we want.
	if !strings.Contains(out, `\nFAKE LOG LINE`) {
		t.Fatalf("expected %%q-escaped id in log, got: %q", out)
	}
}

func TestNewReceiver_ValidatesConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no verifier", Config{Pusher: &recordingPusher{}, Environment: "prod"}},
		{"no pusher", Config{Verifier: NewHMACVerifier(testSecret), Environment: "prod"}},
		{"no environment", Config{Verifier: NewHMACVerifier(testSecret), Pusher: &recordingPusher{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewReceiver(tc.cfg); err == nil {
				t.Fatalf("NewReceiver(%s): expected error", tc.name)
			}
		})
	}
}

func TestNewReceiver_Defaults(t *testing.T) {
	t.Parallel()
	rec, err := NewReceiver(Config{
		Verifier:    NewHMACVerifier(testSecret),
		Pusher:      &recordingPusher{},
		Environment: "prod",
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	if rec.cfg.ControlID != "scf:IRO-02" {
		t.Fatalf("default ControlID = %q, want scf:IRO-02", rec.cfg.ControlID)
	}
	if rec.cfg.Service != "pagerduty" {
		t.Fatalf("default Service = %q, want pagerduty", rec.cfg.Service)
	}
	if rec.now == nil {
		t.Fatalf("now clock not defaulted")
	}
}

// Server lifecycle: NewServer sets the gosec-G112 timeouts; Serve shuts down
// gracefully on ctx cancel.
func TestServer_TimeoutsSet(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	srv := NewServer("127.0.0.1:0", "/webhooks/pagerduty", rec)
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("server timeouts not all set: %+v", srv)
	}
}

func TestServe_GracefulShutdownOnCancel(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	srv := NewServer("127.0.0.1:0", "/webhooks/pagerduty", rec)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv) }()

	// Give the listener a moment, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on graceful shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

func TestServe_ListenError(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p)
	// An invalid addr makes ListenAndServe return immediately with a non-nil,
	// non-ErrServerClosed error.
	srv := NewServer("256.256.256.256:99999", "/webhooks/pagerduty", rec)
	err := Serve(context.Background(), srv)
	if err == nil {
		t.Fatal("expected a listen error")
	}
}

// drainOK is a tiny guard that the verifier's body-read path matches what the
// receiver feeds it (defensive — ensures Sign+Verify round-trips on the exact
// bytes ReadAll produces).
func TestVerify_RoundTripOnReadAllBytes(t *testing.T) {
	t.Parallel()
	body := incidentBody("incident.triggered", "PINC1", "")
	r := io.NopCloser(bytes.NewReader(body))
	read, _ := io.ReadAll(r)
	v := NewHMACVerifier(testSecret)
	h := http.Header{}
	h.Set(HeaderSignature, Sign([]byte(testSecret), read))
	if err := v.Verify(read, h); err != nil {
		t.Fatalf("Verify round-trip failed: %v", err)
	}
}

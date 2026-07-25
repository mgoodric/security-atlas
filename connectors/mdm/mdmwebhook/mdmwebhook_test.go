// Tests for the shared MDM webhook receiver. Both vendor adapters (jamf + intune)
// drive this same core; the per-vendor parser/verifier are stubbed here, and the
// concrete schemes are tested in the vendor cmd packages.
//
// No real Jamf/Intune secrets in fixtures — neutral "test-*" strings only
// (GitGuardian flags vendor-shaped literals even in fixtures).
package mdmwebhook

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/idem"
)

// fixedNow pins the observed-at clock so the idempotency key is deterministic and
// the webhook/pull keys can be compared byte-for-byte (cross-profile dedup).
var fixedNow = time.Date(2026, 6, 9, 14, 37, 0, 0, time.UTC)

const (
	testHeader = "X-Test-Webhook-Secret"
	testSecret = "test-shared-secret-not-a-real-jamf-credential"
)

// recordingPusher captures pushed records so a test can assert shape + that no
// over-collected field reached the payload.
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

// oneDeviceParser returns a single posture-bounded device with the given id.
func oneDeviceParser(id string) PayloadParser {
	return func(_ []byte) ([]devposture.RawDevice, error) {
		return []devposture.RawDevice{{
			DeviceID:              id,
			DeviceName:            "ENG-MBP-014",
			OSVersion:             "14.5",
			Platform:              "macOS",
			DiskEncryptionEnabled: true,
			ScreenLockEnabled:     true,
			Managed:               true,
			Enrolled:              true,
			Compliance:            devposture.ComplianceCompliant,
			OwnerAssignmentID:     "user-123",
			OwnerDisplayName:      "A. Engineer",
		}}, nil
	}
}

func newTestReceiver(t *testing.T, p Pusher, parser PayloadParser) *Receiver {
	t.Helper()
	rec, err := NewReceiver(Config{
		SourceMDM:   devposture.MDMJamf,
		Verifier:    NewSharedSecretVerifier(testHeader, testSecret),
		Parser:      parser,
		Pusher:      p,
		ActorID:     "connector:jamf:devices@test",
		Service:     "jamf",
		Environment: "prod",
		Now:         func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	return rec
}

func post(rec *Receiver, body []byte, header, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/jamf", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set(header, secret)
	}
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, req)
	return w
}

// AC: an authentic (correctly-credentialed) delivery is accepted and emits
// exactly one endpoint.device_posture.v1 record.
func TestServeHTTP_AuthenticDelivery_Accepts(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, testSecret)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 1 {
		t.Fatalf("pushed %d records, want 1", len(p.records))
	}
	if got := p.records[0].GetEvidenceKind(); got != "endpoint.device_posture.v1" {
		t.Fatalf("evidence_kind = %q, want endpoint.device_posture.v1", got)
	}
}

// AC (DOMINANT P0): a delivery missing the credential is rejected 401 BEFORE any
// record is built (the parser/pusher are never reached).
func TestServeHTTP_MissingCredential_Rejects401NoRecord(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	parserCalled := false
	parser := func(b []byte) ([]devposture.RawDevice, error) {
		parserCalled = true
		return oneDeviceParser("DEV1")(b)
	}
	rec := newTestReceiver(t, p, parser)

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, "") // no credential
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if parserCalled {
		t.Error("parser ran on an unauthenticated delivery — verify-first violated")
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on an unauthenticated delivery, want 0", len(p.records))
	}
}

// AC (P0): a forged delivery (wrong credential) is rejected 401 before any record.
func TestServeHTTP_ForgedCredential_Rejects401(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, "test-wrong-secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on a forged delivery, want 0", len(p.records))
	}
}

// AC (P0 cross-profile dedup): a webhook-emitted record and a pull-emitted record
// for the SAME device within the SAME UTC hour derive the SAME idempotency key, so
// the ledger collapses them to one row.
func TestServeHTTP_DedupWithPull_SameIdempotencyKey(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV-DEDUP"))

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, testSecret)
	if w.Code != http.StatusOK || len(p.records) != 1 {
		t.Fatalf("setup: status=%d records=%d", w.Code, len(p.records))
	}
	webhookKey := p.records[0].GetIdempotencyKey()

	// The pull profile derives its key directly from the shared idem package for
	// the same (mdm, device, hour). They MUST be byte-identical.
	pullKey := idem.DevicePostureKey("jamf", "DEV-DEDUP", fixedNow)
	if webhookKey != pullKey {
		t.Fatalf("webhook key %q != pull key %q — cross-profile dedup broken", webhookKey, pullKey)
	}
}

// AC (P0 over-collection guard): no field beyond the slice-490 posture-summary
// set reaches an emitted record. Even if a parser were buggy, the record payload
// keys are constrained to the posture-summary allow-list by construction
// (devposture.RawDevice has no over-collected field). This test pins the payload
// key set so a regression that adds a banned key fails.
func TestServeHTTP_NoFieldBeyondPostureSummary(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, testSecret)
	if w.Code != http.StatusOK || len(p.records) != 1 {
		t.Fatalf("setup: status=%d records=%d", w.Code, len(p.records))
	}
	allowed := map[string]bool{
		"source_mdm": true, "device_id": true, "device_name": true,
		"os_version": true, "platform": true, "disk_encryption_enabled": true,
		"screen_lock_enabled": true, "managed": true, "enrolled": true,
		"compliance_result": true, "owner_assignment_id": true,
		"owner_display_name": true,
	}
	banned := []string{"geolocation", "location", "installed_apps", "apps", "owner_email", "owner_phone", "owner_address", "browsing"}
	fields := p.records[0].GetPayload().GetFields()
	for k := range fields {
		if !allowed[k] {
			t.Errorf("payload carries a field beyond the posture-summary set: %q", k)
		}
	}
	for _, b := range banned {
		if _, ok := fields[b]; ok {
			t.Errorf("payload carries banned over-collection field %q", b)
		}
	}
}

// A malformed body (parser error) is rejected 400, nothing pushed.
func TestServeHTTP_MalformedBody_400(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	parser := func(_ []byte) ([]devposture.RawDevice, error) {
		return nil, errors.New("bad json")
	}
	rec := newTestReceiver(t, p, parser)

	w := post(rec, []byte(`not json`), testHeader, testSecret)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on a malformed body, want 0", len(p.records))
	}
}

// An authentic-but-empty delivery (no posture-bearing device, e.g. a keep-alive)
// acks 200 and emits nothing.
func TestServeHTTP_EmptyDelivery_200NoRecord(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	parser := func(_ []byte) ([]devposture.RawDevice, error) {
		return nil, nil
	}
	rec := newTestReceiver(t, p, parser)

	w := post(rec, []byte(`{"keepalive":true}`), testHeader, testSecret)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on an empty delivery, want 0", len(p.records))
	}
}

// A push failure surfaces as 502 so the source retries.
func TestServeHTTP_PushError_502(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{err: errors.New("push rejected")}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	w := post(rec, []byte(`{"event":"ComputerCheckIn"}`), testHeader, testSecret)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// Non-POST → 405 (the shared skeleton enforces it).
func TestServeHTTP_NonPost_405(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	rec := newTestReceiver(t, p, oneDeviceParser("DEV1"))

	req := httptest.NewRequest(http.MethodGet, "/webhooks/jamf", nil)
	req.Header.Set(testHeader, testSecret)
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// An oversized body → 413 before any record.
func TestServeHTTP_OversizedBody_413(t *testing.T) {
	t.Parallel()
	p := &recordingPusher{}
	cfg := Config{
		SourceMDM:    devposture.MDMJamf,
		Verifier:     NewSharedSecretVerifier(testHeader, testSecret),
		Parser:       oneDeviceParser("DEV1"),
		Pusher:       p,
		ActorID:      "connector:jamf:devices@test",
		Environment:  "prod",
		MaxBodyBytes: 16, // tiny cap for the test
		Now:          func() time.Time { return fixedNow },
	}
	rec, err := NewReceiver(cfg)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	big := bytes.Repeat([]byte("x"), 1024)
	w := post(rec, big, testHeader, testSecret)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if len(p.records) != 0 {
		t.Fatalf("pushed %d records on an oversized body, want 0", len(p.records))
	}
}

func TestNewReceiver_Validation(t *testing.T) {
	t.Parallel()
	base := Config{
		SourceMDM:   devposture.MDMJamf,
		Verifier:    NewSharedSecretVerifier(testHeader, testSecret),
		Parser:      oneDeviceParser("DEV1"),
		Pusher:      &recordingPusher{},
		ActorID:     "connector:jamf:devices@test",
		Environment: "prod",
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"no source", func(c *Config) { c.SourceMDM = "" }},
		{"no verifier", func(c *Config) { c.Verifier = nil }},
		{"no parser", func(c *Config) { c.Parser = nil }},
		{"no pusher", func(c *Config) { c.Pusher = nil }},
		{"no environment", func(c *Config) { c.Environment = "" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.mutate(&cfg)
			if _, err := NewReceiver(cfg); err == nil {
				t.Errorf("NewReceiver(%s) = nil error; want validation error", tc.name)
			}
		})
	}
}

// NewReceiver applies defaults (ControlID, Service, MaxBodyBytes, Now).
func TestNewReceiver_Defaults(t *testing.T) {
	t.Parallel()
	rec, err := NewReceiver(Config{
		SourceMDM:   devposture.MDMIntune,
		Verifier:    NewSharedSecretVerifier(testHeader, testSecret),
		Parser:      oneDeviceParser("DEV1"),
		Pusher:      &recordingPusher{},
		ActorID:     "connector:intune:devices@test",
		Environment: "prod",
	})
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	if rec.cfg.ControlID != "scf:END-04" {
		t.Errorf("default ControlID = %q, want scf:END-04", rec.cfg.ControlID)
	}
	if rec.cfg.Service != "intune" {
		t.Errorf("default Service = %q, want intune", rec.cfg.Service)
	}
	if rec.maxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("default maxBodyBytes = %d, want %d", rec.maxBodyBytes, DefaultMaxBodyBytes)
	}
	if rec.now == nil {
		t.Error("default now clock nil")
	}
}

func TestNewServerAndServe_Lifecycle(t *testing.T) {
	t.Parallel()
	rec := newTestReceiver(t, &recordingPusher{}, oneDeviceParser("DEV1"))
	srv := NewServer("127.0.0.1:0", "/webhooks/jamf", rec)
	if srv.ReadHeaderTimeout == 0 {
		t.Error("NewServer must set ReadHeaderTimeout (gosec G112)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate shutdown
	if err := Serve(ctx, srv); err != nil {
		t.Errorf("Serve on cancelled ctx = %v, want nil", err)
	}
}

// The verify path never logs the secret. A no-token-logged guard: render the
// verifier with %v / %+v and assert the secret is absent.
func TestSharedSecretVerifier_DoesNotLeakSecret(t *testing.T) {
	t.Parallel()
	v := NewSharedSecretVerifier(testHeader, testSecret)
	for _, s := range []string{
		strings.ReplaceAll(strings.TrimSpace(plain("%v", v)), " ", ""),
		strings.ReplaceAll(strings.TrimSpace(plain("%+v", v)), " ", ""),
	} {
		if strings.Contains(s, testSecret) {
			t.Errorf("verifier formatting leaked the secret: %q", s)
		}
	}
}

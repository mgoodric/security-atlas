package githubwebhook_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubwebhook"
)

const testSecret = "super-secret-webhook-key"

func TestVerifySignature_AcceptsMatching(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sig := githubwebhook.Sign([]byte(testSecret), body)
	if err := githubwebhook.VerifySignature([]byte(testSecret), body, sig); err != nil {
		t.Fatalf("VerifySignature on correct sig: %v", err)
	}
}

func TestVerifySignature_RejectsMissing(t *testing.T) {
	if err := githubwebhook.VerifySignature([]byte(testSecret), []byte(`{}`), ""); !errors.Is(err, githubwebhook.ErrMissingSignature) {
		t.Fatalf("err = %v; want ErrMissingSignature", err)
	}
}

func TestVerifySignature_RejectsMalformedPrefix(t *testing.T) {
	if err := githubwebhook.VerifySignature([]byte(testSecret), []byte(`{}`), "sha512=abc"); !errors.Is(err, githubwebhook.ErrMalformedSignature) {
		t.Fatalf("err = %v; want ErrMalformedSignature", err)
	}
}

func TestVerifySignature_RejectsBadHex(t *testing.T) {
	if err := githubwebhook.VerifySignature([]byte(testSecret), []byte(`{}`), "sha256=ZZZZ"); !errors.Is(err, githubwebhook.ErrMalformedSignature) {
		t.Fatalf("err = %v; want ErrMalformedSignature", err)
	}
}

func TestVerifySignature_RejectsTamperedBody(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sig := githubwebhook.Sign([]byte(testSecret), body)
	tampered := []byte(`{"hello":"WORLD"}`)
	if err := githubwebhook.VerifySignature([]byte(testSecret), tampered, sig); !errors.Is(err, githubwebhook.ErrBadSignature) {
		t.Fatalf("err = %v; want ErrBadSignature", err)
	}
}

func TestVerifySignature_RejectsWrongSecret(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sig := githubwebhook.Sign([]byte("attacker-secret"), body)
	if err := githubwebhook.VerifySignature([]byte(testSecret), body, sig); !errors.Is(err, githubwebhook.ErrBadSignature) {
		t.Fatalf("err = %v; want ErrBadSignature", err)
	}
}

// stubPusher captures pushed records for assertions.
type stubPusher struct {
	mu      sync.Mutex
	records []*githubwebhook.AuditEventRecord
	err     error
}

func (s *stubPusher) Push(_ context.Context, r *githubwebhook.AuditEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, r)
	return nil
}

func (s *stubPusher) snapshot() []*githubwebhook.AuditEventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*githubwebhook.AuditEventRecord, len(s.records))
	copy(out, s.records)
	return out
}

func TestNewHandler_RequiresSecret(t *testing.T) {
	_, err := githubwebhook.NewHandler(nil, &stubPusher{}, nil)
	if err == nil {
		t.Fatal("expected error on empty secret")
	}
}

func TestNewHandler_RequiresPusher(t *testing.T) {
	_, err := githubwebhook.NewHandler([]byte("x"), nil, nil)
	if err == nil {
		t.Fatal("expected error on nil pusher")
	}
}

func TestHandler_AcceptsValidDelivery(t *testing.T) {
	push := &stubPusher{}
	h, err := githubwebhook.NewHandler([]byte(testSecret), push, func() time.Time {
		return time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	body := []byte(`{
		"action": "edited",
		"sender": {"login": "matt"},
		"organization": {"login": "example"},
		"repository": {"full_name": "example/web"}
	}`)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req := mustReq(t, srv.URL+"/webhook", body, map[string]string{
		githubwebhook.HeaderSignature: githubwebhook.Sign([]byte(testSecret), body),
		githubwebhook.HeaderEvent:     "repository",
		githubwebhook.HeaderDelivery:  "72d3162e-cc78-11e3-81ab-4c9367dc0958",
	})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d; want 204", res.StatusCode)
	}
	got := push.snapshot()
	if len(got) != 1 {
		t.Fatalf("pushed %d records; want 1", len(got))
	}
	if got[0].IdempotencyKey != "72d3162e-cc78-11e3-81ab-4c9367dc0958" {
		t.Errorf("idempotency_key = %q; must match X-GitHub-Delivery verbatim", got[0].IdempotencyKey)
	}
	if got[0].EventType != "repository" {
		t.Errorf("event_type = %q", got[0].EventType)
	}
	if got[0].Action != "edited" {
		t.Errorf("action = %q", got[0].Action)
	}
	if got[0].Actor != "matt" {
		t.Errorf("actor = %q", got[0].Actor)
	}
	if got[0].Org != "example" {
		t.Errorf("org = %q", got[0].Org)
	}
	if !bytes.Equal(got[0].RawPayload, body) {
		t.Errorf("raw_payload not preserved verbatim")
	}
}

func TestHandler_Rejects401OnMissingSignature(t *testing.T) {
	push := &stubPusher{}
	h, _ := githubwebhook.NewHandler([]byte(testSecret), push, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := []byte(`{}`)
	req := mustReq(t, srv.URL+"/webhook", body, map[string]string{
		githubwebhook.HeaderEvent:    "ping",
		githubwebhook.HeaderDelivery: "abc",
	})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", res.StatusCode)
	}
	if len(push.snapshot()) != 0 {
		t.Fatal("handler pushed despite missing signature — anti-criterion P0 violation")
	}
}

func TestHandler_Rejects401OnBadSignature(t *testing.T) {
	push := &stubPusher{}
	h, _ := githubwebhook.NewHandler([]byte(testSecret), push, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := []byte(`{"organization":{"login":"x"}}`)
	req := mustReq(t, srv.URL+"/webhook", body, map[string]string{
		githubwebhook.HeaderSignature: "sha256=" + strings.Repeat("0", 64),
		githubwebhook.HeaderEvent:     "ping",
		githubwebhook.HeaderDelivery:  "abc",
	})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", res.StatusCode)
	}
}

func TestHandler_RejectsMissingDeliveryHeader(t *testing.T) {
	push := &stubPusher{}
	h, _ := githubwebhook.NewHandler([]byte(testSecret), push, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := []byte(`{}`)
	req := mustReq(t, srv.URL+"/webhook", body, map[string]string{
		githubwebhook.HeaderSignature: githubwebhook.Sign([]byte(testSecret), body),
		githubwebhook.HeaderEvent:     "ping",
	})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.StatusCode)
	}
	if len(push.snapshot()) != 0 {
		t.Fatal("pushed without idempotency_key — anti-criterion P0 violation")
	}
}

func TestHandler_RejectsGET(t *testing.T) {
	push := &stubPusher{}
	h, _ := githubwebhook.NewHandler([]byte(testSecret), push, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	res, err := srv.Client().Get(srv.URL + "/webhook")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", res.StatusCode)
	}
}

func TestHandler_RejectsMissingOrg(t *testing.T) {
	push := &stubPusher{}
	h, _ := githubwebhook.NewHandler([]byte(testSecret), push, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	body := []byte(`{"action":"ping","sender":{"login":"x"}}`)
	req := mustReq(t, srv.URL+"/webhook", body, map[string]string{
		githubwebhook.HeaderSignature: githubwebhook.Sign([]byte(testSecret), body),
		githubwebhook.HeaderEvent:     "ping",
		githubwebhook.HeaderDelivery:  "abc",
	})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.StatusCode)
	}
}

func mustReq(t *testing.T, url string, body []byte, headers map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

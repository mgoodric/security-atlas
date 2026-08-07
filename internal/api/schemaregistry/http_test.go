package schemaregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
)

type stubRegistryHTTPService struct {
	listErr error
	getErr  error
}

func (s stubRegistryHTTPService) List(context.Context, string, int32, int32) ([]RegisteredSchema, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return nil, nil
}

func (s stubRegistryHTTPService) Get(context.Context, string, string, string) (RegisteredSchema, error) {
	if s.getErr != nil {
		return RegisteredSchema{}, s.getErr
	}
	return RegisteredSchema{Kind: "manual.upload.v1", Semver: "1.0.0", SchemaJSON: json.RawMessage(`{}`)}, nil
}

func (s stubRegistryHTTPService) Register(context.Context, RegisterRequest) (RegisteredSchema, error) {
	return RegisteredSchema{}, errors.New("unexpected register call")
}

func (s stubRegistryHTTPService) InvalidateTenant(string) {}

func TestReadHTTP_RegistryUnavailableReturnsCoded503(t *testing.T) {
	leakyErr := fmt.Errorf("%w: SQLSTATE 42501: permission denied for table evidence_kind_schemas by atlas_app at /srv/db/schema.sql", ErrRegistryReadFailed)
	tests := []struct {
		name string
		path string
		svc  stubRegistryHTTPService
	}{
		{
			name: "list",
			path: "/v1/schemas",
			svc:  stubRegistryHTTPService{listErr: leakyErr},
		},
		{
			name: "get",
			path: "/v1/schemas/manual.upload.v1/1.0.0",
			svc:  stubRegistryHTTPService{getErr: leakyErr},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveReadRequest(tt.svc, tt.path)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Retry-After"); got != schemaRegistryUnavailableRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, schemaRegistryUnavailableRetryAfter)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
			}
			if body["code"] != schemaRegistryUnavailableCode {
				t.Fatalf("code = %q, want %q; body=%v", body["code"], schemaRegistryUnavailableCode, body)
			}
			if body["request_id"] != "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("request_id = %q", body["request_id"])
			}
			assertNoInternalRegistryDetail(t, rec.Body.String())
		})
	}
}

func TestReadHTTP_InternalErrorRemainsGeneric500(t *testing.T) {
	err := errors.New("SQLSTATE 42501: permission denied for table evidence_kind_schemas by atlas_app at /srv/db/schema.sql")
	tests := []struct {
		name string
		path string
		svc  stubRegistryHTTPService
	}{
		{
			name: "list",
			path: "/v1/schemas",
			svc:  stubRegistryHTTPService{listErr: err},
		},
		{
			name: "get",
			path: "/v1/schemas/manual.upload.v1/1.0.0",
			svc:  stubRegistryHTTPService{getErr: err},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveReadRequest(tt.svc, tt.path)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v; raw=%s", err, rec.Body.String())
			}
			if body["code"] != "" {
				t.Fatalf("code = %q, want omitted; body=%v", body["code"], body)
			}
			if body["request_id"] == "" {
				t.Fatalf("request_id missing; body=%v", body)
			}
			assertNoInternalRegistryDetail(t, rec.Body.String())
		})
	}
}

func serveReadRequest(svc stubRegistryHTTPService, path string) *httptest.ResponseRecorder {
	h := (&HTTPHandler{svc: svc, defaultLimit: 100, maxLimit: 500}).Routes()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := requestidmw.WithRequestID(req.Context(), "11111111-1111-4111-8111-111111111111")
	ctx = authctx.WithCredential(ctx, credstore.Credential{
		ID:       "key_test",
		TenantID: "22222222-2222-4222-8222-222222222222",
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func assertNoInternalRegistryDetail(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"SQLSTATE",
		"42501",
		"evidence_kind_schemas",
		"atlas_app",
		"/srv/db/schema.sql",
		"permission denied",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q in body %s", forbidden, body)
		}
	}
}

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

// unreachableKeys is an apikeystore over a pool whose server does not
// answer, so every lookup fails with a connection error, never
// ErrUnknownKey. pgxpool dials lazily, so constructing it does not fail.
func unreachableKeys(t *testing.T) *apikeystore.Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://atlas_app@127.0.0.1:1/atlas?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	return apikeystore.NewStore(pool, pool, hasher, 0)
}

// #2849 review: when the key lookup fails for any reason but an unknown
// key, the gate answers 503 and does not pass the request on, so no later
// middleware can authenticate a policy-sync key the gate could not check.
// Requests with no API-key bearer never reach the lookup.
func TestRefusePolicySyncKeys_FailsClosedOnLookupError(t *testing.T) {
	t.Parallel()
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	gate := refusePolicySyncKeys(unreachableKeys(t))(next)

	cases := []struct {
		name, auth string
		want       int
		wantNext   bool
	}{
		{"api key, lookup fails", "Bearer " + bearer.PrefixProd + "abcdef0123456789", http.StatusServiceUnavailable, false},
		{"jwt bearer", "Bearer eyJhbGciOiJFUzI1NiJ9.e30.sig", http.StatusOK, true},
		{"no bearer", "", http.StatusOK, true},
	}
	for _, tc := range cases {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
		if reached != tc.wantNext {
			t.Errorf("%s: next handler reached = %v, want %v", tc.name, reached, tc.wantNext)
		}
	}
}

// #2849 review: authInterceptor is a unary interceptor, and there is no
// stream interceptor, so a streaming RPC would skip every bearer check,
// the policy-sync refusal included. No service registers one today; this
// fails the day one does, so the stream interceptor lands with it.
func TestGRPCServices_HaveNoStreamingMethods(t *testing.T) {
	t.Parallel()
	srv := New(Config{})
	info := srv.GRPC.GetServiceInfo()
	if len(info) == 0 {
		t.Fatal("no gRPC services registered; the check would prove nothing")
	}
	for svc, si := range info {
		for _, m := range si.Methods {
			if m.IsClientStream || m.IsServerStream {
				t.Errorf("%s/%s is a streaming RPC: add a grpc.StreamInterceptor with authInterceptor's checks (bearer, calendar token, policy-sync key) before registering it", svc, m.Name)
			}
		}
	}
}

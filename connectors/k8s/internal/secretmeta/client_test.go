package secretmeta

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dropMarker is an obviously-fake neutral secret value. base64 of it is served
// as the Secret's .data value; the test asserts neither the base64 form nor the
// decoded form ever reaches a record. It is NOT a vendor-shaped literal, so the
// branch-scoped secret scanner does not flag it.
const dropMarker = "test-secret-value-should-be-dropped"

func dropMarkerB64() string { return base64.StdEncoding.EncodeToString([]byte(dropMarker)) }

// newFakeK8sSecrets serves a canned secrets list whose Secrets carry REAL .data
// (base64) + .stringData (plaintext) alongside the metadata. The metadata-only
// test then asserts NONE of those values escape — only type/namespace/name/age/
// key-NAMES survive (the client-boundary half of the AC-5 structural guard).
func newFakeK8sSecrets(t *testing.T) *httptest.Server {
	t.Helper()
	b64 := dropMarkerB64()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/secrets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{
				"metadata":{"name":"web-tls","namespace":"prod","creationTimestamp":"2026-05-10T00:00:00Z",
					"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"data\":\"` + b64 + `\"}"}},
				"type":"kubernetes.io/tls",
				"data":{"tls.crt":"` + b64 + `","tls.key":"` + b64 + `"},
				"stringData":{"extra":"test-stringdata-plaintext-should-be-dropped"}
			},
			{
				"metadata":{"name":"db-pass","namespace":"prod","creationTimestamp":"2026-04-10T00:00:00Z"},
				"type":"Opaque",
				"data":{"password":"` + b64 + `"}
			}
		]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListSecretMeta_ReducesToMetadata(t *testing.T) {
	srv := newFakeK8sSecrets(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListSecretMeta(context.Background())
	if err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("secrets = %d; want 2", len(got))
	}
	// Sorted by (namespace, name): db-pass, web-tls.
	db, tls := got[0], got[1]
	if db.Name != "db-pass" || tls.Name != "web-tls" {
		t.Fatalf("sort wrong: %+v", got)
	}
	if tls.Type != "kubernetes.io/tls" || tls.Namespace != "prod" {
		t.Errorf("tls metadata wrong: %+v", tls)
	}
	if strings.Join(tls.KeyNames, ",") != "tls.crt,tls.key" {
		t.Errorf("tls key names = %v; want [tls.crt tls.key] (map KEYS only)", tls.KeyNames)
	}
	if strings.Join(db.KeyNames, ",") != "password" {
		t.Errorf("db key names = %v; want [password]", db.KeyNames)
	}
}

// TestClient_NoSecretValueReachesRecord is the LOAD-BEARING client-boundary
// proof (AC-5): the Secret objects carry real .data (base64) + .stringData
// (plaintext) + an annotation containing the secret blob. We run the full
// collect -> inventory path and assert NONE of those values — base64, decoded,
// plaintext, or annotation — stringifies into any record-bound field.
func TestClient_NoSecretValueReachesRecord(t *testing.T) {
	srv := newFakeK8sSecrets(t)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	raw, err := c.ListSecretMeta(context.Background())
	if err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}

	b64 := dropMarkerB64()
	for _, r := range raw {
		assertNoValueLeak(t, r, b64, "test-stringdata-plaintext-should-be-dropped")
	}

	inv, err := Collect(context.Background(), staticAPI(raw), fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, i := range inv {
		assertNoValueLeak(t, i, b64, "test-stringdata-plaintext-should-be-dropped")
	}
}

type staticAPI []RawSecret

func (s staticAPI) ListSecretMeta(_ context.Context) ([]RawSecret, error) { return s, nil }

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "secrets") {
			gotAuth = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListSecretMeta(context.Background()); err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}
	if gotAuth != "Bearer test-k8s-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// TestClient_TokenNeverLogged is the AC-7 no-token-log proof: the client holds
// the bearer token (it is sent in the Authorization header) but the token must
// never appear in the RawSecret records the client returns. We assert the token
// string does not stringify into any record-bound field.
func TestClient_TokenNeverLogged(t *testing.T) {
	const token = "test-k8s-secret-bearer-never-log"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"s","namespace":"ns","creationTimestamp":"2026-05-10T00:00:00Z"},"type":"Opaque","data":{"k":"` + dropMarkerB64() + `"}}
		]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, token)
	got, err := c.ListSecretMeta(context.Background())
	if err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}
	for _, r := range got {
		assertNoValueLeak(t, r, dropMarkerB64(), token)
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := c.ListSecretMeta(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestClient_SkipsUnidentifiedSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"","namespace":"ns"}},
			{"metadata":{"name":"ok","namespace":"ns"},"type":"Opaque"}
		]}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListSecretMeta(context.Background())
	if err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("want only the identified secret; got %+v", got)
	}
}

func TestAPIError_String(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 401}).Error() != "k8s: HTTP 401" {
		t.Error("bare status mismatch")
	}
	if !strings.Contains((&APIError{Status: 500, Body: "boom"}).Error(), "boom") {
		t.Error("body should be included")
	}
}

// TestClient_ListSecretMeta_FollowsContinueAcrossPages proves the secrets list
// follows the metadata.continue cursor to completion via the shared k8slist
// reader (slice 621): page 1 carries a continue token, page 2 closes it, and the
// client accumulates Secrets across both pages.
func TestClient_ListSecretMeta_FollowsContinueAcrossPages(t *testing.T) {
	b64 := dropMarkerB64()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/secrets", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("continue") {
		case "":
			_, _ = w.Write([]byte(`{"metadata":{"continue":"PAGE-2"},"items":[
				{"metadata":{"name":"alpha","namespace":"ns"},"type":"Opaque","data":{"k":"` + b64 + `"}}
			]}`))
		case "PAGE-2":
			_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[
				{"metadata":{"name":"bravo","namespace":"ns"},"type":"Opaque","data":{"k":"` + b64 + `"}}
			]}`))
		default:
			t.Errorf("unexpected continue token %q", r.URL.Query().Get("continue"))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client(), srv.URL, "test-k8s-token")
	got, err := c.ListSecretMeta(context.Background())
	if err != nil {
		t.Fatalf("ListSecretMeta: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("secrets = %d; want 2 accumulated across two pages", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Fatalf("expected alpha + bravo across pages; got %+v", got)
	}
}

package workers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func bambooOneServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/employees/") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_GetWorker_DecodesLifecycleFieldsOnly(t *testing.T) {
	t.Parallel()
	body := `{
	  "id": "42",
	  "status": "Inactive",
	  "hireDate": "2024-01-15",
	  "terminationDate": "2026-05-31",
	  "jobTitle": "Software Engineer",
	  "department": "Engineering",
	  "supervisorEid": "9",
	  "workEmail": "a.engineer@corp.example",
	  "payRate": "200000",
	  "ssn": "000-00-0000"
	}`
	srv := bambooOneServer(t, http.StatusOK, body)
	c := NewClient(srv.Client(), srv.URL, "acme", "test-bamboo-secret")
	raw, ok, err := c.GetWorker(context.Background(), "42")
	if err != nil || !ok {
		t.Fatalf("GetWorker: ok=%v err=%v", ok, err)
	}
	if raw.ID != "42" || raw.Status != "Inactive" || raw.TerminationDate != "2026-05-31" {
		t.Errorf("decoded = %+v", raw)
	}
}

func TestClient_GetWorker_FallsBackToRequestedID(t *testing.T) {
	t.Parallel()
	// BambooHR's per-employee response can omit the "id" field; the client falls
	// back to the requested id so the record carries a stable key.
	body := `{"status":"Inactive","terminationDate":"2026-05-31","workEmail":"x@corp.example"}`
	srv := bambooOneServer(t, http.StatusOK, body)
	c := NewClient(srv.Client(), srv.URL, "acme", "test-bamboo-secret")
	raw, ok, err := c.GetWorker(context.Background(), "42")
	if err != nil || !ok {
		t.Fatalf("GetWorker: ok=%v err=%v", ok, err)
	}
	if raw.ID != "42" {
		t.Errorf("id = %q; want fallback 42", raw.ID)
	}
}

func TestClient_GetWorker_NotFoundIsNotError(t *testing.T) {
	t.Parallel()
	srv := bambooOneServer(t, http.StatusNotFound, `not found`)
	c := NewClient(srv.Client(), srv.URL, "acme", "test-bamboo-secret")
	_, ok, err := c.GetWorker(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("404 should not error: %v", err)
	}
	if ok {
		t.Error("ok=true on 404; want false")
	}
}

func TestClient_GetWorker_EmptyIDShortCircuits(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://api.bamboohr.example", "acme", "test-bamboo-secret")
	_, ok, err := c.GetWorker(context.Background(), "")
	if err != nil || ok {
		t.Errorf("empty id: ok=%v err=%v; want false,nil", ok, err)
	}
}

func TestClient_GetWorker_ServerError(t *testing.T) {
	t.Parallel()
	srv := bambooOneServer(t, http.StatusInternalServerError, `boom`)
	c := NewClient(srv.Client(), srv.URL, "acme", "test-bamboo-secret")
	_, _, err := c.GetWorker(context.Background(), "42")
	if err == nil {
		t.Fatal("want error on 500")
	}
}

type fakeOneAPI struct {
	raw RawWorker
	ok  bool
	err error
}

func (f fakeOneAPI) GetWorker(_ context.Context, _ string) (RawWorker, bool, error) {
	return f.raw, f.ok, f.err
}

func TestFetchOne_MapsTerminatedLeaver(t *testing.T) {
	t.Parallel()
	api := fakeOneAPI{raw: RawWorker{
		ID: "42", Status: "Inactive", TerminationDate: "2026-05-31", WorkEmail: "x@corp.example",
	}, ok: true}
	w, ok, err := FetchOne(context.Background(), api, "42")
	if err != nil || !ok {
		t.Fatalf("FetchOne: ok=%v err=%v", ok, err)
	}
	// Inactive + termination date => terminated leaver.
	if w.Status != worker.StatusTerminated {
		t.Errorf("status = %q; want terminated", w.Status)
	}
}

func TestFetchOne_NilAPI(t *testing.T) {
	t.Parallel()
	if _, _, err := FetchOne(context.Background(), nil, "42"); err == nil {
		t.Fatal("want error on nil OneAPI")
	}
}

func TestFetchOne_NotOk(t *testing.T) {
	t.Parallel()
	_, ok, err := FetchOne(context.Background(), fakeOneAPI{ok: false}, "42")
	if err != nil || ok {
		t.Errorf("ok=%v err=%v; want false,nil", ok, err)
	}
}

func TestFetchOne_PropagatesError(t *testing.T) {
	t.Parallel()
	_, _, err := FetchOne(context.Background(), fakeOneAPI{err: http.ErrServerClosed}, "42")
	if err == nil {
		t.Fatal("want wrapped error")
	}
}

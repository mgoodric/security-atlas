package workers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// ripplingOneServer stands up a fake Rippling single-employee endpoint. NO live
// Rippling.
func ripplingOneServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/platform/api/employees/") {
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
	// The single-employee payload carries banned fields the client MUST NOT decode.
	body := `{
	  "id": "emp-1",
	  "employmentStatus": "TERMINATED",
	  "startDate": "2024-01-15",
	  "endDate": "2026-05-31",
	  "title": "Software Engineer",
	  "department": "Engineering",
	  "manager": "mgr-9",
	  "workEmail": "a.engineer@corp.example",
	  "compensation": {"annualSalary": 200000},
	  "ssn": "000-00-0000",
	  "homeAddress": "1 Fixture St"
	}`
	srv := ripplingOneServer(t, http.StatusOK, body)
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
	raw, ok, err := c.GetWorker(context.Background(), "emp-1")
	if err != nil || !ok {
		t.Fatalf("GetWorker: ok=%v err=%v", ok, err)
	}
	if raw.ID != "emp-1" || raw.EmploymentStatus != "TERMINATED" || raw.WorkEmail != "a.engineer@corp.example" {
		t.Errorf("decoded = %+v", raw)
	}
}

func TestClient_GetWorker_NotFoundIsNotError(t *testing.T) {
	t.Parallel()
	srv := ripplingOneServer(t, http.StatusNotFound, `{"error":"not found"}`)
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
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
	c := NewClient(nil, "https://api.rippling.example", "test-rippling-key")
	_, ok, err := c.GetWorker(context.Background(), "   ")
	if err != nil || ok {
		t.Errorf("empty id: ok=%v err=%v; want false,nil", ok, err)
	}
}

func TestClient_GetWorker_ServerError(t *testing.T) {
	t.Parallel()
	srv := ripplingOneServer(t, http.StatusInternalServerError, `{"error":"boom"}`)
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
	_, _, err := c.GetWorker(context.Background(), "emp-1")
	if err == nil {
		t.Fatal("want error on 500")
	}
}

// fakeOneAPI drives FetchOne without HTTP.
type fakeOneAPI struct {
	raw RawWorker
	ok  bool
	err error
}

func (f fakeOneAPI) GetWorker(_ context.Context, _ string) (RawWorker, bool, error) {
	return f.raw, f.ok, f.err
}

func TestFetchOne_MapsToWorkerRawWorker(t *testing.T) {
	t.Parallel()
	api := fakeOneAPI{raw: RawWorker{
		ID: "w1", EmploymentStatus: "TERMINATED", EndDate: "2026-05-31",
		Title: "SWE", Department: "Eng", ManagerAssignmentID: "mgr-9", WorkEmail: "x@corp.example",
	}, ok: true}
	w, ok, err := FetchOne(context.Background(), api, "w1")
	if err != nil || !ok {
		t.Fatalf("FetchOne: ok=%v err=%v", ok, err)
	}
	if w.WorkerID != "w1" || w.Status != worker.StatusTerminated {
		t.Errorf("mapped = %+v", w)
	}
	if w.EndDate.IsZero() {
		t.Error("EndDate not parsed")
	}
}

func TestFetchOne_NilAPI(t *testing.T) {
	t.Parallel()
	if _, _, err := FetchOne(context.Background(), nil, "w1"); err == nil {
		t.Fatal("want error on nil OneAPI")
	}
}

func TestFetchOne_NotOk(t *testing.T) {
	t.Parallel()
	_, ok, err := FetchOne(context.Background(), fakeOneAPI{ok: false}, "w1")
	if err != nil || ok {
		t.Errorf("ok=%v err=%v; want false,nil", ok, err)
	}
}

func TestFetchOne_PropagatesError(t *testing.T) {
	t.Parallel()
	_, _, err := FetchOne(context.Background(), fakeOneAPI{err: http.ErrServerClosed}, "w1")
	if err == nil {
		t.Fatal("want wrapped error")
	}
}

package workers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ripplingTestServer stands up a fake Rippling that handles the employee
// directory read. NO live Rippling.
func ripplingTestServer(t *testing.T, body string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/platform/api/employees") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		log.authHeader = r.Header.Get("Authorization")
		log.rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

type requestLog struct {
	authHeader string
	rawQuery   string
}

func TestClient_ListWorkers_DecodesLifecycleFields(t *testing.T) {
	t.Parallel()
	// The directory payload deliberately includes fields the client MUST NOT
	// decode (compensation, ssn, homeAddress, bankAccount, performanceRating).
	body := `{
	  "results": [
	    {
	      "id": "emp-1",
	      "employmentStatus": "ACTIVE",
	      "startDate": "2024-01-15",
	      "endDate": "",
	      "title": "Software Engineer",
	      "department": "Engineering",
	      "manager": "mgr-9",
	      "workEmail": "a.engineer@corp.example",
	      "compensation": {"annualSalary": 200000},
	      "ssn": "000-00-0000",
	      "homeAddress": "1 Fixture St",
	      "bankAccount": "fixture-acct",
	      "performanceRating": "exceeds"
	    }
	  ]
	}`
	srv, log := ripplingTestServer(t, body)
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
	got, err := c.ListWorkers(context.Background())
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if log.authHeader != "Bearer test-rippling-key" {
		t.Errorf("auth header = %q", log.authHeader)
	}
	// The fields query must NOT request any sensitive PII field, and MUST request
	// the lifecycle fields (over-collection guard P0-491-3).
	lower := strings.ToLower(log.rawQuery)
	for _, banned := range []string{"compensation", "salary", "ssn", "bank", "address", "benefit", "performance", "dob", "birth"} {
		if strings.Contains(lower, banned) {
			t.Errorf("fields query requested banned field %q (P0-491-3): %q", banned, log.rawQuery)
		}
	}
	if !strings.Contains(log.rawQuery, "employmentStatus") {
		t.Errorf("fields query missing employmentStatus: %q", log.rawQuery)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	w := got[0]
	if w.ID != "emp-1" || w.EmploymentStatus != "ACTIVE" || w.Title != "Software Engineer" || w.Department != "Engineering" {
		t.Errorf("lifecycle decode wrong: %+v", w)
	}
	if w.ManagerAssignmentID != "mgr-9" || w.WorkEmail != "a.engineer@corp.example" {
		t.Errorf("manager/email decode wrong: %+v", w)
	}
	// RawWorker has no field for ssn/compensation/address/bank/performance, so
	// they could not have been decoded — the struct shape is the guard.
}

func TestClient_ListWorkers_DropsMissingID(t *testing.T) {
	t.Parallel()
	body := `{"results":[{"id":"","employmentStatus":"ACTIVE"},{"id":"keep","employmentStatus":"ACTIVE"}]}`
	srv, _ := ripplingTestServer(t, body)
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
	got, err := c.ListWorkers(context.Background())
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("got %+v; want only [keep]", got)
	}
}

func TestClient_ListWorkers_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "test-rippling-key")
	_, err := c.ListWorkers(context.Background())
	if err == nil {
		t.Fatal("want HTTP error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Errorf("want APIError 403; got %v", err)
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://api.rippling.com", "test-rippling-key")
	if c.HTTP == nil {
		t.Error("default HTTP client not set")
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	if (&APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if !strings.Contains((&APIError{Status: 503, Body: "boom"}).Error(), "boom") {
		t.Error("error should include body")
	}
}

func asAPIError(err error, target **APIError) bool {
	if e, ok := err.(*APIError); ok {
		*target = e
		return true
	}
	return false
}

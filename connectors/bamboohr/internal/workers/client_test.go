package workers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bambooTestServer stands up a fake BambooHR that handles the custom-report read.
// NO live BambooHR.
func bambooTestServer(t *testing.T, body string) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/reports/custom") {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		u, p, ok := r.BasicAuth()
		log.basicUser, log.basicPass, log.basicOK = u, p, ok
		log.rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

type requestLog struct {
	basicUser string
	basicPass string
	basicOK   bool
	rawQuery  string
}

func TestClient_ListWorkers_DecodesLifecycleFields(t *testing.T) {
	t.Parallel()
	// The report payload deliberately includes fields the client MUST NOT decode
	// (payRate, ssn, homeEmail, address1).
	body := `{
	  "employees": [
	    {
	      "id": "42",
	      "status": "Active",
	      "hireDate": "2024-01-15",
	      "terminationDate": "0000-00-00",
	      "jobTitle": "Software Engineer",
	      "department": "Engineering",
	      "supervisorEid": "7",
	      "workEmail": "a.engineer@corp.example",
	      "payRate": "200000 USD",
	      "ssn": "000-00-0000",
	      "homeEmail": "secret@home.invalid",
	      "address1": "1 Fixture St"
	    }
	  ]
	}`
	srv, log := bambooTestServer(t, body)
	c := NewClient(srv.Client(), srv.URL, "acme", "fake-bamboo-secret")
	got, err := c.ListWorkers(context.Background())
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if !log.basicOK || log.basicUser != "fake-bamboo-secret" {
		t.Errorf("basic auth user = %q ok=%v; want the API key as username", log.basicUser, log.basicOK)
	}
	// The fields query must NOT request any sensitive PII field, and MUST request
	// the lifecycle fields (over-collection guard P0-491-3).
	lower := strings.ToLower(log.rawQuery)
	for _, banned := range []string{"payrate", "compensation", "salary", "ssn", "bank", "homeemail", "address", "benefit", "performance", "dob", "birthdate"} {
		if strings.Contains(lower, banned) {
			t.Errorf("fields query requested banned field %q (P0-491-3): %q", banned, log.rawQuery)
		}
	}
	if !strings.Contains(log.rawQuery, "status") || !strings.Contains(log.rawQuery, "terminationDate") {
		t.Errorf("fields query missing lifecycle fields: %q", log.rawQuery)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	w := got[0]
	if w.ID != "42" || w.Status != "Active" || w.JobTitle != "Software Engineer" || w.Department != "Engineering" {
		t.Errorf("lifecycle decode wrong: %+v", w)
	}
	if w.ManagerAssignmentID != "7" || w.WorkEmail != "a.engineer@corp.example" {
		t.Errorf("manager/email decode wrong: %+v", w)
	}
	// RawWorker has no field for ssn/payRate/homeEmail/address, so they could not
	// have been decoded — the struct shape is the guard.
}

func TestClient_ListWorkers_DropsMissingID(t *testing.T) {
	t.Parallel()
	body := `{"employees":[{"id":"","status":"Active"},{"id":"keep","status":"Active"}]}`
	srv, _ := bambooTestServer(t, body)
	c := NewClient(srv.Client(), srv.URL, "acme", "fake-bamboo-secret")
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
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "acme", "fake-bamboo-secret")
	_, err := c.ListWorkers(context.Background())
	if err == nil {
		t.Fatal("want HTTP error")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("want APIError 401; got %v", err)
	}
}

func TestClient_DefaultHTTPClient(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "https://api.bamboohr.com", "acme", "fake-bamboo-secret")
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

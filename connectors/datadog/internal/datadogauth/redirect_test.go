package datadogauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCheckRedirect(t *testing.T) {
	t.Parallel()
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	orig := []*http.Request{req("https://api.datadoghq.com/api/v1/query")}
	for _, ok := range []string{"https://api.datadoghq.com/api/v1/other", "https://API.datadoghq.com/x"} {
		if err := CheckRedirect(req(ok), orig); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"https://evil.example/api/v1/query",
		"http://api.datadoghq.com/api/v1/query",
		"https://api.datadoghq.com:8443/api/v1/query",
		"https://api.datadoghq.eu/api/v1/query",
	} {
		if err := CheckRedirect(req(bad), orig); !errors.Is(err, ErrUnsafeRedirect) {
			t.Errorf("%s: err = %v, want ErrUnsafeRedirect", bad, err)
		}
	}
	many := make([]*http.Request, 10)
	for i := range many {
		many[i] = orig[0]
	}
	if err := CheckRedirect(req("https://api.datadoghq.com/x"), many); err == nil {
		t.Error("11th redirect: want an error")
	}
}

// TestCheckRedirect_KeysNeverReachAnotherHost drives a real client: the
// origin redirects to a second server, and the second server must never be
// called with (or without) the keys.
func TestCheckRedirect_KeysNeverReachAnotherHost(t *testing.T) {
	t.Parallel()
	reached := make(chan string, 1)
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached <- r.Header.Get("DD-API-KEY")
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	hc := &http.Client{CheckRedirect: CheckRedirect}
	r, _ := http.NewRequest(http.MethodGet, origin.URL+"/api/v1/query", nil)
	r.Header.Set("DD-API-KEY", "test-datadog-api-key")
	res, err := hc.Do(r)
	if res != nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, ErrUnsafeRedirect) {
		t.Errorf("err = %v, want ErrUnsafeRedirect", err)
	}
	select {
	case k := <-reached:
		t.Errorf("redirect target was called (DD-API-KEY %q)", k)
	default:
	}
}

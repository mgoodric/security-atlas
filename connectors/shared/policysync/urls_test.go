package policysync

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func TestCheckBaseURL(t *testing.T) {
	t.Parallel()
	for raw, ok := range map[string]bool{
		"https://contoso.sharepoint.com/sites/Policies": true,
		"https://atlas.example.com/":                    true,
		"http://127.0.0.1:8080":                         true,
		"http://localhost:8080":                         true,
		"http://[::1]:8080":                             true,
		"http://atlas.example.com":                      false,
		"ftp://atlas.example.com":                       false,
		"atlas.example.com":                             false,
		"https://u:p@atlas.example.com":                 false,
		"https://atlas.example.com/#x":                  false,
		"https://atlas.example.com/?a=b":                false,
		"https://atlas.example.com/%zz":                 false,
	} {
		_, err := CheckBaseURL("--x", raw)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestCheckRedirect(t *testing.T) {
	t.Parallel()
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	orig := []*http.Request{req("https://graph.microsoft.com/v1.0/sites")}
	if err := CheckRedirect(req("https://GRAPH.microsoft.com/v1.0/x"), orig); err != nil {
		t.Errorf("same origin: %v", err)
	}
	for _, bad := range []string{
		"https://evil.example/v1.0",
		"http://graph.microsoft.com/v1.0",
		"https://graph.microsoft.com:8443/v1.0",
	} {
		if err := CheckRedirect(req(bad), orig); !errors.Is(err, ErrUnsafeRedirect) {
			t.Errorf("%s: err = %v, want ErrUnsafeRedirect", bad, err)
		}
	}
	many := make([]*http.Request, 10)
	for i := range many {
		many[i] = orig[0]
	}
	if err := CheckRedirect(req("https://graph.microsoft.com/x"), many); err == nil {
		t.Error("11th redirect: want an error")
	}
}

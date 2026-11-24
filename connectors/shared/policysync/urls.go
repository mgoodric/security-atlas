package policysync

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CheckBaseURL parses a base URL a request carrying a secret is sent to:
// the platform's, or a source site's. It must be https, or http on a
// loopback host (a local platform, or a test server), so a credential is
// never sent in the clear across a network.
func CheckBaseURL(flag, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL, got %q", flag, raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("%s must not carry a query, fragment or user info", flag)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("%s must be https (http is accepted for loopback hosts only)", flag)
		}
	default:
		return nil, fmt.Errorf("%s must be https, got scheme %q", flag, u.Scheme)
	}
	return u, nil
}

// ErrUnsafeRedirect is returned for a redirect that changes host or
// leaves https.
var ErrUnsafeRedirect = errors.New("refused redirect")

// CheckRedirect is an http.Client CheckRedirect that refuses a redirect
// to another host (scheme and port included) or from https to http, so a
// request carrying a credential is only ever followed on its own origin.
// It also stops after 10 redirects, as the default does.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	orig := via[0].URL
	if !strings.EqualFold(req.URL.Host, orig.Host) {
		return fmt.Errorf("%w: from host %q to %q", ErrUnsafeRedirect, orig.Host, req.URL.Host)
	}
	if req.URL.Scheme != orig.Scheme {
		return fmt.Errorf("%w: from %s to %s", ErrUnsafeRedirect, orig.Scheme, req.URL.Scheme)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

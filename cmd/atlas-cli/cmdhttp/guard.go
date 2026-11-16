package cmdhttp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// ErrInsecureHTTP is returned, before anything is sent, for a request to a
// plain http:// URL whose host is not loopback.
var ErrInsecureHTTP = errors.New("refusing plain http:// to a non-loopback host")

// allowInsecureHTTP is the process-wide opt-out set by the root command's
// --allow-insecure-http flag. The CLI is one short-lived process per
// invocation, so a package-level switch matches how the flag is scoped.
var allowInsecureHTTP atomic.Bool

// SetAllowInsecureHTTP sets whether Guard lets plain http:// through to a
// non-loopback host. Only the root command's --allow-insecure-http calls it.
func SetAllowInsecureHTTP(allow bool) { allowInsecureHTTP.Store(allow) }

// Guard wraps next (http.DefaultTransport when nil) so that every request it
// carries is checked before it is sent: a plain http:// URL must name a
// loopback host (sdk.IsLoopbackURL: localhost, 127.0.0.0/8, ::1), or the
// request is refused with ErrInsecureHTTP and never reaches the network.
// The bearer every atlas-cli REST call carries, and the token the device
// flow returns, would otherwise cross the wire in the clear (#2257).
//
// The check sits on the transport rather than at each call site, so it sees
// every hop: http.Client sends each redirect through the transport, which
// makes a redirect that downgrades https:// to a non-loopback http:// fail
// the same way.
func Guard(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &guard{next: next}
}

type guard struct {
	next http.RoundTripper
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Scheme, "http") && !allowInsecureHTTP.Load() && !sdk.IsLoopbackURL(req.URL.String()) {
		if req.Body != nil {
			// RoundTrip must close the body, even on error.
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%w %q: use https://, a loopback endpoint, or --allow-insecure-http", ErrInsecureHTTP, req.URL.Host)
	}
	return g.next.RoundTrip(req)
}

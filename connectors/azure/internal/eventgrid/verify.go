package eventgrid

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// errBadCredential is returned when a delivery's credential is absent or does not
// match. It is the shared webhookrecv.ErrBadSignature sentinel so the receiver and
// the skeleton agree; the skeleton maps any non-nil Verify error to a bare 401 (no
// detail leak).
var errBadCredential = webhookrecv.ErrBadSignature

// CredentialLocation selects where the operator-configured Event-Grid delivery key
// is carried on each delivery (D1). Event Grid replays a static credential the
// operator sets on the subscription, either in the delivery URL (a query param) or
// in a header (an Entra bearer or a custom shared-secret header).
type CredentialLocation int

const (
	// CredentialHeader reads the credential from a request header (default
	// "Authorization").
	CredentialHeader CredentialLocation = iota
	// CredentialQuery reads the credential from a URL query parameter (default
	// "code").
	CredentialQuery
)

// DeliveryKeyVerifier authenticates each real Event Grid delivery by a constant-
// time compare of a configured shared secret against the value carried in a
// configured location (a header or a query param). Event Grid, like Jamf (slice
// 557), does NOT HMAC-sign the event body — the operator configures a static
// credential the source replays verbatim. We hold the configured credential and
// compare it constant-time.
//
// The secret is held unexported so a stray %v cannot leak it. A missing or
// mismatched credential returns webhookrecv.ErrBadSignature so the shared skeleton
// rejects with 401 BEFORE any record is built (verify-FIRST).
type DeliveryKeyVerifier struct {
	location CredentialLocation
	name     string // header name or query-param name
	secret   []byte
}

// NewDeliveryKeyVerifier builds a DeliveryKeyVerifier. location selects header vs
// query; name is the header/param carrying the credential; secret is the exact
// value to require. The secret is read from the connector environment, never a
// flag, never logged.
func NewDeliveryKeyVerifier(location CredentialLocation, name, secret string) *DeliveryKeyVerifier {
	return &DeliveryKeyVerifier{location: location, name: name, secret: []byte(secret)}
}

// Verify returns nil iff the configured location carries exactly the configured
// secret (constant-time compare). body is ignored — Event Grid does not sign the
// body; the credential lives entirely in the header or query param. A missing or
// mismatched credential returns webhookrecv.ErrBadSignature so the skeleton rejects
// with 401 BEFORE any record is built.
//
// The webhookrecv.Verifier interface passes only (body, header). To read a query
// param the verifier needs the request URL, so the receiver supplies it via a
// per-request VerifierFor wrapper (see receiver.go) that binds the request's query
// values. For the header location the header set is sufficient.
func (v *DeliveryKeyVerifier) Verify(_ []byte, header http.Header) error {
	if v.location != CredentialHeader {
		// A query-located credential cannot be verified from the header set alone;
		// the receiver routes query verification through verifyValue. Reaching here
		// with a non-header location is a wiring error — fail closed.
		return errBadCredential
	}
	return v.verifyValue(strings.TrimSpace(header.Get(v.name)))
}

// verifyValue constant-time compares a supplied credential against the configured
// secret. Exported-to-package so the receiver can call it for the query-param
// location (where the value comes from the URL, not the header set).
func (v *DeliveryKeyVerifier) verifyValue(got string) error {
	if subtle.ConstantTimeCompare([]byte(got), v.secret) == 1 {
		return nil
	}
	return errBadCredential
}

// Location reports the configured credential location (the receiver uses it to
// decide whether to read the value from the header set or the request URL).
func (v *DeliveryKeyVerifier) Location() CredentialLocation { return v.location }

// QueryName reports the configured query-param name (used only for the query
// location).
func (v *DeliveryKeyVerifier) QueryName() string { return v.name }

// ensure DeliveryKeyVerifier satisfies the shared one-method Verifier at compile
// time.
var _ webhookrecv.Verifier = (*DeliveryKeyVerifier)(nil)

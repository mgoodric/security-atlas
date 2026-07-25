package notify

import "strings"

// Secret wraps a credential string (Slack bot token, webhook bearer,
// webhook HMAC signing secret) so it can NEVER be accidentally logged
// (P0-543-5 / threat-model S — the 445 D9 analog generalized).
//
// Secret satisfies fmt.Stringer, fmt.GoStringer, and
// encoding.TextMarshaler so that %s, %v, %#v, %q, json.Marshal, and a
// structured-logger field all render the redaction placeholder, never the
// value. The plaintext is reachable ONLY through Reveal(), which the
// transport calls at the moment of use and never logs.
type Secret string

// redactedPlaceholder is what every formatting/serialization path renders.
const redactedPlaceholder = "<redacted>"

// String implements fmt.Stringer — %s / %v render the placeholder.
func (s Secret) String() string { return redactedPlaceholder }

// GoString implements fmt.GoStringer — %#v renders the placeholder (so a
// struct dump with %+v / %#v cannot leak the value).
func (s Secret) GoString() string { return redactedPlaceholder }

// MarshalText implements encoding.TextMarshaler — json.Marshal and any
// text encoder render the placeholder, never the value.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redactedPlaceholder), nil }

// Reveal returns the plaintext secret for use at the transport boundary
// (setting an Authorization header, computing an HMAC). Callers MUST NOT
// log the result.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is empty (channel inert / not
// configured).
func (s Secret) IsZero() bool { return s == "" }

// ScrubSecret removes any occurrence of a secret's plaintext from a string
// destined for a log or a persisted error, replacing it with the redaction
// placeholder. This is defense-in-depth for the case where a third-party
// transport error echoes a header value back: even if a Secret's plaintext
// somehow reached an error string, ScrubSecret strips it before it is
// stored/logged.
func ScrubSecret(s string, secrets ...Secret) string {
	for _, sec := range secrets {
		if sec.IsZero() {
			continue
		}
		s = strings.ReplaceAll(s, sec.Reveal(), redactedPlaceholder)
	}
	return s
}

// Package identity holds the one rule for reading a users(id) out of a
// principal string (#2183). It has no dependencies inside the repo, so both
// the API layer (credstore.CallerUserID) and lower layers (the auditor
// attrs resolver) can share it instead of each keeping a copy.
package identity

import (
	"strings"

	"github.com/google/uuid"
)

// UserID returns the users(id) named by a principal string: the
// "user:<uuid>" subject a local-login / PKCE / device-grant JWT carries, or a
// bare uuid. Anything else names no user: an API key id ("key_..."),
// "bootstrap-admin", a machine token ("oauth_client:<id>"), an email, the nil
// uuid.
//
// A string alone cannot tell an unbound key whose id happens to be
// uuid-shaped from a user. For a live credential use credstore.CallerUserID,
// which rules that case out first; call this directly only for strings
// already recorded as a user id.
func UserID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(strings.TrimPrefix(s, "user:"))
	if err != nil || u == uuid.Nil {
		return uuid.Nil, false
	}
	return u, true
}

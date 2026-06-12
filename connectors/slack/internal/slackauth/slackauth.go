// Package slackauth handles Slack workspace authentication + identity
// resolution for the connector. The connector authenticates TO Slack with a
// least-privilege read-only OAuth token (slice 443 threat-model E) and
// resolves the workspace id used as the `tenant_workspace` scope dimension.
//
// The Slack token is source-side (invariant #3) and NEVER logged or
// transmitted to the platform (slice 443 threat-model I / P0-443-4). It is
// carried only on the Authorization header of outbound Slack API calls. This
// package deliberately exposes no Stringer / Format method that could leak
// the token into a log line.
package slackauth

import (
	"context"
	"errors"
	"fmt"
)

// RequiredScopes is the minimal read-only OAuth scope set the connector
// needs — documented as the minimum (slice 443 AC-5 / threat-model E). It
// deliberately EXCLUDES every message-read scope (`channels:history`,
// `groups:history`, `im:history`, `mpim:history`, `search:read`): the
// connector must never be able to read message content.
var RequiredScopes = []string{
	"admin.users:read",         // workspace member roster (access evidence)
	"auditlogs:read",           // admin audit-log entries (admin-action evidence)
	"admin.conversations:read", // retention-settings posture (data-retention)
	"admin.teams:read",         // resolve the workspace/team id for scoping
}

// BannedScopes are message-read scopes the connector must never request. A
// run that is handed a token carrying any of these is misconfigured — the
// connector cannot enforce Slack's grant, but it documents and asserts the
// boundary (slice 443 P0-443-3).
var BannedScopes = []string{
	"channels:history",
	"groups:history",
	"im:history",
	"mpim:history",
	"search:read",
}

// Token wraps the Slack OAuth bearer token. It is an opaque value: it has no
// String() method on purpose, so a stray `%v`/`%s` on a Token in a log line
// prints the struct tag, not the secret. Read the raw value only via Value()
// at the single Slack-API call site.
type Token struct {
	raw string
}

// NewToken constructs a Token from the raw OAuth value. Returns an error on
// the empty string so the connector fails loudly rather than calling Slack
// unauthenticated.
func NewToken(raw string) (Token, error) {
	if raw == "" {
		return Token{}, errors.New("slackauth: a Slack OAuth token is required (set --slack-token or SLACK_TOKEN)")
	}
	return Token{raw: raw}, nil
}

// Value returns the raw token. Call this ONLY at the Slack-API Authorization
// header. Never pass the result to a logger.
func (t Token) Value() string { return t.raw }

// String implements fmt.Stringer to guarantee the secret never leaks through
// a `%v`/`%s` verb anywhere in the connector or its dependencies.
func (t Token) String() string { return "Token(***redacted***)" }

// GoString implements fmt.GoStringer so `%#v` (used by some loggers and by
// testify dumps) is also redacted.
func (t Token) GoString() string { return "slackauth.Token{raw:\"***redacted***\"}" }

// Identity is the resolved Slack workspace context for one run.
type Identity struct {
	TeamID   string // Slack workspace/team id (e.g. "T012AB3CD") — the scope value
	TeamName string // human-readable workspace name (NOT used for scoping)
}

// IdentityAPI is the narrow surface this package needs to resolve the
// workspace id. The concrete Slack admin/teams client satisfies it; tests
// pass a fake.
type IdentityAPI interface {
	ResolveTeam(ctx context.Context) (Identity, error)
}

// Resolve returns the workspace identity for scoping. Fails loudly when the
// team id cannot be resolved so the connector never emits un-scoped records
// (mirrors awsauth.ResolveIdentity).
func Resolve(ctx context.Context, api IdentityAPI) (Identity, error) {
	id, err := api.ResolveTeam(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("slackauth: resolve workspace: %w", err)
	}
	if id.TeamID == "" {
		return Identity{}, errors.New("slackauth: Slack returned an empty team id — cannot scope evidence")
	}
	return id, nil
}

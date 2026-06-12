package slackauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	t.Parallel()
	if _, err := NewToken(""); err == nil {
		t.Error("empty token should error")
	}
	tok, err := NewToken("xoxb-secret-value-123")
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if tok.Value() != "xoxb-secret-value-123" {
		t.Errorf("Value() = %q; want raw token back", tok.Value())
	}
}

// TestTokenNeverLogged is the load-bearing secret-redaction guard (slice 443
// AC-12 / P0-443-4 / threat-model I). Every fmt verb a logger might use on a
// Token must redact the secret. Only Value() may return it.
func TestTokenNeverLogged(t *testing.T) {
	t.Parallel()
	const secret = "xoxb-super-secret-deadbeef"
	tok, err := NewToken(secret)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		out := fmt.Sprintf(verb, tok)
		if strings.Contains(out, secret) {
			t.Errorf("fmt %q leaked the token: %q", verb, out)
		}
		if !strings.Contains(strings.ToLower(out), "redact") {
			t.Errorf("fmt %q = %q; expected a redaction marker", verb, out)
		}
	}
	// A Token embedded in a struct (a common accidental-log shape) must also
	// stay redacted under %v / %+v.
	wrapper := struct{ T Token }{T: tok}
	for _, verb := range []string{"%v", "%+v"} {
		if strings.Contains(fmt.Sprintf(verb, wrapper), secret) {
			t.Errorf("wrapper fmt %q leaked the token", verb)
		}
	}
}

// TestScopeDisciplineExcludesMessageReads asserts the documented minimal
// scope set never includes a message-read scope (slice 443 threat-model E /
// P0-443-3). The connector must not be able to read message content.
func TestScopeDisciplineExcludesMessageReads(t *testing.T) {
	t.Parallel()
	for _, banned := range BannedScopes {
		for _, req := range RequiredScopes {
			if req == banned {
				t.Errorf("RequiredScopes includes banned message-read scope %q", banned)
			}
		}
	}
	// Every required scope is read-only (`:read` suffixed) — no write/admin
	// mutation scope sneaks in.
	for _, req := range RequiredScopes {
		if !strings.HasSuffix(req, ":read") {
			t.Errorf("required scope %q is not read-only (missing :read suffix)", req)
		}
	}
	// The known message-history scopes are explicitly named in BannedScopes.
	for _, want := range []string{"channels:history", "im:history", "search:read"} {
		found := false
		for _, b := range BannedScopes {
			if b == want {
				found = true
			}
		}
		if !found {
			t.Errorf("BannedScopes should name %q", want)
		}
	}
}

type fakeIdentityAPI struct {
	id  Identity
	err error
}

func (f fakeIdentityAPI) ResolveTeam(_ context.Context) (Identity, error) {
	return f.id, f.err
}

func TestResolve(t *testing.T) {
	t.Parallel()
	got, err := Resolve(context.Background(), fakeIdentityAPI{id: Identity{TeamID: "T123", TeamName: "Acme"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.TeamID != "T123" {
		t.Errorf("TeamID = %q; want T123", got.TeamID)
	}
}

func TestResolve_EmptyTeamFailsLoudly(t *testing.T) {
	t.Parallel()
	_, err := Resolve(context.Background(), fakeIdentityAPI{id: Identity{TeamID: ""}})
	if err == nil {
		t.Fatal("empty team id should error (never emit un-scoped records)")
	}
}

func TestResolve_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("api down")
	_, err := Resolve(context.Background(), fakeIdentityAPI{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

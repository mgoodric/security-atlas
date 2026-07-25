package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

// Slice 733 AC-1 / P0-733-2 wiring tests for the OIDC session-mint group-role
// derivation. The positive derivation semantics (validated claims → roles) are
// proven end-to-end against Postgres in internal/auth/grouprole's integration
// suite (the SAME Derive call this handler makes). These tests lock the WIRING
// contract: the deriver is invoked only on the validated path, and a forged /
// failed callback never derives (P0-733-2).

// recordingOIDCDeriver records DeriveOnLogin calls so a test can assert whether
// derivation fired and with what (validated) inputs.
type recordingOIDCDeriver struct {
	calls []struct {
		idpConfig uuid.UUID
		userID    string
		groups    []string
	}
}

func (d *recordingOIDCDeriver) DeriveOnLogin(_ context.Context, idpConfigID uuid.UUID, userID string, groups []string) error {
	d.calls = append(d.calls, struct {
		idpConfig uuid.UUID
		userID    string
		groups    []string
	}{idpConfig: idpConfigID, userID: userID, groups: groups})
	return nil
}

// unknownIdpResolver always reports "unknown IdP" — a callback against it never
// completes token validation, so it can never reach the (validated) derivation
// step. Used to prove P0-733-2: an unvalidatable callback does not derive.
type unknownIdpResolver struct{}

func (unknownIdpResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return oidc.IdpConfig{}, oidc.ErrUnknownIdp
}

// TestOIDCCallback_ForgedTokenDoesNotDerive proves P0-733-2: a callback that
// fails validation (here, a CSRF/state-mismatch — the validation guard fires
// before any token is trusted) NEVER invokes the group-role deriver. The
// resolver only ever receives VALIDATED claims; a forged/failed flow derives
// nothing.
func TestOIDCCallback_ForgedTokenDoesNotDerive(t *testing.T) {
	t.Parallel()
	deriver := &recordingOIDCDeriver{}
	// nil user/session stores are never reached on the forged path — the
	// callback returns at the validation guard, well before user upsert.
	h := New(oidc.New(unknownIdpResolver{}), nil, nil, false, nil)
	h.AttachGroupDeriver(deriver)

	// A forged callback: state cookie disagrees with the query param (CSRF
	// guard trips inside HandleCallback before any claim is trusted).
	req := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?tenant_id="+uuid.New().String()+"&code=forged&state=ATTACKER", nil)
	req.AddCookie(&http.Cookie{Name: oidc.StateCookie, Value: "LEGIT"})
	req.AddCookie(&http.Cookie{Name: oidc.VerifierCookie, Value: "v"})
	req.AddCookie(&http.Cookie{Name: oidc.IdpCookie, Value: "default"})
	req.AddCookie(&http.Cookie{Name: oidc.NonceCookie, Value: "n"})
	rec := httptest.NewRecorder()

	h.OIDCCallback(rec, req)

	// The callback failed (no 302 redirect), and CRUCIALLY the deriver was
	// never called — no role derivation on an unvalidated flow (P0-733-2).
	if rec.Code == http.StatusFound {
		t.Fatalf("forged callback must not succeed; got 302")
	}
	if len(deriver.calls) != 0 {
		t.Fatalf("P0-733-2 violated: deriver invoked on a forged/failed callback (%d calls)", len(deriver.calls))
	}
}

// TestAttachGroupDeriver_NilByDefault proves the pre-733 behavior is preserved:
// a Handler with no deriver attached leaves groupDeriver nil (derivation is
// opt-in; an un-wired deployment keeps the slice-508 login behavior).
func TestAttachGroupDeriver_NilByDefault(t *testing.T) {
	t.Parallel()
	h := New(oidc.New(unknownIdpResolver{}), nil, nil, false, nil)
	if h.groupDeriver != nil {
		t.Fatal("groupDeriver must be nil until AttachGroupDeriver is called")
	}
	d := &recordingOIDCDeriver{}
	h.AttachGroupDeriver(d)
	if h.groupDeriver == nil {
		t.Fatal("AttachGroupDeriver did not wire the deriver")
	}
}

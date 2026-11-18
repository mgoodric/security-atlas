//go:build integration

// #2609: why a saved SSO config cannot be used goes to the server log, never
// to the unauthenticated caller of /auth/oidc/login or /auth/oidc/callback.
package auth_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

// The reason the resolver gives for a secret sealed under another key.
const unreadableReason = "re-enter it on the SSO settings page"

// wrongKeyResolver resolves with a crypter whose key is not the one the
// config was saved under, so the secret does not decrypt.
func wrongKeyResolver(t *testing.T, h harness) *idpconfig.Resolver {
	t.Helper()
	c, err := idpconfig.NewSecretCrypter(bytes.Repeat([]byte{0x7a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return idpconfig.NewResolver(h.app, c, devIssuerPolicy)
}

// captureSlog routes the default slog logger into a buffer for the test.
// The tests that use it do not run in parallel.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func assertGenericUnusable(t *testing.T, step string, code int, body string) {
	t.Helper()
	if code != http.StatusServiceUnavailable {
		t.Errorf("%s = %d %s, want 503", step, code, body)
	}
	if !strings.Contains(body, authapi.ErrMsgSSOConfigUnusable) {
		t.Errorf("%s body = %s, want the generic SSO message", step, body)
	}
	for _, leak := range []string{unreadableReason, "decrypt", "issuer_url", "client_id", "unusable"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s body carries the config reason (%q): %s", step, leak, body)
		}
	}
}

func TestUnusableSSOConfig_LoginAnswersGeneric(t *testing.T) {
	h := newHarness(t)
	tenant := uuid.New()
	h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
	idp := newFakeIdP(t)
	idp.emailVerified = true
	h.saveSSO(t, tenant, settingsFor(idp, "s"))
	logs := captureSlog(t)

	rec := h.signIn(t, oidc.New(wrongKeyResolver(t, h), testStateKey, loopbackIdP), idp, tenant)
	assertGenericUnusable(t, "login", rec.Code, rec.Body.String())
	if !strings.Contains(logs.String(), unreadableReason) {
		t.Errorf("server log does not carry the reason; log:\n%s", logs.String())
	}
}

// switchingResolver answers the first resolve (the login) with good and
// every later one (the callback) with bad, as when the config is broken
// between the two requests.
type switchingResolver struct {
	mu        sync.Mutex
	calls     int
	good, bad oidc.IdpResolver
}

func (s *switchingResolver) ResolveIdp(ctx context.Context, tenantID uuid.UUID, name string) (oidc.IdpConfig, error) {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		return s.good.ResolveIdp(ctx, tenantID, name)
	}
	return s.bad.ResolveIdp(ctx, tenantID, name)
}

func TestUnusableSSOConfig_CallbackAnswersGeneric(t *testing.T) {
	h := newHarness(t)
	tenant := uuid.New()
	h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
	idp := newFakeIdP(t)
	idp.emailVerified = true
	h.saveSSO(t, tenant, settingsFor(idp, "s"))
	logs := captureSlog(t)

	resolver := &switchingResolver{
		good: idpconfig.NewResolver(h.app, testSecretCrypter(t), devIssuerPolicy),
		bad:  wrongKeyResolver(t, h),
	}
	rec := h.signIn(t, oidc.New(resolver, testStateKey, loopbackIdP), idp, tenant)
	if resolver.calls < 2 {
		t.Fatalf("the callback did not run (resolver calls = %d): %d %s", resolver.calls, rec.Code, rec.Body.String())
	}
	assertGenericUnusable(t, "callback", rec.Code, rec.Body.String())
	if !strings.Contains(logs.String(), unreadableReason) {
		t.Errorf("server log does not carry the reason; log:\n%s", logs.String())
	}
}

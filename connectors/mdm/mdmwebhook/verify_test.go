package mdmwebhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// plain renders v with the given fmt verb — used by the no-secret-leak guards.
func plain(verb string, v any) string { return fmt.Sprintf(verb, v) }

func hdr(key, val string) http.Header {
	h := http.Header{}
	if val != "" {
		h.Set(key, val)
	}
	return h
}

// SharedSecretVerifier: correct credential accepts.
func TestSharedSecretVerifier_Accepts(t *testing.T) {
	t.Parallel()
	v := NewSharedSecretVerifier(testHeader, testSecret)
	if err := v.Verify(nil, hdr(testHeader, testSecret)); err != nil {
		t.Fatalf("Verify(correct) = %v, want nil", err)
	}
}

// SharedSecretVerifier: missing / wrong credential rejects.
func TestSharedSecretVerifier_Rejects(t *testing.T) {
	t.Parallel()
	v := NewSharedSecretVerifier(testHeader, testSecret)
	for _, tc := range []struct {
		name string
		val  string
	}{
		{"missing", ""},
		{"wrong", "test-wrong-secret"},
		{"prefix-of-secret", testSecret[:8]},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := v.Verify(nil, hdr(testHeader, tc.val)); err == nil {
				t.Errorf("Verify(%s) = nil, want rejection", tc.name)
			}
		})
	}
}

// ClientStateVerifier: matching clientState accepts; absent/wrong rejects.
func TestClientStateVerifier(t *testing.T) {
	t.Parallel()
	const cs = "test-client-state-not-a-real-value"
	extract := func(body []byte) (string, bool) {
		var v struct {
			Value []struct {
				ClientState string `json:"clientState"`
			} `json:"value"`
		}
		if err := json.Unmarshal(body, &v); err != nil || len(v.Value) == 0 {
			return "", false
		}
		// Require EVERY notification to carry the matching clientState — return the
		// first, and a mismatch on any later one is caught by the caller. For this
		// unit we return the first.
		return v.Value[0].ClientState, true
	}
	v := NewClientStateVerifier(cs, extract)

	good := []byte(`{"value":[{"clientState":"test-client-state-not-a-real-value"}]}`)
	if err := v.Verify(good, nil); err != nil {
		t.Fatalf("Verify(matching) = %v, want nil", err)
	}

	bad := []byte(`{"value":[{"clientState":"test-forged-state"}]}`)
	if err := v.Verify(bad, nil); err == nil {
		t.Error("Verify(mismatch) = nil, want rejection")
	}

	none := []byte(`{"value":[]}`)
	if err := v.Verify(none, nil); err == nil {
		t.Error("Verify(no clientState) = nil, want rejection")
	}
}

// ClientStateVerifier does not leak the secret on a format path.
func TestClientStateVerifier_DoesNotLeakSecret(t *testing.T) {
	t.Parallel()
	const cs = "test-client-state-secret-xyz"
	v := NewClientStateVerifier(cs, func([]byte) (string, bool) { return "", false })
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(plain(verb, v), cs) {
			t.Errorf("ClientStateVerifier leaked secret via %s", verb)
		}
	}
}

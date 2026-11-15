package oauthclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// countingVerifier wraps password.Verify and counts calls, so the
// timing-equalizer test asserts on work done rather than wall-clock time.
type countingVerifier struct{ calls int }

func (v *countingVerifier) Verify(plaintext, encoded string) (bool, error) {
	v.calls++
	return password.Verify(plaintext, encoded)
}

// TestAuthenticateRunsOneVerifyOnEveryPath covers security-atlas#2241: an
// unknown, empty, disabled (revoked) or known client_id each cost exactly
// one secret verification, and every rejection returns ErrUnknownClient.
func TestAuthenticateRunsOneVerifyOnEveryPath(t *testing.T) {
	t.Parallel()
	const secret = "the-right-secret"
	realHash, err := password.Hash(secret)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	dummyHash := timingDummyHash()
	if dummyHash == "" {
		t.Fatal("timingDummyHash is empty")
	}
	disabledAt := time.Now()

	cases := []struct {
		name    string
		row     *Client
		hash    string
		secret  string
		wantOK  bool
		wantErr error
	}{
		{name: "known client, right secret", row: &Client{ClientID: "c"}, hash: realHash, secret: secret, wantOK: true},
		{name: "known client, wrong secret", row: &Client{ClientID: "c"}, hash: realHash, secret: "wrong", wantErr: ErrUnknownClient},
		{name: "known client, empty secret", row: &Client{ClientID: "c"}, hash: realHash, secret: "", wantErr: ErrUnknownClient},
		{name: "unknown or malformed client_id", row: nil, secret: secret, wantErr: ErrUnknownClient},
		{name: "unknown client_id, empty secret", row: nil, secret: "", wantErr: ErrUnknownClient},
		{name: "disabled (revoked) client, right secret", row: &Client{ClientID: "c", DisabledAt: &disabledAt}, hash: realHash, secret: secret, wantErr: ErrUnknownClient},
		{name: "disabled (revoked) client, wrong secret", row: &Client{ClientID: "c", DisabledAt: &disabledAt}, hash: realHash, secret: "wrong", wantErr: ErrUnknownClient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := &countingVerifier{}
			s := &Store{verifier: v, dummyHash: timingDummyHash}
			got, err := s.authenticate(tc.row, tc.hash, tc.secret, dummyHash)
			if v.calls != 1 {
				t.Errorf("verifier calls = %d, want 1", v.calls)
			}
			if tc.wantOK {
				if err != nil || got == nil {
					t.Fatalf("authenticate = (%v, %v), want a client", got, err)
				}
				return
			}
			if got != nil {
				t.Errorf("authenticate returned client %+v, want nil", got)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestAuthenticateCorruptHashStillBurnsArgon2 covers the corrupt stored
// hash: password.Verify rejects it at decode, before any argon2id work, so
// authenticate burns the dummy verify to keep the rejection's cost equal.
func TestAuthenticateCorruptHashStillBurnsArgon2(t *testing.T) {
	t.Parallel()
	dummyHash := timingDummyHash()
	v := &countingVerifier{}
	s := &Store{verifier: v, dummyHash: timingDummyHash}
	got, err := s.authenticate(&Client{ClientID: "c"}, "not-an-argon2id-hash", "secret", dummyHash)
	if got != nil || !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("authenticate = (%v, %v), want (nil, ErrUnknownClient)", got, err)
	}
	// One decode-failed call plus one full argon2id run on the dummy hash.
	if v.calls != 2 {
		t.Errorf("verifier calls = %d, want 2 (failed decode + dummy burn)", v.calls)
	}
}

// TestVerifyEmptyClientIDSkipsLookupButVerifies covers the empty-client_id
// early path: Verify skips the DB lookup (nil pool proves it) yet still
// runs one verification before rejecting.
func TestVerifyEmptyClientIDSkipsLookupButVerifies(t *testing.T) {
	t.Parallel()
	v := &countingVerifier{}
	s := &Store{verifier: v, dummyHash: timingDummyHash}
	got, err := s.Verify(context.Background(), "", "secret")
	if got != nil || !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("Verify = (%v, %v), want (nil, ErrUnknownClient)", got, err)
	}
	if v.calls != 1 {
		t.Errorf("verifier calls = %d, want 1", v.calls)
	}
}

// TestVerifyFailsClosedWithoutDummyHash covers the build-failure path: no
// lookup and no fast rejection, the same error for every caller.
func TestVerifyFailsClosedWithoutDummyHash(t *testing.T) {
	t.Parallel()
	v := &countingVerifier{}
	s := &Store{verifier: v, dummyHash: func() string { return "" }}
	_, err := s.Verify(context.Background(), "any-client", "secret")
	if !errors.Is(err, errTimingEqualizerUnavailable) {
		t.Fatalf("err = %v, want errTimingEqualizerUnavailable", err)
	}
	if v.calls != 0 {
		t.Errorf("verifier calls = %d, want 0", v.calls)
	}
}

// TestTimingDummyHashMatchesClientSecretParams pins that the dummy hash
// carries the same argon2id parameters as a real client secret's hash.
func TestTimingDummyHashMatchesClientSecretParams(t *testing.T) {
	t.Parallel()
	clientHash, err := password.Hash("x")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if got, want := paramsPrefix(timingDummyHash()), paramsPrefix(clientHash); got != want {
		t.Errorf("dummy hash params %q, want %q", got, want)
	}
}

// paramsPrefix returns the "$argon2id$v=..$m=..,t=..,p=.." head of an
// encoded hash, dropping salt and key.
func paramsPrefix(encoded string) string {
	n := 0
	for i, r := range encoded {
		if r == '$' {
			n++
			if n == 4 {
				return encoded[:i]
			}
		}
	}
	return encoded
}

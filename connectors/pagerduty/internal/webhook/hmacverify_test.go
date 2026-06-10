package webhook

import (
	"errors"
	"net/http"
	"testing"
)

func TestHMACVerifier_Verify(t *testing.T) {
	t.Parallel()
	body := []byte(`{"event":{"event_type":"incident.triggered","data":{"id":"PINC1"}}}`)
	v := NewHMACVerifier(testSecret)
	good := Sign([]byte(testSecret), body)

	cases := []struct {
		name    string
		sig     string
		wantErr error
	}{
		{"valid single", good, nil},
		{"no header", "", ErrUnsigned},
		{"unknown scheme only", "v2=deadbeef", ErrUnsigned},
		{"malformed hex", "v1=zzzz", ErrBadSignature},
		{"wrong signature", Sign([]byte("other"), body), ErrBadSignature},
		{"valid among unknown", "v2=deadbeef," + good, nil},
		{"valid second of two v1", Sign([]byte("old"), body) + "," + good, nil},
		{"whitespace tolerant", "  " + good + "  ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tc.sig != "" {
				h.Set(HeaderSignature, tc.sig)
			}
			err := v.Verify(body, h)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Verify(%s) = %v, want nil", tc.name, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify(%s) = %v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

// Tampered body: a valid signature over a DIFFERENT body must not verify (covers
// in-transit tampering — threat-model T).
func TestHMACVerifier_TamperedBody(t *testing.T) {
	t.Parallel()
	original := []byte(`{"event":{"data":{"id":"PINC1"}}}`)
	tampered := []byte(`{"event":{"data":{"id":"PINC2"}}}`)
	v := NewHMACVerifier(testSecret)
	h := http.Header{}
	h.Set(HeaderSignature, Sign([]byte(testSecret), original))
	if err := v.Verify(tampered, h); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered body verified: %v", err)
	}
}

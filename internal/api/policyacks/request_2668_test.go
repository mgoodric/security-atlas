package policyacks

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecodeAckRequest2668 pins the optional acknowledge body: nothing, or
// no policy_version_id, pins nothing; a uuid pins it; anything else is a
// 400 before the store is touched.
func TestDecodeAckRequest2668(t *testing.T) {
	t.Parallel()
	const v = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "empty", body: ""},
		{name: "empty object", body: "{}"},
		{name: "null", body: "null"},
		{name: "blank version", body: `{"policy_version_id":""}`},
		{name: "pinned", body: `{"policy_version_id":"` + v + `"}`, want: v},
		{name: "not a uuid", body: `{"policy_version_id":"nope"}`, wantErr: true},
		{name: "not json", body: "[", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("POST", "/v1/policies/x/acknowledge", strings.NewReader(tc.body))
			got, err := decodeAckRequest(r)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			gotS := ""
			if got != nil {
				gotS = got.String()
			}
			if gotS != tc.want {
				t.Fatalf("pinned %q, want %q", gotS, tc.want)
			}
		})
	}
}

func TestIncludesCurrent2668(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]bool{
		"":                               false,
		"?include=current":               true,
		"?include=ack_rate,%20current":   true,
		"?include=other&include=current": true,
		"?include=currently":             false,
	} {
		r := httptest.NewRequest("GET", "/v1/me/acknowledgments"+q, nil)
		if got := includesCurrent(r); got != want {
			t.Errorf("%q: includesCurrent = %v, want %v", q, got, want)
		}
	}
}

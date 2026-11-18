package policies

import (
	"reflect"
	"testing"
)

// TestAudienceValues2524 pins the audience a policy is chosen from: the five
// canonical roles plus everyone (#2524).
func TestAudienceValues2524(t *testing.T) {
	t.Parallel()
	want := []string{"admin", "grc_engineer", "control_owner", "auditor", "viewer", "everyone"}
	if got := AudienceValues(); !reflect.DeepEqual(got, want) {
		t.Errorf("AudienceValues() = %v; want %v", got, want)
	}
}

func TestValidateAudience2524(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		roles []string
		keep  []string
		ok    bool
	}{
		{"empty", nil, nil, true},
		{"roles and everyone", []string{"viewer", "auditor", "everyone"}, nil, true},
		{"free text refused on create", []string{"employee"}, nil, false},
		{"free text the draft already carries stays", []string{"employee", "viewer"}, []string{"employee"}, true},
		{"new free text on a draft refused", []string{"employee", "contractor"}, []string{"employee"}, false},
		{"case matters", []string{"Viewer"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateAudience(tc.roles, tc.keep); (err == nil) != tc.ok {
				t.Errorf("validateAudience(%v, %v) = %v; want ok=%v", tc.roles, tc.keep, err, tc.ok)
			}
		})
	}
}

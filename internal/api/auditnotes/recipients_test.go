// recipients_test.go — #2183: audit-note reply notifications go to each prior
// author's bare users(id), the form every notification reader looks up.
//
// Pure-Go (slice-353 Q-2 fast loop).

package auditnotes

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestUserRecipients(t *testing.T) {
	t.Parallel()
	alice, bob, carol := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cases := []struct {
		name    string
		authors []string
		author  string
		want    []string
	}{
		{"user: prefix stripped", []string{"user:" + alice}, "user:" + bob, []string{alice}},
		{"bare uuid kept", []string{alice}, "user:" + bob, []string{alice}},
		{"one person in both forms collapses", []string{"user:" + alice, alice}, "user:" + bob, []string{alice}},
		// The notes store excludes the author by raw string, so the same
		// person under the other form reaches here and must still be dropped.
		{"author under the other form dropped", []string{alice, "user:" + carol}, "user:" + alice, []string{carol}},
		{"non-users get no notification", []string{"key_abc", "bootstrap-admin", "oauth_client:ci", "user:" + alice}, "user:" + bob, []string{alice}},
		{"non-user author still notifies users", []string{"user:" + alice}, "key_abc", []string{alice}},
		{"none", nil, "user:" + bob, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := userRecipients(tc.authors, tc.author); !slices.Equal(got, tc.want) {
				t.Errorf("userRecipients(%v, %q) = %v; want %v", tc.authors, tc.author, got, tc.want)
			}
		})
	}
}

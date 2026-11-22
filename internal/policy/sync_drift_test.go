// sync_drift_test.go — #2910 pure-Go coverage for the drift comparison's
// whitespace normalisation. No Postgres, no build tag (slice-353 Q-2).
package policy

import "testing"

func TestNormalizeDriftBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"unchanged", "# Title\n\nBody.", "# Title\n\nBody."},
		{"crlf", "# Title\r\n\r\nBody.\r\n", "# Title\n\nBody."},
		{"bare cr", "a\rb", "a\nb"},
		{"trailing spaces and tabs", "a  \t\nb \n", "a\nb"},
		{"trailing nbsp", "a \nb", "a\nb"},
		{"blank-line run", "a\n\n\n\nb", "a\n\nb"},
		{"whitespace-only lines are blank", "a\n  \n\t\n\nb", "a\n\nb"},
		{"trailing newlines", "a\n\n\n", "a"},
		{"leading and inner spaces kept", "  - item  one", "  - item  one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeDriftBody(tc.in); got != tc.want {
				t.Errorf("normalizeDriftBody(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBodyDrifted(t *testing.T) {
	t.Parallel()
	const stored = "# Access Control\n\nAccess is reviewed quarterly.\n"
	cases := []struct {
		name  string
		page  string
		drift bool
	}{
		{"identical", stored, false},
		{"whitespace only", "# Access Control  \r\n\r\n\r\n\r\nAccess is reviewed quarterly.", false},
		{"content", "# Access Control\n\nAccess is reviewed annually.\n", true},
		{"word spacing is content", "# Access Control\n\nAccess is  reviewed quarterly.\n", true},
		{"indentation is content", "# Access Control\n\n    Access is reviewed quarterly.\n", true},
		{"paragraph split is content", "# Access Control\n\nAccess is reviewed\nquarterly.\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bodyDrifted(stored, tc.page); got != tc.drift {
				t.Errorf("bodyDrifted(stored, %q) = %v; want %v", tc.page, got, tc.drift)
			}
		})
	}
}

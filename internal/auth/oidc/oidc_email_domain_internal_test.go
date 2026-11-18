package oidc

import "testing"

// #2034: allowed_email_domains matches the whole domain after the last "@",
// ignoring case, and only on a verified address.
func TestEmailDomainAllowed(t *testing.T) {
	yes, no := true, false
	strict := IdpConfig{AllowedEmailDomains: []string{"Example.com"}}
	trusting := IdpConfig{AllowedEmailDomains: []string{"example.com"}, TrustUnverifiedEmailForLink: true}
	cases := []struct {
		name     string
		email    string
		verified *bool
		cfg      IdpConfig
		want     bool
	}{
		{"exact, verified", "a@example.com", &yes, strict, true},
		{"case differs", "A@EXAMPLE.COM", &yes, strict, true},
		{"lookalike suffix", "a@evil-example.com", &yes, strict, false},
		{"subdomain", "a@sub.example.com", &yes, strict, false},
		{"allowed domain then another", "a@example.com@evil.com", &yes, strict, false},
		{"other domain", "a@example.org", &yes, strict, false},
		{"no domain", "a@", &yes, strict, false},
		{"no local part", "@example.com", &yes, strict, false},
		{"not an address", "example.com", &yes, strict, false},
		{"explicitly unverified", "a@example.com", &no, strict, false},
		{"explicitly unverified, config trusts", "a@example.com", &no, trusting, false},
		{"claim absent, strict config", "a@example.com", nil, strict, false},
		{"claim absent, config trusts", "a@example.com", nil, trusting, true},
		{"blank entry never matches", "a@", &yes, IdpConfig{AllowedEmailDomains: []string{" "}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := emailDomainAllowed(tc.email, tc.verified, tc.cfg); got != tc.want {
				t.Errorf("emailDomainAllowed(%q) = %v, want %v", tc.email, got, tc.want)
			}
		})
	}
}

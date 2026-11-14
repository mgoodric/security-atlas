package approval

import (
	"errors"
	"testing"
)

// TestCheck pins the shared approval rule (#2101): only a person approves;
// never the requester unless the organisation allows self-approval, and
// then the approval is reported as self-approved; a machine is refused
// whatever the setting; a session-recorded requester cannot be compared.
func TestCheck(t *testing.T) {
	t.Parallel()
	person := func(u string) Approver { return Approver{UserID: u, CredentialID: "key_other"} }
	machine := Approver{CredentialID: "key_machine"}
	cases := []struct {
		name      string
		requester string
		approver  Approver
		allowSelf bool
		wantErr   error
		wantSelf  bool
	}{
		{"second person", "user-1", person("user-2"), false, nil, false},
		{"requester, setting off", "user-1", person("user-1"), false, ErrSelfApproval, false},
		{"requester, setting on", "user-1", person("user-1"), true, nil, true},
		{"second person, setting on", "user-1", person("user-2"), true, nil, false},
		{"requester by credential id, setting off", "key_abc", Approver{UserID: "user-1", CredentialID: "key_abc"}, false, ErrSelfApproval, false},
		{"requester by credential id, setting on", "key_abc", Approver{UserID: "user-1", CredentialID: "key_abc"}, true, nil, true},
		{"machine, setting off", "user-1", machine, false, ErrNotPerson, false},
		{"machine, setting on", "user-1", machine, true, ErrNotPerson, false},
		{"machine, no requester", "", machine, true, ErrNotPerson, false},
		{"no recorded requester", "", person("user-1"), false, nil, false},
		{"session requester", "jwt:token-1", person("user-2"), false, ErrRequesterUnknown, false},
		{"session requester, setting on", "jwt:token-1", person("user-2"), true, ErrRequesterUnknown, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			self, err := Check(tc.requester, tc.approver, tc.allowSelf)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if self != tc.wantSelf {
				t.Fatalf("selfApproved = %v, want %v", self, tc.wantSelf)
			}
		})
	}
}

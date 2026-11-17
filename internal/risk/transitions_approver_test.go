package risk

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/approval"
)

// TestCheckAcceptanceApprover pins how an acceptance approval maps the
// shared approval rule (#2105), including #2371: an acceptance with no
// comparable proposer is refused for everyone, never admitted.
func TestCheckAcceptanceApprover(t *testing.T) {
	t.Parallel()
	const alice, bob = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	person := func(id string) approval.Approver { return approval.Approver{UserID: id, CredentialID: "jwt:" + id} }
	cases := []struct {
		name      string
		proposer  string
		approver  approval.Approver
		allowSelf bool
		wantSelf  bool
		wantErr   error
	}{
		{"second person", alice, person(bob), false, false, nil},
		{"proposer, self-approval off", alice, person(alice), false, false, ErrAcceptanceSelfApproval},
		{"proposer, self-approval on", alice, person(alice), true, true, nil},
		{"machine approver", alice, approval.Approver{CredentialID: "key_x"}, true, false, ErrAcceptanceApproverNotPerson},
		{"no proposer recorded", "", person(bob), false, false, ErrAcceptanceProposerUnknown},
		{"no proposer recorded, self-approval on", "", person(bob), true, false, ErrAcceptanceProposerUnknown},
		{"session proposer", "jwt:abc", person(bob), false, false, ErrAcceptanceProposerUnknown},
		{"machine approver, no proposer", "", approval.Approver{CredentialID: "key_x"}, false, false, ErrAcceptanceApproverNotPerson},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			self, err := CheckAcceptanceApprover(c.proposer, c.approver, c.allowSelf)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if self != c.wantSelf {
				t.Fatalf("selfApproved = %v, want %v", self, c.wantSelf)
			}
		})
	}
	if !errors.Is(ErrAcceptanceProposerUnknown, ErrTransitionState) {
		t.Fatal("ErrAcceptanceProposerUnknown must wrap ErrTransitionState so it renders as 409")
	}
}

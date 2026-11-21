package risk

import (
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
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

// TestValidateProposal_PeopleOnly (#2623): proposing an acceptance is for
// people only. A machine proposer is refused with a 403-class error before
// any other check, at create (CreateProposal) as on the transition, so a
// machine with an otherwise invalid body is still told it is a machine.
func TestValidateProposal_PeopleOnly(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	machine := ProposeAcceptanceInput{
		Justification: "within appetite", ProposedUntil: now.AddDate(0, 1, 0), Proposer: "key_x",
	}
	if _, err := ValidateProposal(machine, now); !errors.Is(err, ErrAcceptanceProposerNotPerson) || !IsApprovalRefusal(err) {
		t.Fatalf("machine proposer: err = %v, want ErrAcceptanceProposerNotPerson (an approval refusal)", err)
	}
	if _, err := ValidateProposal(ProposeAcceptanceInput{Proposer: "key_x"}, now); !errors.Is(err, ErrAcceptanceProposerNotPerson) {
		t.Fatalf("machine proposer, empty body: err = %v, want ErrAcceptanceProposerNotPerson", err)
	}
	if _, err := CreateProposal(dbx.RiskAcceptanceStatusProposed, machine, now); !errors.Is(err, ErrAcceptanceProposerNotPerson) {
		t.Fatalf("machine create: err = %v, want ErrAcceptanceProposerNotPerson", err)
	}
	// A create that proposes nothing is not a proposal: no person needed.
	if _, err := CreateProposal(dbx.RiskAcceptanceStatusApproved, ProposeAcceptanceInput{Proposer: "key_x"}, now); err != nil {
		t.Fatalf("machine create, not proposed: err = %v, want nil", err)
	}
	person := machine
	person.Proposer, person.ProposerIsPerson = "11111111-1111-1111-1111-111111111111", true
	if _, err := ValidateProposal(person, now); err != nil {
		t.Fatalf("person proposer: err = %v, want nil", err)
	}
	if _, err := CreateProposal(dbx.RiskAcceptanceStatusProposed, person, now); err != nil {
		t.Fatalf("person create: err = %v, want nil", err)
	}
}

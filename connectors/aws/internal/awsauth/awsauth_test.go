package awsauth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
)

type fakeSTS struct{ accountID string }

func (f *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String(f.accountID)}, nil
}

type fakeOrg struct {
	tags map[string]string // key -> value
	err  error
}

func (f *fakeOrg) ListTagsForResource(context.Context, *organizations.ListTagsForResourceInput, ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := &organizations.ListTagsForResourceOutput{}
	for k, v := range f.tags {
		out.Tags = append(out.Tags, orgtypes.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return out, nil
}

type accessDeniedErr struct{ code string }

func (e accessDeniedErr) Error() string                 { return e.code }
func (e accessDeniedErr) ErrorCode() string             { return e.code }
func (e accessDeniedErr) ErrorMessage() string          { return e.code }
func (e accessDeniedErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestResolveIdentity_OrgTagWins(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&fakeSTS{accountID: "111122223333"},
		&fakeOrg{tags: map[string]string{"Environment": "prod"}},
		"staging-flag-fallback",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.AccountID != "111122223333" || id.Environment != "prod" || id.Source != "org_tag" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestResolveIdentity_AccessDeniedFallsBackToFlag(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&fakeSTS{accountID: "111122223333"},
		&fakeOrg{err: accessDeniedErr{code: "AccessDeniedException"}},
		"prod",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "prod" || id.Source != "flag" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestResolveIdentity_OrgsNotInUseFallsBackToFlag(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&fakeSTS{accountID: "111122223333"},
		&fakeOrg{err: accessDeniedErr{code: "AWSOrganizationsNotInUseException"}},
		"dev",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "dev" || id.Source != "flag" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestResolveIdentity_BothMissingErrors(t *testing.T) {
	t.Parallel()
	_, err := awsauth.ResolveIdentity(
		context.Background(),
		&fakeSTS{accountID: "111122223333"},
		&fakeOrg{err: accessDeniedErr{code: "AccessDenied"}},
		"",
	)
	if err == nil {
		t.Fatal("expected error when both org tag and flag are missing")
	}
}

func TestAssume_RejectsEmptyRoleARN(t *testing.T) {
	t.Parallel()
	if _, err := awsauth.Assume(context.Background(), "", "us-east-1"); err == nil {
		t.Fatal("expected error for empty role-arn")
	}
}

func TestAssume_RejectsEmptyRegion(t *testing.T) {
	t.Parallel()
	if _, err := awsauth.Assume(context.Background(), "arn:aws:iam::111122223333:role/x", ""); err == nil {
		t.Fatal("expected error for empty region")
	}
}

func TestResolveIdentity_OrgErrorOtherThanAccessDeniedStillFallsBack(t *testing.T) {
	t.Parallel()
	// A non-AccessDenied error should still let the connector run with the flag.
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&fakeSTS{accountID: "111122223333"},
		&fakeOrg{err: errors.New("transient transport error")},
		"prod",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "prod" || id.Source != "flag" {
		t.Fatalf("identity = %+v", id)
	}
}

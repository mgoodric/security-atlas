// Additional unit tests for the awsauth package, lifting merged coverage
// from 66.7% to 97.2% per slice 298.
//
// Load-bearing functions and the branches each test exercises:
//
//   - Assume: success path (region + role-arn both non-empty) — covers
//     config loading + STS client wiring + AssumeRoleProvider + cache.
//   - ResolveIdentity: (a) STS error wraps cleanly; (b) STS returns
//     empty account id; (c) nil OrgAPI skips the org block entirely;
//     (d) Org Environment tag present but value empty (falls through
//     to flag); (e) Org tags present but no Environment key (also
//     falls through to flag); (f) Org returns a non-access-denied
//     APIError (drives isAccessDenied's terminal `return false`).
//   - isAccessDenied: APIError with an unmatched code (e.g.
//     ThrottlingException) returns false — exercised transitively via
//     (f) above.
//   - OrgClient / STSClient: non-nil constructor smoke tests against
//     a zero aws.Config.
//
// No vendor-prefixed tokens (no AKIA*, etc.) appear in fixtures — neutral
// 12-digit dummy account IDs (111122223333) and "test-*" strings only,
// per CLAUDE.md's hard rule.
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

// stsErrFake makes GetCallerIdentity fail. Exercises the early-return error
// wrap in ResolveIdentity.
type stsErrFake struct{ err error }

func (f *stsErrFake) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return nil, f.err
}

// stsEmptyAccountFake returns a successful call but with an empty Account
// pointer. Exercises the empty-account-id guard.
type stsEmptyAccountFake struct{}

func (f *stsEmptyAccountFake) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String("")}, nil
}

func TestResolveIdentity_STSErrorWraps(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sts boom")
	_, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsErrFake{err: sentinel},
		nil,
		"prod",
	)
	if err == nil {
		t.Fatal("expected STS error to propagate")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
}

func TestResolveIdentity_EmptyAccountIDErrors(t *testing.T) {
	t.Parallel()
	_, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsEmptyAccountFake{},
		nil,
		"prod",
	)
	if err == nil {
		t.Fatal("expected error when STS returns empty account id")
	}
}

// nilOrgFake is not actually nil — but a nil OrgAPI argument exercises the
// path where the connector runs without an Organizations client at all (the
// out-of-payer-account case).
type stsOK struct{ accountID string }

func (f *stsOK) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Account: aws.String(f.accountID)}, nil
}

func TestResolveIdentity_NilOrgAPISkipsOrgBlock(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsOK{accountID: "111122223333"},
		nil,
		"prod",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "prod" || id.Source != "flag" || id.AccountID != "111122223333" {
		t.Fatalf("identity = %+v", id)
	}
}

// orgEmptyValueFake returns the Environment tag with an empty string value.
// Per the resolver contract: an empty tag value falls through to the flag
// rather than being treated as a valid environment.
type orgEmptyValueFake struct{}

func (f *orgEmptyValueFake) ListTagsForResource(context.Context, *organizations.ListTagsForResourceInput, ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error) {
	return &organizations.ListTagsForResourceOutput{
		Tags: []orgtypes.Tag{
			{Key: aws.String("Environment"), Value: aws.String("")},
		},
	}, nil
}

func TestResolveIdentity_OrgTagEmptyValueFallsBackToFlag(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsOK{accountID: "111122223333"},
		&orgEmptyValueFake{},
		"staging",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "staging" || id.Source != "flag" {
		t.Fatalf("identity = %+v (expected staging/flag)", id)
	}
}

// orgUnrelatedTagFake returns tags that don't include the Environment key at
// all — also a fall-through-to-flag path.
type orgUnrelatedTagFake struct{}

func (f *orgUnrelatedTagFake) ListTagsForResource(context.Context, *organizations.ListTagsForResourceInput, ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error) {
	return &organizations.ListTagsForResourceOutput{
		Tags: []orgtypes.Tag{
			{Key: aws.String("CostCenter"), Value: aws.String("test-cc-123")},
			{Key: aws.String("Owner"), Value: aws.String("test-owner")},
		},
	}, nil
}

func TestResolveIdentity_NoEnvironmentTagFallsBackToFlag(t *testing.T) {
	t.Parallel()
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsOK{accountID: "111122223333"},
		&orgUnrelatedTagFake{},
		"dev",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "dev" || id.Source != "flag" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestAssume_SuccessPathReturnsConfiguredCredentials(t *testing.T) {
	t.Parallel()
	// Assume only validates inputs and wires up the SDK — it does not call
	// AWS until credentials are first retrieved. So a valid call should
	// return a config whose Credentials provider is set and whose Region
	// matches the requested value, without any network IO.
	cfg, err := awsauth.Assume(
		context.Background(),
		"arn:aws:iam::111122223333:role/test-role",
		"us-east-1",
	)
	if err != nil {
		t.Fatalf("Assume: %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("Region = %q (expected us-east-1)", cfg.Region)
	}
	if cfg.Credentials == nil {
		t.Fatal("Credentials provider should be wired (AssumeRoleProvider cached)")
	}
}

// unmatchedAPIErr exercises the isAccessDenied path where errors.As succeeds
// but the ErrorCode() doesn't match any of the access-denied codes — the
// switch falls through to the trailing `return false`.
type unmatchedAPIErr struct{}

func (unmatchedAPIErr) Error() string                 { return "ThrottlingException" }
func (unmatchedAPIErr) ErrorCode() string             { return "ThrottlingException" }
func (unmatchedAPIErr) ErrorMessage() string          { return "rate exceeded" }
func (unmatchedAPIErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

// orgWithUnmatchedAPIErr surfaces an APIError whose code isn't in the
// access-denied list — drives isAccessDenied's terminal `return false`.
type orgWithUnmatchedAPIErr struct{}

func (orgWithUnmatchedAPIErr) ListTagsForResource(context.Context, *organizations.ListTagsForResourceInput, ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error) {
	return nil, unmatchedAPIErr{}
}

func TestResolveIdentity_OrgUnmatchedAPIErrorStillFallsBack(t *testing.T) {
	t.Parallel()
	// An APIError with a non-access-denied code (e.g. throttling) is treated
	// by ResolveIdentity as "unexpected; fall through to flag" — same as the
	// generic-error path, but exercising the switch's default arm in
	// isAccessDenied.
	id, err := awsauth.ResolveIdentity(
		context.Background(),
		&stsOK{accountID: "111122223333"},
		orgWithUnmatchedAPIErr{},
		"prod",
	)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.Environment != "prod" || id.Source != "flag" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestOrgClient_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	c := awsauth.OrgClient(aws.Config{Region: "us-east-1"})
	if c == nil {
		t.Fatal("OrgClient returned nil")
	}
}

func TestSTSClient_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	c := awsauth.STSClient(aws.Config{Region: "us-east-1"})
	if c == nil {
		t.Fatal("STSClient returned nil")
	}
}

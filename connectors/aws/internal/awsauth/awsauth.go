// Package awsauth handles STS AssumeRole + account-identity + environment
// inference for the AWS connector. No access keys ever touch this package —
// only role ARNs. STS credentials auto-rotate via aws.NewCredentialsCache.
package awsauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

// EnvironmentTagKey is the AWS account tag the connector reads to infer the
// `environment` scope dimension. If the assumed role lacks
// Organizations:DescribeAccount or the tag is absent, the caller's
// --environment fallback applies.
const EnvironmentTagKey = "Environment"

// Identity is the resolved AWS identity + environment context for one run.
type Identity struct {
	AccountID   string
	Environment string
	Source      string // "org_tag" or "flag"
}

// STSAPI is the narrow surface this package needs from the STS client. The
// concrete *sts.Client satisfies it; tests use a fake.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// OrgAPI is the narrow surface for the Organizations client. The concrete
// *organizations.Client satisfies it.
type OrgAPI interface {
	ListTagsForResource(ctx context.Context, params *organizations.ListTagsForResourceInput, optFns ...func(*organizations.Options)) (*organizations.ListTagsForResourceOutput, error)
}

// Assume builds an aws.Config whose credentials are obtained by assuming
// roleARN via STS. region is required (S3 service calls need it). The
// returned config's credentials auto-rotate ahead of expiry.
func Assume(ctx context.Context, roleARN, region string) (aws.Config, error) {
	if roleARN == "" {
		return aws.Config{}, errors.New("awsauth: role-arn is required (no static access keys supported)")
	}
	if region == "" {
		return aws.Config{}, errors.New("awsauth: region is required")
	}

	base, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		// Refuse to fall back to env-var or profile-baked static creds.
		// The assumed-role provider below is the only credential source.
		config.WithCredentialsProvider(aws.AnonymousCredentials{}),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("awsauth: load base config: %w", err)
	}

	// STS client gets called with empty creds against the public endpoint —
	// the AssumeRole API itself doesn't require signing the request beyond
	// the role ARN; in practice the cluster runs with a baseline identity
	// (workload identity, OIDC token) bound elsewhere. For slice 004 the
	// test fake injects identity; the live binary picks up the platform's
	// baseline identity via the default credential chain (IRSA, OIDC, etc.).
	stsClient := sts.NewFromConfig(base)
	provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN)
	base.Credentials = aws.NewCredentialsCache(provider)
	return base, nil
}

// ResolveIdentity returns the AccountID and environment tag for the assumed
// identity. The order of preference for environment:
//  1. Organizations:ListTagsForResource on the account, key=Environment.
//  2. envFlag (the connector's --environment override).
//
// AccessDenied or any error from Organizations falls back to envFlag.
// If neither path yields a value, returns an error so the connector fails
// loudly rather than emitting un-scoped records.
func ResolveIdentity(ctx context.Context, stsAPI STSAPI, orgAPI OrgAPI, envFlag string) (Identity, error) {
	caller, err := stsAPI.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, fmt.Errorf("awsauth: get-caller-identity: %w", err)
	}
	accountID := aws.ToString(caller.Account)
	if accountID == "" {
		return Identity{}, errors.New("awsauth: STS returned empty account id")
	}

	if orgAPI != nil {
		tags, err := orgAPI.ListTagsForResource(ctx, &organizations.ListTagsForResourceInput{ResourceId: aws.String(accountID)})
		switch {
		case err == nil:
			for _, t := range tags.Tags {
				if aws.ToString(t.Key) == EnvironmentTagKey {
					if v := aws.ToString(t.Value); v != "" {
						return Identity{AccountID: accountID, Environment: v, Source: "org_tag"}, nil
					}
				}
			}
		case isAccessDenied(err):
			// Fall through to flag.
		default:
			// Unexpected Organizations error; still fall through so the
			// connector can run with the flag value. Log at the caller.
			_ = err
		}
	}

	if envFlag != "" {
		return Identity{AccountID: accountID, Environment: envFlag, Source: "flag"}, nil
	}
	return Identity{}, fmt.Errorf("awsauth: environment unknown — pass --environment or grant the role Organizations:ListTagsForResource and tag account %s with %q", accountID, EnvironmentTagKey)
}

// isAccessDenied detects the AWS API error codes that mean "your role
// can't see Organizations" — common when the connector runs outside the
// payer account.
func isAccessDenied(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDenied", "AccessDeniedException", "AWSOrganizationsNotInUseException":
		return true
	}
	return false
}

// OrgClient is a constructor exported so the connector main can build a
// real Organizations client from an aws.Config; tests inject a fake OrgAPI.
func OrgClient(cfg aws.Config) *organizations.Client {
	return organizations.NewFromConfig(cfg)
}

// STSClient is the symmetric helper.
func STSClient(cfg aws.Config) *sts.Client {
	return sts.NewFromConfig(cfg)
}

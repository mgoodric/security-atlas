package s3tls_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/s3tls"
)

// documented is the statement the S3 user guide gives under "Restrict
// access to only HTTPS requests", for bucket acme-logs.
const documented = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "RestrictToTLSRequestsOnly",
    "Effect": "Deny",
    "Principal": "*",
    "Action": "s3:*",
    "Resource": ["arn:aws:s3:::acme-logs", "arn:aws:s3:::acme-logs/*"],
    "Condition": {"Bool": {"aws:SecureTransport": "false"}}
  }]
}`

func TestDeniesInsecureTransport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		policy string
		want   bool
	}{
		{"documented statement", documented, true},
		{"single statement object", `{"Statement": {"Effect": "Deny", "Principal": "*", "Action": "s3:*",
			"Resource": ["arn:aws:s3:::acme-logs", "arn:aws:s3:::acme-logs/*"],
			"Condition": {"Bool": {"aws:SecureTransport": "false"}}}}`, true},
		{"principal AWS star", `{"Statement": [{"Effect": "Deny", "Principal": {"AWS": "*"}, "Action": ["s3:*"],
			"Resource": ["arn:aws:s3:::acme-logs", "arn:aws:s3:::acme-logs/*"],
			"Condition": {"Bool": {"aws:SecureTransport": ["false"]}}}]}`, true},
		{"action star and resource star", `{"Statement": [{"Effect": "Deny", "Principal": "*", "Action": "*",
			"Resource": "*", "Condition": {"Bool": {"aws:SecureTransport": "false"}}}]}`, true},
		{"bucket and objects in two statements", `{"Statement": [
			{"Effect": "Deny", "Principal": "*", "Action": "s3:*", "Resource": "arn:aws:s3:::acme-logs",
			 "Condition": {"Bool": {"aws:SecureTransport": "false"}}},
			{"Effect": "Deny", "Principal": "*", "Action": "s3:*", "Resource": "arn:aws:s3:::acme-logs/*",
			 "Condition": {"Bool": {"aws:SecureTransport": "false"}}}]}`, true},
		{"objects only", `{"Statement": [{"Effect": "Deny", "Principal": "*", "Action": "s3:*",
			"Resource": "arn:aws:s3:::acme-logs/*", "Condition": {"Bool": {"aws:SecureTransport": "false"}}}]}`, false},
		{"another bucket", strings.ReplaceAll(documented, "acme-logs", "acme-other"), false},
		{"allow not deny", strings.Replace(documented, `"Deny"`, `"Allow"`, 1), false},
		{"secure transport true", strings.Replace(documented, `"aws:SecureTransport": "false"`, `"aws:SecureTransport": "true"`, 1), false},
		{"one principal only", strings.Replace(documented, `"Principal": "*"`, `"Principal": {"AWS": "arn:aws:iam::111122223333:root"}`, 1), false},
		{"service principal", strings.Replace(documented, `"Principal": "*"`, `"Principal": {"Service": "*"}`, 1), false},
		{"get object only", strings.Replace(documented, `"Action": "s3:*"`, `"Action": "s3:GetObject"`, 1), false},
		{"not action", strings.Replace(documented, `"Action": "s3:*"`, `"NotAction": "s3:GetObject"`, 1), false},
		{"extra condition narrows the deny", strings.Replace(documented,
			`"Condition": {"Bool": {"aws:SecureTransport": "false"}}`,
			`"Condition": {"Bool": {"aws:SecureTransport": "false"}, "StringEquals": {"aws:SourceVpce": "vpce-1"}}`, 1), false},
		{"no condition", strings.Replace(documented, `,
    "Condition": {"Bool": {"aws:SecureTransport": "false"}}`, "", 1), false},
		{"unrelated policy", `{"Statement": [{"Effect": "Allow", "Principal": {"AWS": "arn:aws:iam::111122223333:root"},
			"Action": "s3:GetObject", "Resource": "arn:aws:s3:::acme-logs/*"}]}`, false},
		{"empty statement list", `{"Statement": []}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := s3tls.DeniesInsecureTransport(tc.policy, "acme-logs")
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("DeniesInsecureTransport = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestDeniesInsecureTransport_Malformed(t *testing.T) {
	t.Parallel()
	for name, policy := range map[string]string{
		"not json":         `{"Statement": [`,
		"no statement":     `{"Version": "2012-10-17"}`,
		"statement string": `{"Statement": "Deny"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := s3tls.DeniesInsecureTransport(policy, "acme-logs"); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

type fakeS3 struct {
	buckets  []string
	policies map[string]string
	errs     map[string]error
}

func (f *fakeS3) ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	out := &s3.ListBucketsOutput{}
	for _, b := range f.buckets {
		out.Buckets = append(out.Buckets, s3types.Bucket{Name: aws.String(b)})
	}
	return out, nil
}

func (f *fakeS3) GetBucketPolicy(_ context.Context, in *s3.GetBucketPolicyInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	name := aws.ToString(in.Bucket)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	if p, ok := f.policies[name]; ok {
		return &s3.GetBucketPolicyOutput{Policy: aws.String(p)}, nil
	}
	return nil, &smithy.GenericAPIError{Code: "NoSuchBucketPolicy", Message: "The bucket policy does not exist"}
}

func TestInspect(t *testing.T) {
	t.Parallel()
	api := &fakeS3{
		buckets: []string{"acme-logs", "acme-open", "acme-unrelated", "acme-locked", "acme-garbled", "acme-elsewhere"},
		policies: map[string]string{
			"acme-logs":      documented,
			"acme-unrelated": strings.ReplaceAll(documented, "acme-logs", "acme-other"),
			"acme-garbled":   `{"Statement": [`,
		},
		errs: map[string]error{
			"acme-locked": &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"},
		},
	}
	resolver := func(_ context.Context, b string) (string, error) {
		if b == "acme-elsewhere" {
			return "", errors.New("AccessDenied")
		}
		return "us-east-1", nil
	}
	states, err := s3tls.Inspect(context.Background(), api, resolver, func(string) s3tls.API { return api })
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	type want struct {
		result                s3tls.Result
		read, present, denies bool
		reasonPrefix, region  string
	}
	wants := map[string]want{
		"acme-logs":      {s3tls.ResultPass, true, true, true, "", "us-east-1"},
		"acme-open":      {s3tls.ResultFail, true, false, false, "", "us-east-1"},
		"acme-unrelated": {s3tls.ResultFail, true, true, false, "", "us-east-1"},
		"acme-locked":    {s3tls.ResultInconclusive, false, false, false, "get-bucket-policy: ", "us-east-1"},
		"acme-garbled":   {s3tls.ResultInconclusive, false, false, false, "parse bucket policy: ", "us-east-1"},
		"acme-elsewhere": {s3tls.ResultInconclusive, false, false, false, "resolve region: ", ""},
	}
	if len(states) != len(wants) {
		t.Fatalf("got %d states; want %d", len(states), len(wants))
	}
	for _, s := range states {
		w := wants[s.BucketName]
		if s.Result != w.result || s.PolicyRead != w.read || s.PolicyPresent != w.present ||
			s.DeniesInsecureTransport != w.denies || s.BucketRegion != w.region || !strings.HasPrefix(s.Reason, w.reasonPrefix) {
			t.Errorf("%s: %+v; want %+v", s.BucketName, s, w)
		}
		if w.reasonPrefix == "" && s.Reason != "" {
			t.Errorf("%s: reason = %q; want empty", s.BucketName, s.Reason)
		}
		if s.BucketARN != "arn:aws:s3:::"+s.BucketName {
			t.Errorf("%s: arn = %q", s.BucketName, s.BucketARN)
		}
	}
}

type listFails struct{ fakeS3 }

func (listFails) ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return nil, errors.New("AccessDenied")
}

func TestInspect_ListError(t *testing.T) {
	t.Parallel()
	api := &listFails{}
	_, err := s3tls.Inspect(context.Background(), api, nil, func(string) s3tls.API { return api })
	if err == nil || !strings.Contains(err.Error(), "list buckets") {
		t.Fatalf("err = %v; want a list-buckets error", err)
	}
}

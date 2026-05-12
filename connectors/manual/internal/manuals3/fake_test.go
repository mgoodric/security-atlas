package manuals3

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// fakeAPIFunc is a generic adapter so tests can express API behavior as a
// closure without dragging in the smithy stack.
type fakeAPIFunc func(ctx context.Context, bucket, prefix string) ([]Object, error)

func (f fakeAPIFunc) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	objs, err := f(ctx, aws.ToString(in.Bucket), aws.ToString(in.Prefix))
	if err != nil {
		return nil, err
	}
	out := &s3.ListObjectsV2Output{}
	for i := range objs {
		o := objs[i]
		etag := `"` + o.ETag + `"`
		size := o.Size
		lm := o.LastModified
		out.Contents = append(out.Contents, s3types.Object{
			Key:          aws.String(o.Key),
			ETag:         aws.String(etag),
			Size:         &size,
			LastModified: &lm,
		})
	}
	return out, nil
}

// newFakeAPI returns an API that emits a canned two-object listing.
// The httptest.Server is only used to give the tests a real HTTP round
// trip when we care to exercise the SDK wiring; the API surface for
// List is intentionally pure so we don't have to drive the real SDK
// through a server unless an explicit integration test demands it.
func newFakeAPI(_ /*serverURL*/, _ /*bucket*/, _ /*prefix*/ string) API {
	return fakeAPIFunc(func(_ context.Context, _, _ string) ([]Object, error) {
		return []Object{
			{
				Key:          "audits/q1.pdf",
				ETag:         "etag-q1",
				Size:         2048,
				LastModified: mustTime("2026-04-01T00:00:00Z"),
			},
			{
				Key:          "audits/q2.pdf",
				ETag:         "etag-q2",
				Size:         4096,
				LastModified: mustTime("2026-05-01T00:00:00Z"),
			},
		}, nil
	})
}

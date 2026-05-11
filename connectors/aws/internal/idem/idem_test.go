package idem_test

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/idem"
)

func TestKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:s3:::test-bucket"
	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	first := idem.Key(arn, base)
	for i := 0; i < 60; i++ {
		got := idem.Key(arn, base.Add(time.Duration(i)*time.Minute))
		if got != first {
			t.Fatalf("hour-stable check failed at +%dm: %s vs %s", i, got, first)
		}
	}
}

func TestKey_DifferentHoursDifferentKeys(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:s3:::test-bucket"
	a := idem.Key(arn, time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))
	b := idem.Key(arn, time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC))
	if a == b {
		t.Fatalf("expected different keys across hour boundary")
	}
}

func TestKey_DifferentBucketsDifferentKeys(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	a := idem.Key("arn:aws:s3:::bucket-a", ts)
	b := idem.Key("arn:aws:s3:::bucket-b", ts)
	if a == b {
		t.Fatalf("expected different keys for different buckets")
	}
}

func TestKey_TimezoneInvariant(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:s3:::test-bucket"
	utc := time.Date(2026, 5, 10, 12, 30, 0, 0, time.UTC)
	pacific, _ := time.LoadLocation("America/Los_Angeles")
	if pacific != nil {
		la := utc.In(pacific)
		if got := idem.Key(arn, la); got != idem.Key(arn, utc) {
			t.Fatalf("key changed across timezone: %s vs %s", got, idem.Key(arn, utc))
		}
	}
}

// Slice 162 — pure-logic unit tests for the new helpers in sessions.go.
//
// truncateUserAgent + nullable have no DB dependency; this file lives next
// to the production code so the helpers are exercised on every `go test`
// run (no integration-tag required).

package sessions

import (
	"strings"
	"testing"
)

func TestTruncateUserAgent_PassesShortStringThrough(t *testing.T) {
	in := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15"
	if got := truncateUserAgent(in); got != in {
		t.Errorf("truncateUserAgent short = %q; want %q (unchanged)", got, in)
	}
}

func TestTruncateUserAgent_PassesEmptyStringThrough(t *testing.T) {
	if got := truncateUserAgent(""); got != "" {
		t.Errorf("truncateUserAgent empty = %q; want \"\"", got)
	}
}

func TestTruncateUserAgent_TruncatesAtCap(t *testing.T) {
	in := strings.Repeat("A", MaxUserAgentBytes+10)
	got := truncateUserAgent(in)
	if len(got) != MaxUserAgentBytes {
		t.Errorf("truncateUserAgent len = %d; want %d", len(got), MaxUserAgentBytes)
	}
	if !strings.HasPrefix(in, got) {
		t.Errorf("truncated value is not a prefix of input")
	}
}

func TestTruncateUserAgent_BoundaryExact(t *testing.T) {
	in := strings.Repeat("B", MaxUserAgentBytes)
	if got := truncateUserAgent(in); got != in {
		t.Errorf("truncateUserAgent at cap = %d bytes; want %d (no change)", len(got), MaxUserAgentBytes)
	}
}

func TestNullable_EmptyReturnsNil(t *testing.T) {
	if got := nullable(""); got != nil {
		t.Errorf("nullable(\"\") = %v; want nil (SQL NULL)", got)
	}
}

func TestNullable_NonEmptyReturnsPointer(t *testing.T) {
	got := nullable("192.0.2.18")
	if got == nil {
		t.Fatal("nullable(non-empty) = nil; want pointer")
	}
	if *got != "192.0.2.18" {
		t.Errorf("*nullable = %q; want \"192.0.2.18\"", *got)
	}
}

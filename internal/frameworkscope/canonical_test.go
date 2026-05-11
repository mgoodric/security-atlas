package frameworkscope_test

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

func TestCanonicalize_DeterministicAcrossKeyOrder(t *testing.T) {
	a := []byte(`{"op":"and","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"in","dim":"data_classification","values":["restricted","confidential"]}]}`)
	b := []byte(`{"args":[{"value":"prod","dim":"environment","op":"eq"},{"values":["restricted","confidential"],"dim":"data_classification","op":"in"}],"op":"and"}`)

	canonA, hashA, err := frameworkscope.Canonicalize(a)
	if err != nil {
		t.Fatalf("canonA: %v", err)
	}
	canonB, hashB, err := frameworkscope.Canonicalize(b)
	if err != nil {
		t.Fatalf("canonB: %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("canonical bytes differ:\n  a=%s\n  b=%s", canonA, canonB)
	}
	if hashA != hashB {
		t.Fatalf("hashes differ: %s vs %s", hashA, hashB)
	}
	if len(hashA) != 64 {
		t.Fatalf("hash length = %d; want 64", len(hashA))
	}
}

func TestCanonicalize_EmptyFormsCollapseToTrue(t *testing.T) {
	want, wantHash, err := frameworkscope.Canonicalize([]byte(`{"op":"true"}`))
	if err != nil {
		t.Fatalf("canonical true: %v", err)
	}
	for _, in := range []string{"", "null", "{}", "true", "  \n"} {
		canon, hash, err := frameworkscope.Canonicalize([]byte(in))
		if err != nil {
			t.Fatalf("Canonicalize(%q): %v", in, err)
		}
		if string(canon) != string(want) {
			t.Fatalf("Canonicalize(%q): canonical = %s; want %s", in, canon, want)
		}
		if hash != wantHash {
			t.Fatalf("Canonicalize(%q): hash mismatch", in)
		}
	}
}

func TestCanonicalize_RejectsMalformed(t *testing.T) {
	_, _, err := frameworkscope.Canonicalize([]byte(`{"op":}`))
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("want malformed error; got %v", err)
	}
}

func TestValidate_RejectsUnknownOperator(t *testing.T) {
	canon, _, err := frameworkscope.Canonicalize([]byte(`{"op":"between","dim":"x","values":["a","b"]}`))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if err := frameworkscope.Validate(canon); err == nil {
		t.Fatalf("want validate error for unknown op; got nil")
	}
}

func TestValidate_AcceptsKnownOperators(t *testing.T) {
	for _, in := range []string{
		`{"op":"true"}`,
		`{"op":"eq","dim":"environment","value":"prod"}`,
		`{"op":"in","dim":"environment","values":["prod","staging"]}`,
		`{"op":"not","arg":{"op":"eq","dim":"x","value":"y"}}`,
		`{"op":"and","args":[{"op":"true"},{"op":"true"}]}`,
		`{"op":"or","args":[{"op":"true"}]}`,
	} {
		canon, _, err := frameworkscope.Canonicalize([]byte(in))
		if err != nil {
			t.Fatalf("canonical %s: %v", in, err)
		}
		if err := frameworkscope.Validate(canon); err != nil {
			t.Fatalf("validate %s: %v", in, err)
		}
	}
}

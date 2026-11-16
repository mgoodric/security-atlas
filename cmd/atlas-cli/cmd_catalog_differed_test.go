package main

import (
	"bytes"
	"strings"
	"testing"
)

// #2008: a frozen version's kept requirements are warnings, one per code.
func TestPrintDifferedWarnings(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printDifferedWarnings(&buf, "2017", []string{"CC6.1", "CC7.2"})
	want := "warning: requirement kept CC6.1: incoming title or body differs, but framework version 2017 is frozen; ship a new version to change it\n" +
		"warning: requirement kept CC7.2: incoming title or body differs, but framework version 2017 is frozen; ship a new version to change it\n"
	if got := buf.String(); got != want {
		t.Fatalf("output =\n%s\nwant\n%s", got, want)
	}

	buf.Reset()
	printDifferedWarnings(&buf, "2017", nil)
	if buf.Len() != 0 {
		t.Fatalf("no differed codes wrote %q", buf.String())
	}
}

// #2008: --strict turns a non-empty differed list into a non-zero exit.
func TestStrictDifferedErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		strict  bool
		codes   []string
		wantErr bool
	}{
		{"strict with differed", true, []string{"CC6.1", "CC7.2"}, true},
		{"strict without differed", true, nil, false},
		{"lenient with differed", false, []string{"CC6.1"}, false},
		{"lenient without differed", false, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := strictDifferedErr(tc.strict, tc.codes)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "2 requirement(s)") {
				t.Fatalf("err = %q, want the count", err)
			}
			if err != nil && !strings.Contains(err.Error(), "CC6.1, CC7.2") {
				t.Fatalf("err = %q, want the codes", err)
			}
		})
	}
}

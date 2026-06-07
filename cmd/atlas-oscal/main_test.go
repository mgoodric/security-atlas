package main

import (
	"reflect"
	"testing"
)

func TestSplitPositional(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		args      []string
		wantFile  string
		wantFlags []string
		wantErr   bool
	}{
		{
			name:      "file first",
			args:      []string{"cat.json", "--tenant-id", "t1", "--json"},
			wantFile:  "cat.json",
			wantFlags: []string{"--tenant-id", "t1", "--json"},
		},
		{
			name:      "file last",
			args:      []string{"--tenant-id", "t1", "--json", "cat.json"},
			wantFile:  "cat.json",
			wantFlags: []string{"--tenant-id", "t1", "--json"},
		},
		{
			name:      "file in the middle",
			args:      []string{"--tenant-id", "t1", "cat.json", "--json"},
			wantFile:  "cat.json",
			wantFlags: []string{"--tenant-id", "t1", "--json"},
		},
		{
			name:      "equals-joined flag",
			args:      []string{"--tenant-id=t1", "cat.json"},
			wantFile:  "cat.json",
			wantFlags: []string{"--tenant-id=t1"},
		},
		{
			name:    "two positionals is an error",
			args:    []string{"a.json", "b.json"},
			wantErr: true,
		},
		{
			name:    "no positional is an error",
			args:    []string{"--tenant-id", "t1"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file, flags, err := splitPositional(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got file=%q flags=%v", file, flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if file != tc.wantFile {
				t.Errorf("file = %q, want %q", file, tc.wantFile)
			}
			if !reflect.DeepEqual(flags, tc.wantFlags) {
				t.Errorf("flags = %v, want %v", flags, tc.wantFlags)
			}
		})
	}
}

func TestContainsEquals(t *testing.T) {
	t.Parallel()
	if !containsEquals("--a=b") {
		t.Error("--a=b should contain '='")
	}
	if containsEquals("--json") {
		t.Error("--json should not contain '='")
	}
}

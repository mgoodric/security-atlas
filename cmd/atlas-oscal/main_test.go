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

func TestSplitPositional_RepeatableCatalogFlag(t *testing.T) {
	t.Parallel()
	// import-profile's repeatable --catalog flag rides through splitPositional
	// as a sequence of `--catalog <value>` flag tokens; the single positional
	// <profile-file> is still extracted from any position.
	args := []string{"--catalog", "c1.json", "prof.json", "--catalog", "c2.json", "--json"}
	file, flags, err := splitPositional(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if file != "prof.json" {
		t.Errorf("file = %q, want prof.json", file)
	}
	want := []string{"--catalog", "c1.json", "--catalog", "c2.json", "--json"}
	if !reflect.DeepEqual(flags, want) {
		t.Errorf("flags = %v, want %v", flags, want)
	}
}

func TestStringSlice(t *testing.T) {
	t.Parallel()
	var s stringSlice
	if err := s.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b"); err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Errorf("stringSlice = %v, want [a b]", []string(s))
	}
	if s.String() == "" {
		t.Error("String() should render the slice")
	}
}

func TestRunImportProfile_RequiresCatalog(t *testing.T) {
	t.Parallel()
	// With a DSN + tenant supplied via flags but no --catalog, the command
	// errors before any bridge / DB contact (the Go-side gate).
	err := runImportProfile([]string{
		"prof.json",
		"--dsn", "postgres://x",
		"--tenant-id", "11111111-1111-4111-8111-111111111111",
	})
	if err == nil {
		t.Fatal("expected an error when no --catalog is supplied")
	}
}

func TestRunImportProfile_RequiresTenant(t *testing.T) {
	t.Parallel()
	err := runImportProfile([]string{
		"prof.json",
		"--dsn", "postgres://x",
		"--catalog", "c.json",
	})
	if err == nil {
		t.Fatal("expected an error when --tenant-id is missing")
	}
}

func TestRunImportComponentDefinition_RequiresTenant(t *testing.T) {
	t.Parallel()
	// With a DSN supplied via flag but no --tenant-id, the command errors
	// before any bridge / DB contact (the Go-side gate).
	err := runImportComponentDefinition([]string{
		"compdef.json",
		"--dsn", "postgres://x",
	})
	if err == nil {
		t.Fatal("expected an error when --tenant-id is missing")
	}
}

func TestRunImportComponentDefinition_RequiresFile(t *testing.T) {
	t.Parallel()
	// No positional <file> argument — splitPositional rejects it.
	err := runImportComponentDefinition([]string{
		"--dsn", "postgres://x",
		"--tenant-id", "11111111-1111-4111-8111-111111111111",
	})
	if err == nil {
		t.Fatal("expected an error when no <file> argument is supplied")
	}
}

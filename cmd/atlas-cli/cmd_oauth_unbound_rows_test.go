package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// Tests for security-atlas#1876: `atlas-cli oauth unbound-rows`. The
// listing and purge run against real Postgres in internal/auth/oauthclient's
// integration suite; here the flag guards and the output run without one.

func TestValidateUnboundRowsFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		purge      bool
		confirmSet bool
		confirm    int64
		wantErr    string
	}{
		{name: "list only", wantErr: ""},
		{name: "purge with confirmation", purge: true, confirmSet: true, confirm: 3},
		{name: "purge without confirmation refused", purge: true, wantErr: "--purge requires --confirm-rows"},
		{name: "confirmation without purge refused", confirmSet: true, confirm: 3, wantErr: "only applies with --purge"},
		{name: "zero confirmation refused", purge: true, confirmSet: true, confirm: 0, wantErr: "positive"},
		{name: "negative confirmation refused", purge: true, confirmSet: true, confirm: -1, wantErr: "positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateUnboundRowsFlags(tc.purge, tc.confirmSet, tc.confirm)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestWriteUnboundListing(t *testing.T) {
	t.Parallel()
	var empty bytes.Buffer
	if err := writeUnboundListing(&empty, nil); err != nil {
		t.Fatal(err)
	}
	if got := empty.String(); got != "no rows in the all-zeros tenant\n" {
		t.Errorf("empty listing = %q", got)
	}

	var out bytes.Buffer
	counts := []oauthclient.UnboundTableCount{{Table: "controls", Rows: 2}, {Table: "evidence_records", Rows: 5}}
	if err := writeUnboundListing(&out, counts); err != nil {
		t.Fatal(err)
	}
	want := "controls\t2\nevidence_records\t5\ntotal\t7\nto delete them: atlas-cli oauth unbound-rows --purge --confirm-rows 7\n"
	if got := out.String(); got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

func TestWriteUnboundPurge(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	purged := []oauthclient.UnboundTableCount{{Table: "controls", Rows: 2}, {Table: "evidence_records", Rows: 5}}
	if err := writeUnboundPurge(&out, purged); err != nil {
		t.Fatal(err)
	}
	want := "purged\tcontrols\t2\npurged\tevidence_records\t5\npurged total\t7\n"
	if got := out.String(); got != want {
		t.Errorf("purge log = %q, want %q", got, want)
	}
}

func TestUnboundRowsCmd_RefusesPurgeWithoutConfirmBeforeDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	cmd := newOAuthUnboundRowsCmd()
	cmd.SetArgs([]string{"--purge"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--confirm-rows") {
		t.Fatalf("err = %v, want the --confirm-rows refusal (before DATABASE_URL is checked)", err)
	}
}

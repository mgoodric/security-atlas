package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

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
	counts := []oauthclient.UnboundTableCount{
		{Table: "controls", Rows: 2},
		{Table: "evidence_records", Rows: 5, Retained: oauthclient.RetainedLedger},
		{Table: "risks", Rows: 1},
	}
	if err := writeUnboundListing(&out, counts); err != nil {
		t.Fatal(err)
	}
	want := "controls\t2\n" +
		"evidence_records\t5\tretained: append-only ledger (ADR-0012, tombstone only)\n" +
		"risks\t1\n" +
		"purgeable total\t3\nto delete them: atlas-cli oauth unbound-rows --purge --confirm-rows 3\n"
	if got := out.String(); got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}

	var retainedOnly bytes.Buffer
	if err := writeUnboundListing(&retainedOnly, counts[1:2]); err != nil {
		t.Fatal(err)
	}
	if got := retainedOnly.String(); !strings.HasSuffix(got, "purgeable total\t0\nnothing to purge: every listed row is retained\n") {
		t.Errorf("retained-only listing = %q", got)
	}
}

func TestWriteUnboundPurge(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	id := uuid.MustParse("0e3b4a7e-4ad8-4c14-9c9a-6f8e2b13c92d")
	res := oauthclient.PurgeResult{
		AuditID:  id,
		Purged:   []oauthclient.UnboundTableCount{{Table: "controls", Rows: 2}, {Table: "risks", Rows: 5}},
		Retained: []oauthclient.UnboundTableCount{{Table: "evidence_records", Rows: 4, Retained: oauthclient.RetainedLedger}},
	}
	if err := writeUnboundPurge(&out, res); err != nil {
		t.Fatal(err)
	}
	want := "purged\tcontrols\t2\npurged\trisks\t5\n" +
		"kept\tevidence_records\t4\tretained: append-only ledger (ADR-0012, tombstone only)\n" +
		"purged total\t7\nrecorded in tenant_data_purges\t" + id.String() + "\n"
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

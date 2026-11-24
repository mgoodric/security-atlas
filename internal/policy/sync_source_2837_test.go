package policy

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2837: a synced policy names the system it comes from, read from its
// sync site, so a SharePoint policy is not called a Confluence one.
func TestSyncSourceName(t *testing.T) {
	t.Parallel()
	for site, want := range map[string]string{
		"https://contoso.sharepoint.com/sites/Policies": SyncSourceSharePoint,
		"https://Contoso.SharePoint.com":                SyncSourceSharePoint,
		" https://contoso.sharepoint.com/ ":             SyncSourceSharePoint,
		"contoso.sharepoint.com/sites/Policies":         SyncSourceSharePoint,
		"https://acme.atlassian.net":                    SyncSourceConfluence,
		"acme.atlassian.net":                            SyncSourceConfluence,
		"https://wiki.example.com":                      SyncSourceConfluence,
		"https://sharepoint.com.evil.example":           SyncSourceConfluence,
		"https://notsharepoint.com":                     SyncSourceConfluence,
		"":                                              SyncSourceConfluence,
		"://bad":                                        SyncSourceConfluence,
	} {
		if got := SyncSourceName(site); got != want {
			t.Errorf("SyncSourceName(%q) = %q, want %q", site, got, want)
		}
	}
}

func TestManagedBySyncError(t *testing.T) {
	t.Parallel()
	sp := "https://contoso.sharepoint.com/sites/Policies"
	err := managedBySync(dbx.Policy{SourceAttribution: SourceSynced, SyncSite: &sp})
	if !errors.Is(err, ErrManagedBySync) {
		t.Fatalf("errors.Is(%v, ErrManagedBySync) = false", err)
	}
	if got, want := err.Error(), "policy: managed in SharePoint; edit it at its source page"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if src, ok := SyncSourceOf(fmt.Errorf("wrapped: %w", err)); !ok || src != SyncSourceSharePoint {
		t.Fatalf("SyncSourceOf(wrapped) = %q, %v", src, ok)
	}
	if src, ok := SyncSourceOf(managedBySync(dbx.Policy{})); !ok || src != SyncSourceConfluence {
		t.Fatalf("SyncSourceOf(no site) = %q, %v", src, ok)
	}
	if src, ok := SyncSourceOf(ErrManagedBySync); !ok || src != SyncSourceConfluence {
		t.Fatalf("SyncSourceOf(sentinel) = %q, %v", src, ok)
	}
	if _, ok := SyncSourceOf(errors.New("other")); ok {
		t.Fatal("SyncSourceOf(other) matched")
	}
}

func TestPolicyFromRowSyncSource(t *testing.T) {
	t.Parallel()
	sp := "https://contoso.sharepoint.com/sites/Policies"
	if got := policyFromRow(dbx.Policy{SourceAttribution: SourceSynced, SyncSite: &sp}).SyncSource; got != SyncSourceSharePoint {
		t.Fatalf("synced SharePoint row: SyncSource = %q", got)
	}
	if got := policyFromRow(dbx.Policy{SourceAttribution: SourceImported}).SyncSource; got != "" {
		t.Fatalf("imported row: SyncSource = %q, want empty", got)
	}
}

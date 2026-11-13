//go:build integration

// #1892: the bootstrap-recovery marker and issuer survive a restart, so a
// reset or sign-in after a restart still finds and revokes the recovery
// key by marker and id. Reuses the pools and hasher from TestMain in
// integration_test.go.
package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// bootStore is one simulated process: a fresh credstore rehydrated from
// api_keys.
func bootStore(t *testing.T) *credstore.Store {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the load-at-boot scan needs the BYPASSRLS pool")
	}
	s := credstore.New(time.Hour)
	if _, err := s.AttachPersistence(apikeystore.NewPersister(appPool, adminPool, testHasher)); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	return s
}

func TestCredPersist_RecoveryMarkerSurvivesRestart(t *testing.T) {
	tenant := uuid.New().String()
	issuer := uuid.New().String()

	atlas1 := bootStore(t)
	minted, bearer, err := atlas1.IssueRecoveryAdmin(tenant, 24*time.Hour, issuer)
	if err != nil {
		t.Fatalf("IssueRecoveryAdmin: %v", err)
	}
	_, ordinaryBearer, err := atlas1.IssueAdmin(tenant, 0)
	if err != nil {
		t.Fatalf("IssueAdmin: %v", err)
	}

	// Restart: the marker, issuer and TTL come back from api_keys.
	atlas2 := bootStore(t)
	got, err := atlas2.Authenticate(bearer)
	if err != nil {
		t.Fatalf("recovery key after restart: %v", err)
	}
	if got.ID != minted.ID || got.Purpose != credstore.PurposeBootstrapRecovery ||
		got.UserID != issuer || got.TTL != 24*time.Hour || !got.IsAdmin {
		t.Fatalf("reloaded credential = %+v; want id %s, recovery marker, issuer %s, 24h, admin",
			got, minted.ID, issuer)
	}

	// The next reset (or sign-in) after the restart revokes it by marker.
	revoked, err := atlas2.RevokePurpose(credstore.PurposeBootstrapRecovery, "")
	if err != nil {
		t.Fatalf("RevokePurpose: %v", err)
	}
	found := false
	for _, id := range revoked {
		if id == minted.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("RevokePurpose after restart revoked %v; want it to include %s", revoked, minted.ID)
	}

	// The revoke is durable, and the ordinary key was not touched.
	atlas3 := bootStore(t)
	if _, err := atlas3.Authenticate(bearer); !errors.Is(err, credstore.ErrUnknownKey) {
		t.Fatalf("revoked recovery key after second restart: err = %v; want ErrUnknownKey", err)
	}
	if _, err := atlas3.Authenticate(ordinaryBearer); err != nil {
		t.Fatalf("ordinary admin key revoked: %v", err)
	}
}

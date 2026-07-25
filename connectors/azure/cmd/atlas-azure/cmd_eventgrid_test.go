// Seam + adapter tests for the `eventgrid` (subscribe) subcommand. The sdk client
// constructor and the blocking Serve loop are swapped for fakes; the Rereader
// routing + filter + dedup behaviour is exercised through the package read seams
// (installSeams). No live Azure — synthetic events; neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/eventgrid"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// resolveTestCred builds a no-op client-credentials Credential from the okEnv()
// environment. Every reader call is seamed, so the credential never authenticates
// against live Azure.
func resolveTestCred(t *testing.T) (azureauth.Credential, error) {
	t.Helper()
	return azureauth.Resolve(azureauth.ResolveOpts{Mode: azureauth.ModeClientCredentials, TenantID: "tenant-1", ClientID: "client-1"})
}

func mustResolveTestCred(t *testing.T) azureauth.Credential {
	t.Helper()
	okEnv(t)
	cred, err := resolveTestCred(t)
	if err != nil {
		t.Fatalf("resolve cred: %v", err)
	}
	return cred
}

// keyRecordingClient captures the idempotency key of the last pushed record so a
// test can assert the subscribe path derives the SAME key as the pull profile.
type keyRecordingClient struct {
	lastKey string
	pushed  int
}

func (c *keyRecordingClient) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	c.lastKey = rec.GetIdempotencyKey()
	c.pushed++
	return &evidencev1.EvidenceReceipt{}, nil
}

func (c *keyRecordingClient) Close() error { return nil }

const testEGDeliveryKey = "test-eventgrid-delivery-key-not-real"

func installEventGridSeams(t *testing.T, newClient func(string, string, ...sdk.Option) (sdkPushClient, error), serve func(context.Context, *http.Server) error) {
	t.Helper()
	if newClient != nil {
		prev := newEventGridSDKClient
		newEventGridSDKClient = newClient
		t.Cleanup(func() { newEventGridSDKClient = prev })
	}
	if serve != nil {
		prev := eventGridServe
		eventGridServe = serve
		t.Cleanup(func() { eventGridServe = prev })
	}
}

func okEventGridFlags() eventGridFlags {
	return eventGridFlags{
		environment:     "prod",
		authMode:        "client-credentials",
		subscriptionID:  "sub-1",
		listen:          "127.0.0.1:0",
		path:            "/webhooks/azure/eventgrid",
		credentialIn:    "header",
		credentialName:  "Authorization",
		entraControl:    "scf:IAC-21",
		storageControl:  "scf:CRY-04",
		aksControl:      "scf:CFG-02",
		nsgControl:      "scf:NET-04",
		keyvaultControl: "scf:CRY-09",
		firewallControl: "scf:NET-04",
	}
}

func TestDoEventGrid_Success(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	t.Setenv(deliveryKeyEnv, testEGDeliveryKey)
	installSeams(t, seamOverrides{})

	fake := &fakeSDKClient{}
	var served bool
	installEventGridSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		func(_ context.Context, _ *http.Server) error { served = true; return nil },
	)
	if err := doEventGrid(context.Background(), okEventGridFlags()); err != nil {
		t.Fatalf("doEventGrid: %v", err)
	}
	if !served {
		t.Error("Serve seam not invoked")
	}
	if !fake.closeCalled {
		t.Error("sdk client Close not called")
	}
}

func TestDoEventGrid_MissingDeliveryKey(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	t.Setenv(deliveryKeyEnv, "")
	err := doEventGrid(context.Background(), okEventGridFlags())
	if err == nil || !strings.Contains(err.Error(), deliveryKeyEnv) {
		t.Fatalf("want delivery-key error; got %v", err)
	}
}

func TestDoEventGrid_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	t.Setenv(deliveryKeyEnv, testEGDeliveryKey)
	sentinel := errors.New("bad endpoint")
	installEventGridSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
		nil,
	)
	err := doEventGrid(context.Background(), okEventGridFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoEventGrid_ServeError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	t.Setenv(deliveryKeyEnv, testEGDeliveryKey)
	installSeams(t, seamOverrides{})
	sentinel := errors.New("bind failed")
	installEventGridSeams(t,
		func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
		func(_ context.Context, _ *http.Server) error { return sentinel },
	)
	err := doEventGrid(context.Background(), okEventGridFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "serve: ") {
		t.Fatalf("want wrapped serve error; got %v", err)
	}
}

func TestNewEventGridCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newEventGridCmd()
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

func TestNewEventGridCmd_PreRunRejectsMissingSubscription(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	cmd := newEventGridCmd()
	_ = cmd.Flags().Set("environment", "prod")
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "subscription-id") {
		t.Fatalf("want subscription-id error; got %v", err)
	}
}

func TestNewEventGridCmd_PreRunRejectsBadCredentialIn(t *testing.T) {
	resetCommon(t)
	cmd := newEventGridCmd()
	_ = cmd.Flags().Set("environment", "prod")
	_ = cmd.Flags().Set("subscription-id", "sub-1")
	_ = cmd.Flags().Set("credential-in", "bogus")
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "credential-in") {
		t.Fatalf("want credential-in error; got %v", err)
	}
}

func TestParseCredentialLocation(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"header", ""} {
		if loc, err := parseCredentialLocation(in); err != nil || loc != eventgrid.CredentialHeader {
			t.Fatalf("parseCredentialLocation(%q) = %v,%v", in, loc, err)
		}
	}
	if loc, err := parseCredentialLocation("query"); err != nil || loc != eventgrid.CredentialQuery {
		t.Fatalf("query = %v,%v", loc, err)
	}
	if _, err := parseCredentialLocation("bogus"); err == nil {
		t.Fatal("want error on bogus location")
	}
}

func TestProfilesSupported_PullAndSubscribe(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"pull": true, "subscribe": true}
	if len(ProfilesSupported) != len(want) {
		t.Fatalf("ProfilesSupported = %v; want pull+subscribe", ProfilesSupported)
	}
	for _, p := range ProfilesSupported {
		if !want[p] {
			t.Errorf("unexpected profile %q", p)
		}
		if strings.Contains(strings.ToLower(p), "continuous") {
			t.Errorf("profile %q must not claim continuous monitoring", p)
		}
	}
}

// --- Rereader routing + filter + no-fabrication tests ---

const testEGStorageID = "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct1"

func newTestReread(t *testing.T, fake *fakeSDKClient) eventgrid.Rereader {
	t.Helper()
	okEnv(t)
	// A no-op credential is enough — every reader call is seamed.
	cred, err := resolveTestCred(t)
	if err != nil {
		t.Fatalf("resolve cred: %v", err)
	}
	return newReread(cred, fake, okEventGridFlags())
}

// AC-2: a storage event re-reads via the storage reader and emits ONLY the record
// for the changed resource id (the filter drops other accounts).
func TestReread_Storage_EmitsOnlyChangedResource(t *testing.T) {
	resetCommon(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{
				{AccountID: testEGStorageID, AccountName: "acct1", SubscriptionID: sub, EncryptionEnabled: true, HTTPSTrafficOnly: true, ObservedAt: time.Now().UTC()},
				{AccountID: "/subscriptions/sub-1/providers/Microsoft.Storage/storageAccounts/other", AccountName: "other", SubscriptionID: sub, ObservedAt: time.Now().UTC()},
			}, nil
		},
	})
	reread := newTestReread(t, fake)

	pushed, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if pushed != 1 {
		t.Fatalf("pushed = %d, want 1 (filtered to the changed resource)", pushed)
	}
	if fake.pushed != 1 {
		t.Fatalf("fake.pushed = %d, want 1", fake.pushed)
	}
}

// AC-5 / P0-522-1 (no-fabrication): an event whose resource id resolves to NO real
// resource on re-read produces ZERO records — the event payload is never a data
// source.
func TestReread_NoFabrication_UnresolvedResource(t *testing.T) {
	resetCommon(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			// The re-read returns OTHER accounts but not the event's id.
			return []storage.AccountConfig{
				{AccountID: "/subscriptions/sub-1/providers/Microsoft.Storage/storageAccounts/other", AccountName: "other", SubscriptionID: sub, ObservedAt: time.Now().UTC()},
			}, nil
		},
	})
	reread := newTestReread(t, fake)

	pushed, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if pushed != 0 {
		t.Fatalf("pushed = %d, want 0 (no fabrication — resource not found on re-read)", pushed)
	}
	if fake.pushed != 0 {
		t.Fatalf("fake.pushed = %d, want 0", fake.pushed)
	}
}

// AC-4 (cross-profile dedup): the subscribe-emitted record for a resource carries
// the SAME idempotency key the pull profile's builder derives for that resource in
// the same hour — they collapse to one ledger row.
func TestReread_DedupKeyMatchesPullProfile(t *testing.T) {
	resetCommon(t)
	acct := storage.AccountConfig{
		AccountID: testEGStorageID, AccountName: "acct1", SubscriptionID: "sub-1",
		EncryptionEnabled: true, HTTPSTrafficOnly: true, ObservedAt: time.Now().UTC(),
	}
	// The pull profile's record for this account:
	pullRec, err := buildStorageRecord(acct, "prod", "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildStorageRecord: %v", err)
	}

	// The subscribe path re-reads the SAME account and builds via the SAME builder.
	var subscribeKey string
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, _ string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{acct}, nil
		},
	})
	// Capture the pushed record's key via a recording client.
	rc := &keyRecordingClient{}
	reread := newReread(mustResolveTestCred(t), rc, okEventGridFlags())
	if _, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID); err != nil {
		t.Fatalf("reread: %v", err)
	}
	subscribeKey = rc.lastKey
	_ = fake

	if subscribeKey == "" {
		t.Fatal("subscribe path pushed no record")
	}
	if subscribeKey != pullRec.GetIdempotencyKey() {
		t.Fatalf("subscribe key %q != pull key %q — would NOT dedup", subscribeKey, pullRec.GetIdempotencyKey())
	}
}

// AC-2: routing covers every in-scope reader (each routes without error to its
// reader; the seamed readers return empty so pushed=0, but no routing error).
func TestReread_RoutesEveryResourceType(t *testing.T) {
	resetCommon(t)
	installSeams(t, seamOverrides{
		entraPull: func(_ context.Context, _ entra.API, _ string, _ func() time.Time) ([]entra.Assignment, error) {
			return nil, nil
		},
		storageScan: func(_ context.Context, _ storage.API, _ string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return nil, nil
		},
	})
	reread := newTestReread(t, &fakeSDKClient{})
	for _, rt := range []eventgrid.ResourceType{
		eventgrid.ResourceStorage, eventgrid.ResourceAKS, eventgrid.ResourceNSG,
		eventgrid.ResourceKeyVault, eventgrid.ResourceFirewall, eventgrid.ResourceEntra,
	} {
		if _, err := reread(context.Background(), rt, "/some/id"); err != nil {
			t.Errorf("route %q errored: %v", rt, err)
		}
	}
	// An unmapped type is a no-op (the receiver never sends one; fail closed).
	if pushed, err := reread(context.Background(), eventgrid.ResourceUnknown, "/x"); err != nil || pushed != 0 {
		t.Errorf("unmapped route = %d,%v; want 0,nil", pushed, err)
	}
}

// A token-acquisition failure surfaces as an error (the generic helper's first
// branch).
func TestReread_TokenError(t *testing.T) {
	resetCommon(t)
	installSeams(t, seamOverrides{
		acquire: func(_ context.Context, _ azureauth.Credential, _ *http.Client, _ string) (string, error) {
			return "", errors.New("token boom")
		},
	})
	reread := newTestReread(t, &fakeSDKClient{})
	if _, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID); err == nil || !strings.Contains(err.Error(), "acquire token") {
		t.Fatalf("want acquire-token error; got %v", err)
	}
}

// A reader (scan) failure surfaces as a re-read error.
func TestReread_ScanError(t *testing.T) {
	resetCommon(t)
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, _ string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return nil, errors.New("scan boom")
		},
	})
	reread := newTestReread(t, &fakeSDKClient{})
	if _, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID); err == nil || !strings.Contains(err.Error(), "re-read") {
		t.Fatalf("want re-read error; got %v", err)
	}
}

// A push failure on the matched resource surfaces as a push error.
func TestReread_PushError(t *testing.T) {
	resetCommon(t)
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{
				{AccountID: testEGStorageID, AccountName: "acct1", SubscriptionID: sub, EncryptionEnabled: true, HTTPSTrafficOnly: true, ObservedAt: time.Now().UTC()},
			}, nil
		},
	})
	fake := &fakeSDKClient{pushErr: errors.New("push boom")}
	reread := newTestReread(t, fake)
	if _, err := reread(context.Background(), eventgrid.ResourceStorage, testEGStorageID); err == nil || !strings.Contains(err.Error(), "push record") {
		t.Fatalf("want push error; got %v", err)
	}
}

// AC-2 (non-storage happy path): an AKS event re-reads via the aks reader and emits
// the matched cluster — exercises the generic helper for a second kind.
func TestReread_AKS_EmitsMatchedCluster(t *testing.T) {
	resetCommon(t)
	aksID := "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/c1"
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		aksScan: func(_ context.Context, _ aks.API, sub string, _ func() time.Time) ([]aks.ClusterConfig, error) {
			return []aks.ClusterConfig{
				{ClusterID: aksID, ClusterName: "c1", SubscriptionID: sub, ObservedAt: time.Now().UTC()},
			}, nil
		},
	})
	reread := newTestReread(t, fake)
	pushed, err := reread(context.Background(), eventgrid.ResourceAKS, aksID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if pushed != 1 || fake.pushed != 1 {
		t.Fatalf("pushed = %d / fake.pushed = %d, want 1/1", pushed, fake.pushed)
	}
}

// Seam tests for doRun. All Azure reads + token acquisition + the sdk client
// constructor are swapped for fakes so doRun is exercised end-to-end without
// touching live Azure or a real platform. Seams are restored via t.Cleanup.
//
// No real Azure secrets or vendor-prefixed tokens in fixtures — neutral
// "test-*" strings only.
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

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// fakeSDKClient is a minimal sdkPushClient.
type fakeSDKClient struct {
	pushErr     error
	pushed      int
	closeCalled bool
}

func (f *fakeSDKClient) Push(_ context.Context, _ *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	f.pushed++
	return &evidencev1.EvidenceReceipt{}, nil
}

func (f *fakeSDKClient) Close() error { f.closeCalled = true; return nil }

type seamOverrides struct {
	entraPull   func(ctx context.Context, api entra.API, tenantID string, now func() time.Time) ([]entra.Assignment, error)
	storageScan func(ctx context.Context, api storage.API, subscriptionID string, now func() time.Time) ([]storage.AccountConfig, error)
	acquire     func(ctx context.Context, cred azureauth.Credential, hc *http.Client, scope string) (string, error)
	newClient   func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	// acquireToken: default to a no-op success so storage/entra fakes drive
	// the body unless the test overrides.
	prevAcq := acquireToken
	acquireToken = func(ctx context.Context, cred azureauth.Credential, hc *http.Client, scope string) (string, error) {
		return "test-access-token", nil
	}
	t.Cleanup(func() { acquireToken = prevAcq })
	if o.acquire != nil {
		acquireToken = o.acquire
	}
	if o.entraPull != nil {
		prev := entraPull
		entraPull = o.entraPull
		t.Cleanup(func() { entraPull = prev })
	}
	if o.storageScan != nil {
		prev := storageScan
		storageScan = o.storageScan
		t.Cleanup(func() { storageScan = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_TENANT_ID", "tenant-1")
	t.Setenv("AZURE_CLIENT_ID", "client-1")
	t.Setenv("AZURE_CLIENT_SECRET", "test-azure-client-secret")
}

func okFlags() runFlags {
	return runFlags{
		environment:    "prod",
		authMode:       "client-credentials",
		subscriptionID: "sub-1",
		entraControl:   "scf:IAC-21",
		storageControl: "scf:CRY-04",
	}
}

func TestDoRun_PushSuccessBothKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		entraPull: func(_ context.Context, _ entra.API, tenantID string, _ func() time.Time) ([]entra.Assignment, error) {
			return []entra.Assignment{
				{AssignmentID: "ra-1", PrincipalID: "p", PrincipalType: "user", RoleDefinitionID: "r", TenantID: tenantID, ObservedAt: time.Now().UTC()},
			}, nil
		},
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{
				{AccountID: "/sub/a", AccountName: "a", SubscriptionID: sub, EncryptionEnabled: true, HTTPSTrafficOnly: true, Result: storage.ResultPass, ObservedAt: time.Now().UTC()},
			}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})

	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2 (one entra + one storage)", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRun_SkipEntra(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{{AccountID: "/a", AccountName: "a", SubscriptionID: sub, Result: storage.ResultFail, ObservedAt: time.Now().UTC()}}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipEntra = true
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 1 {
		t.Errorf("pushed = %d; want 1", fake.pushed)
	}
}

func TestDoRun_SkipStorage(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		entraPull: func(_ context.Context, _ entra.API, tenantID string, _ func() time.Time) ([]entra.Assignment, error) {
			return []entra.Assignment{{AssignmentID: "ra", PrincipalID: "p", PrincipalType: "user", RoleDefinitionID: "r", TenantID: tenantID, ObservedAt: time.Now().UTC()}}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipStorage = true
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 1 {
		t.Errorf("pushed = %d; want 1", fake.pushed)
	}
}

func TestDoRun_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("bad endpoint")
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoRun_GraphTokenError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("token 401")
	installSeams(t, seamOverrides{
		acquire: func(_ context.Context, _ azureauth.Credential, _ *http.Client, _ string) (string, error) {
			return "", sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "graph token: ") {
		t.Fatalf("want wrapped graph token error; got %v", err)
	}
}

func TestDoRun_EntraPullError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("graph 403")
	installSeams(t, seamOverrides{
		entraPull: func(_ context.Context, _ entra.API, _ string, _ func() time.Time) ([]entra.Assignment, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "entra pull: ") {
		t.Fatalf("want wrapped entra pull error; got %v", err)
	}
}

func TestDoRun_StoragePushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		storageScan: func(_ context.Context, _ storage.API, sub string, _ func() time.Time) ([]storage.AccountConfig, error) {
			return []storage.AccountConfig{{AccountID: "/a", AccountName: "a", SubscriptionID: sub, Result: storage.ResultPass, ObservedAt: time.Now().UTC()}}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipEntra = true
	err := doRun(context.Background(), f)
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push storage ") {
		t.Fatalf("want wrapped push storage error; got %v", err)
	}
}

func TestDoRun_BadAuthModeRejected(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	f := okFlags()
	f.authMode = "bogus"
	if err := doRun(context.Background(), f); err == nil {
		t.Fatal("expected bad auth-mode error")
	}
}

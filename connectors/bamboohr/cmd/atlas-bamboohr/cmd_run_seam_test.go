// Seam tests for doRun. The BambooHR read + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching live
// BambooHR or a real platform. Seams are restored via t.Cleanup.
//
// No real BambooHR credentials in fixtures — neutral "test-*" / "fake-*" strings
// only.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/workers"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

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
	collect   func(ctx context.Context, api workers.API) ([]worker.RawWorker, error)
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.collect != nil {
		prev := workersCollect
		workersCollect = o.collect
		t.Cleanup(func() { workersCollect = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BAMBOOHR_API_KEY", "fake-bamboo-secret")
	t.Setenv("BAMBOOHR_COMPANY_DOMAIN", "acme")
	t.Setenv("BAMBOOHR_BASE_URL", "https://api.bamboohr.example")
}

func okFlags() runFlags {
	return runFlags{environment: "prod", workerControl: "scf:IAC-22"}
}

func twoWorkers() []worker.RawWorker {
	return []worker.RawWorker{
		{WorkerID: "1", Status: worker.StatusActive, Title: "SWE", Department: "Eng"},
		{WorkerID: "2", Status: worker.StatusTerminated},
	}
}

func TestDoRun_PushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		collect:   func(_ context.Context, _ workers.API) ([]worker.RawWorker, error) { return twoWorkers(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRun_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("BAMBOOHR_API_KEY", "")
	t.Setenv("BAMBOOHR_COMPANY_DOMAIN", "")
	err := doRun(context.Background(), okFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
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

func TestDoRun_CollectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("401")
	installSeams(t, seamOverrides{
		collect:   func(_ context.Context, _ workers.API) ([]worker.RawWorker, error) { return nil, sentinel },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "bamboohr collect: ") {
		t.Fatalf("want wrapped collect error; got %v", err)
	}
}

func TestDoRun_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		collect:   func(_ context.Context, _ workers.API) ([]worker.RawWorker, error) { return twoWorkers(), nil },
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push worker ") {
		t.Fatalf("want wrapped push error; got %v", err)
	}
}

func TestNewWorkersAPI_Constructor(t *testing.T) {
	if newWorkersAPI(http.DefaultClient, "https://api.bamboohr.example", "acme", "fake-bamboo-secret") == nil {
		t.Error("newWorkersAPI returned nil")
	}
}

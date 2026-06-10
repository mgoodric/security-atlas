// Tests for the event-driven (subscribe) profile glue (slice 573). The
// long-lived receiver's Serve is seamed so doSubscribe is exercised end-to-end
// without binding a real port for the test's lifetime.
//
// No real Rippling credentials / secrets in fixtures — neutral "test-*" strings.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/rippling/internal/workers"
)

func installServeSeam(t *testing.T, fn func(ctx context.Context, srv *http.Server) error) {
	t.Helper()
	prev := serveReceiver
	serveReceiver = fn
	t.Cleanup(func() { serveReceiver = prev })
}

func installOneAPISeam(t *testing.T, api workers.OneAPI) {
	t.Helper()
	prev := newWorkersOneAPI
	newWorkersOneAPI = func(_ *http.Client, _, _ string) workers.OneAPI { return api }
	t.Cleanup(func() { newWorkersOneAPI = prev })
}

func okSubscribeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RIPPLING_API_TOKEN", "test-rippling-key")
	t.Setenv("RIPPLING_BASE_URL", "https://api.rippling.example")
	t.Setenv("RIPPLING_WEBHOOK_SECRET", "test-webhook-secret")
}

func okSubscribeFlags() subscribeFlags {
	return subscribeFlags{environment: "prod", workerControl: "scf:IAC-22", listenAddr: "127.0.0.1:0", path: "/hooks/rippling"}
}

func TestDoSubscribe_BuildsReceiverAndServes(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okSubscribeEnv(t)

	served := false
	installServeSeam(t, func(_ context.Context, srv *http.Server) error {
		served = true
		if srv.ReadHeaderTimeout == 0 {
			t.Error("server missing ReadHeaderTimeout (gosec G112)")
		}
		return nil
	})
	installOneAPISeam(t, fakeOne{})
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})

	if err := doSubscribe(context.Background(), okSubscribeFlags()); err != nil {
		t.Fatalf("doSubscribe: %v", err)
	}
	if !served {
		t.Error("serveReceiver not invoked")
	}
}

func TestDoSubscribe_MissingWebhookSecret(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("RIPPLING_API_TOKEN", "test-rippling-key")
	t.Setenv("RIPPLING_WEBHOOK_SECRET", "")
	err := doSubscribe(context.Background(), okSubscribeFlags())
	if err == nil || !strings.Contains(err.Error(), "webhook secret") {
		t.Fatalf("want webhook-secret error; got %v", err)
	}
}

func TestDoSubscribe_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("RIPPLING_API_TOKEN", "")
	err := doSubscribe(context.Background(), okSubscribeFlags())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("want auth error; got %v", err)
	}
}

func TestDoSubscribe_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okSubscribeEnv(t)
	sentinel := errors.New("bad endpoint")
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel },
	})
	err := doSubscribe(context.Background(), okSubscribeFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

func TestDoSubscribe_ServeErrorPropagates(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okSubscribeEnv(t)
	sentinel := errors.New("listen failed")
	installServeSeam(t, func(_ context.Context, _ *http.Server) error { return sentinel })
	installOneAPISeam(t, fakeOne{})
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	if err := doSubscribe(context.Background(), okSubscribeFlags()); !errors.Is(err, sentinel) {
		t.Fatalf("want serve error; got %v", err)
	}
}

func TestRipplingParser_ExtractsEmployeeID(t *testing.T) {
	id, ok, err := ripplingParser{}.ParseWorkerID([]byte(`{"event":"employee.terminated","data":{"employeeId":"emp-7"}}`))
	if err != nil || !ok || id != "emp-7" {
		t.Fatalf("ParseWorkerID = %q ok=%v err=%v", id, ok, err)
	}
}

func TestRipplingParser_FallsBackToID(t *testing.T) {
	id, ok, _ := ripplingParser{}.ParseWorkerID([]byte(`{"data":{"id":"emp-9"}}`))
	if !ok || id != "emp-9" {
		t.Errorf("fallback id = %q ok=%v", id, ok)
	}
}

func TestRipplingParser_NoWorker(t *testing.T) {
	_, ok, err := ripplingParser{}.ParseWorkerID([]byte(`{"event":"unrelated","data":{}}`))
	if err != nil || ok {
		t.Errorf("no-worker: ok=%v err=%v", ok, err)
	}
}

func TestRipplingParser_BadJSON(t *testing.T) {
	_, _, err := ripplingParser{}.ParseWorkerID([]byte(`not-json`))
	if err == nil {
		t.Fatal("want parse error on bad json")
	}
}

func TestRipplingFetcher_DelegatesToFetchOne(t *testing.T) {
	f := ripplingFetcher{api: fakeOne{raw: workers.RawWorker{ID: "w1", EmploymentStatus: "TERMINATED"}, ok: true}}
	w, ok, err := f.FetchWorker(context.Background(), "w1")
	if err != nil || !ok || w.WorkerID != "w1" || w.Status != worker.StatusTerminated {
		t.Fatalf("FetchWorker = %+v ok=%v err=%v", w, ok, err)
	}
}

func TestProfilesSupported_IncludesSubscribe(t *testing.T) {
	want := map[string]bool{"pull": true, "subscribe": true}
	if len(ProfilesSupported) != 2 {
		t.Fatalf("ProfilesSupported = %v", ProfilesSupported)
	}
	for _, p := range ProfilesSupported {
		if !want[p] {
			t.Errorf("unexpected profile %q", p)
		}
	}
}

func TestSubscribeMechanism_HonestNaming(t *testing.T) {
	if strings.Contains(strings.ToLower(SubscribeMechanism), "continuous monitoring") &&
		!strings.Contains(SubscribeMechanism, "NOT continuous") {
		t.Errorf("SubscribeMechanism must not claim continuous monitoring: %q", SubscribeMechanism)
	}
	if !strings.Contains(strings.ToLower(SubscribeMechanism), "event-driven") {
		t.Errorf("SubscribeMechanism should name the event-driven mechanism: %q", SubscribeMechanism)
	}
}

func TestNewSubscribeCmd_PreRunRejectsMissingEnvironment(t *testing.T) {
	resetCommon(t)
	cmd := newSubscribeCmd()
	if err := cmd.PreRunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error; got %v", err)
	}
}

func TestNewSubscribeCmd_HasFlags(t *testing.T) {
	cmd := newSubscribeCmd()
	for _, f := range []string{"environment", "worker-control", "listen", "path"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("subscribe flag %q missing", f)
		}
	}
}

func TestCommandContext_NotNil(t *testing.T) {
	ctx := commandContext()
	if ctx == nil {
		t.Fatal("commandContext returned nil")
	}
	if ctx.Err() != nil {
		t.Errorf("fresh context already done: %v", ctx.Err())
	}
}

// fakeOne implements workers.OneAPI for the subscribe seam tests.
type fakeOne struct {
	raw workers.RawWorker
	ok  bool
	err error
}

func (f fakeOne) GetWorker(_ context.Context, _ string) (workers.RawWorker, bool, error) {
	return f.raw, f.ok, f.err
}

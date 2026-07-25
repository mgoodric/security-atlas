// Tests for the event-driven (subscribe) profile glue (slice 573). Serve is
// seamed so doSubscribe is exercised without binding a real port for the test's
// lifetime. No real BambooHR credentials / secrets in fixtures.
package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/workers"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
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
	newWorkersOneAPI = func(_ *http.Client, _, _, _ string) workers.OneAPI { return api }
	t.Cleanup(func() { newWorkersOneAPI = prev })
}

func okSubscribeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BAMBOOHR_API_KEY", "test-bamboo-secret")
	t.Setenv("BAMBOOHR_COMPANY_DOMAIN", "acme")
	t.Setenv("BAMBOOHR_BASE_URL", "https://api.bamboohr.example")
	t.Setenv("BAMBOOHR_WEBHOOK_SECRET", "test-webhook-secret")
}

func okSubscribeFlags() subscribeFlags {
	return subscribeFlags{environment: "prod", workerControl: "scf:IAC-22", listenAddr: "127.0.0.1:0", path: "/hooks/bamboohr"}
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
	t.Setenv("BAMBOOHR_API_KEY", "test-bamboo-secret")
	t.Setenv("BAMBOOHR_COMPANY_DOMAIN", "acme")
	t.Setenv("BAMBOOHR_WEBHOOK_SECRET", "")
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
	t.Setenv("BAMBOOHR_API_KEY", "")
	t.Setenv("BAMBOOHR_COMPANY_DOMAIN", "")
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

func TestBambooParser_ExtractsStringID(t *testing.T) {
	ids, err := bambooParser{}.ParseWorkerIDs([]byte(`{"employees":[{"id":"42"}]}`))
	if err != nil || len(ids) != 1 || ids[0] != "42" {
		t.Fatalf("ParseWorkerIDs = %v err=%v", ids, err)
	}
}

func TestBambooParser_ExtractsNumericID(t *testing.T) {
	ids, err := bambooParser{}.ParseWorkerIDs([]byte(`{"employees":[{"id":42}]}`))
	if err != nil || len(ids) != 1 || ids[0] != "42" {
		t.Fatalf("numeric id = %v err=%v", ids, err)
	}
}

// TestBambooParser_FansOutAllEmployees is the slice-655 parser assertion: a
// delivery carrying multiple changed employees returns EVERY id, not just the
// first.
func TestBambooParser_FansOutAllEmployees(t *testing.T) {
	ids, err := bambooParser{}.ParseWorkerIDs([]byte(`{"employees":[{"id":"7"},{"id":42},{"id":"9"}]}`))
	if err != nil {
		t.Fatalf("ParseWorkerIDs err=%v", err)
	}
	want := []string{"7", "42", "9"}
	if len(ids) != len(want) {
		t.Fatalf("ParseWorkerIDs = %v; want %v", ids, want)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("id[%d] = %q; want %q", i, ids[i], w)
		}
	}
}

// TestBambooParser_SkipsBlankEmployeeIDs asserts an employee with a null/blank id
// is skipped while the rest are returned.
func TestBambooParser_SkipsBlankEmployeeIDs(t *testing.T) {
	ids, err := bambooParser{}.ParseWorkerIDs([]byte(`{"employees":[{"id":null},{"id":"5"}]}`))
	if err != nil || len(ids) != 1 || ids[0] != "5" {
		t.Fatalf("ParseWorkerIDs = %v err=%v; want [5]", ids, err)
	}
}

func TestBambooParser_NoWorker(t *testing.T) {
	ids, err := bambooParser{}.ParseWorkerIDs([]byte(`{"employees":[]}`))
	if err != nil || len(ids) != 0 {
		t.Errorf("no-worker: ids=%v err=%v", ids, err)
	}
}

func TestBambooParser_BadJSON(t *testing.T) {
	_, err := bambooParser{}.ParseWorkerIDs([]byte(`not-json`))
	if err == nil {
		t.Fatal("want parse error on bad json")
	}
}

func TestBambooFetcher_DelegatesToFetchOne(t *testing.T) {
	f := bambooFetcher{api: fakeOne{raw: workers.RawWorker{ID: "42", Status: "Inactive", TerminationDate: "2026-05-31"}, ok: true}}
	w, ok, err := f.FetchWorker(context.Background(), "42")
	if err != nil || !ok || w.WorkerID != "42" || w.Status != worker.StatusTerminated {
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

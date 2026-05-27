// Unit tests for the doRun + doWebhook seams introduced by slice 308.
// Slice 301 took the package from 15.1% → 58.8% by covering everything
// reachable without refactoring; the remaining gap sat entirely in
// doRun's post-githubauth.Resolve body (the repo + scim pulls and the
// push loops) and doWebhook's listener body. Slice 308 closes both
// with a minimal seam refactor and the tests below.
//
// Load-bearing branches this file covers in cmd_run.go's doRun:
//
//   - githubauthResolve returns error → doRun wraps "auth: %w".
//   - Resolve ok, newSDKClient returns error → doRun wraps "sdk client: %w".
//   - Resolve + sdk ok, !skipRepoProt, githubrepoInspect errors →
//     doRun wraps "repo-protection inspect: %w".
//   - Resolve + sdk ok, !skipRepoProt, Inspect returns 2 states,
//     pushes succeed → pushed counter == 2.
//   - Resolve + sdk ok, !skipRepoProt, first push errors →
//     doRun wraps "push repo_protection <repo>: %w" and stops.
//   - Resolve + sdk ok, skipRepoProt=true → repo loop is skipped.
//   - Resolve + sdk ok, !skipSCIM, Reconcile returns ErrSCIMUnavailable
//     sentinel → doRun continues, no error.
//   - Resolve + sdk ok, !skipSCIM, Reconcile returns generic error →
//     doRun wraps "scim reconcile: %w".
//   - Resolve + sdk ok, !skipSCIM, Reconcile returns 2 users, pushes
//     succeed → pushed counter increments correctly.
//   - Resolve + sdk ok, !skipSCIM, first scim push errors →
//     doRun wraps "push scim_user <id>: %w".
//   - skipRepoProt + skipSCIM together → fast path, pushed=0, no error.
//
// Load-bearing branches this file covers in cmd_webhook.go's doWebhook:
//
//   - newSDKClient returns error → doWebhook wraps "sdk client: %w".
//   - sdk ok, webhookNewHandler returns error → doWebhook wraps
//     "webhook handler: %w".
//   - sdk + handler ok, serverListenAndServe returns non-ErrServerClosed
//     → doWebhook wraps "listen: %w".
//   - sdk + handler ok, serverListenAndServe blocks, parent ctx cancels
//     → doWebhook returns nil (clean caller-cancel exit).
//
// Test seams are restored via t.Cleanup so the package-level vars are
// left untouched between tests. No vendor-prefixed tokens appear in
// fixtures — neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubscim"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubwebhook"
)

// fakeSDKClient is a minimal sdkPushClient. Push returns the queued
// error/receipt for each call so tests can drive success + failure
// branches.
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

func (f *fakeSDKClient) Close() error {
	f.closeCalled = true
	return nil
}

// runSeams holds optional overrides for the doRun seam set.
type runSeams struct {
	resolve       func(githubauth.ResolveOpts) (githubauth.Credential, error)
	repoNewClient func(*http.Client, string, githubauth.Credential) githubrepo.API
	repoInspect   func(context.Context, githubrepo.API, string, func() time.Time) ([]githubrepo.ProtectionState, error)
	scimNewClient func(*http.Client, string, githubauth.Credential) githubscim.API
	scimReconcile func(context.Context, githubscim.API, string, func() time.Time) ([]githubscim.User, error)
	newClient     func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// installRunSeams swaps the doRun-side seams for the duration of the
// test. Returns nothing; tests assert against external behavior.
func installRunSeams(t *testing.T, o runSeams) {
	t.Helper()
	if o.resolve != nil {
		prev := githubauthResolve
		githubauthResolve = o.resolve
		t.Cleanup(func() { githubauthResolve = prev })
	}
	if o.repoNewClient != nil {
		prev := githubrepoNewClient
		githubrepoNewClient = o.repoNewClient
		t.Cleanup(func() { githubrepoNewClient = prev })
	}
	if o.repoInspect != nil {
		prev := githubrepoInspect
		githubrepoInspect = o.repoInspect
		t.Cleanup(func() { githubrepoInspect = prev })
	}
	if o.scimNewClient != nil {
		prev := githubscimNewClient
		githubscimNewClient = o.scimNewClient
		t.Cleanup(func() { githubscimNewClient = prev })
	}
	if o.scimReconcile != nil {
		prev := githubscimReconcile
		githubscimReconcile = o.scimReconcile
		t.Cleanup(func() { githubscimReconcile = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

// resolveNoop is a seam-friendly Resolve that returns a fresh
// PAT-mode Credential without touching env vars.
func resolveNoop(_ githubauth.ResolveOpts) (githubauth.Credential, error) {
	return githubauth.Credential{Mode: githubauth.ModePAT}, nil
}

// repoNewClientStub returns nil — Inspect is also stubbed so the API
// passed in is never dereferenced.
func repoNewClientStub(_ *http.Client, _ string, _ githubauth.Credential) githubrepo.API {
	return nil
}

func scimNewClientStub(_ *http.Client, _ string, _ githubauth.Credential) githubscim.API {
	return nil
}

// okRunFlags returns a fully-wired runFlags. Tests pass a copy and
// flip the skip bools as needed.
func okRunFlags() runFlags {
	return runFlags{
		org:             "example",
		environment:     "prod",
		githubBaseURL:   "https://api.github.invalid",
		pat:             "test-pat-value",
		repoProtControl: "scf:TDA-06",
		scimUserControl: "scf:IAC-22",
	}
}

// TestDoRun_AuthError: githubauthResolve returns an error; doRun wraps
// it as "auth: %w".
func TestDoRun_AuthError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: resolve refused")
	installRunSeams(t, runSeams{
		resolve: func(_ githubauth.ResolveOpts) (githubauth.Credential, error) {
			return githubauth.Credential{}, sentinel
		},
	})

	err := doRun(context.Background(), okRunFlags())
	if err == nil {
		t.Fatal("doRun: want auth error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "auth: ") {
		t.Errorf("doRun err = %q; want 'auth: ' prefix", err.Error())
	}
}

// TestDoRun_SDKClientError: Resolve ok; newSDKClient errors; doRun
// wraps with "sdk client: %w".
func TestDoRun_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: dial refused")
	installRunSeams(t, runSeams{
		resolve: resolveNoop,
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), okRunFlags())
	if err == nil {
		t.Fatal("doRun: want sdk error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Errorf("doRun err = %q; want 'sdk client: ' prefix", err.Error())
	}
}

// TestDoRun_RepoInspectError: Resolve + sdk ok, !skipRepoProt;
// githubrepoInspect returns error; doRun wraps with
// "repo-protection inspect: %w".
func TestDoRun_RepoInspectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: list refused")
	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipSCIM = true // isolate the repo branch
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		repoNewClient: repoNewClientStub,
		repoInspect: func(_ context.Context, _ githubrepo.API, _ string, _ func() time.Time) ([]githubrepo.ProtectionState, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want repo-inspect error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "repo-protection inspect: ") {
		t.Errorf("doRun err = %q; want 'repo-protection inspect: ' prefix", err.Error())
	}
	if !fake.closeCalled {
		t.Error("sdk client Close not called via defer")
	}
}

// TestDoRun_RepoPushSuccess: Resolve + sdk ok, !skipRepoProt; Inspect
// returns 2 states; both pushes succeed; pushed counter == 2.
func TestDoRun_RepoPushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipSCIM = true
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		repoNewClient: repoNewClientStub,
		repoInspect: func(_ context.Context, _ githubrepo.API, _ string, _ func() time.Time) ([]githubrepo.ProtectionState, error) {
			now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
			return []githubrepo.ProtectionState{
				{RepoFullName: "example/web", DefaultBranch: "main", Result: githubrepo.ResultPass, RequiredReviews: 2, ObservedAt: now},
				{RepoFullName: "example/api", DefaultBranch: "main", Result: githubrepo.ResultFail, ObservedAt: now},
			}, nil
		},
	})

	if err := doRun(context.Background(), flags); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called via defer")
	}
}

// TestDoRun_RepoPushError: Resolve + sdk ok, !skipRepoProt; Inspect
// returns 2 states; first push errors; doRun wraps
// "push repo_protection example/web: %w" and stops without attempting
// the second record.
func TestDoRun_RepoPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: push refused")
	fake := &fakeSDKClient{pushErr: sentinel}
	flags := okRunFlags()
	flags.skipSCIM = true
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		repoNewClient: repoNewClientStub,
		repoInspect: func(_ context.Context, _ githubrepo.API, _ string, _ func() time.Time) ([]githubrepo.ProtectionState, error) {
			now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
			return []githubrepo.ProtectionState{
				{RepoFullName: "example/web", DefaultBranch: "main", Result: githubrepo.ResultPass, ObservedAt: now},
				{RepoFullName: "example/api", DefaultBranch: "main", Result: githubrepo.ResultPass, ObservedAt: now},
			}, nil
		},
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want repo-push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push repo_protection example/web: ") {
		t.Errorf("doRun err = %q; want 'push repo_protection example/web: ' prefix", err.Error())
	}
	if fake.pushed != 0 {
		t.Errorf("pushed = %d; want 0 (push errored before counter incremented)", fake.pushed)
	}
}

// TestDoRun_SCIMUnavailableSkips: Resolve + sdk ok, !skipSCIM;
// Reconcile returns the ErrSCIMUnavailable sentinel; doRun continues
// and returns nil (non-enterprise org → clean skip).
func TestDoRun_SCIMUnavailableSkips(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipRepoProt = true // isolate the scim branch
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		scimNewClient: scimNewClientStub,
		scimReconcile: func(_ context.Context, _ githubscim.API, _ string, _ func() time.Time) ([]githubscim.User, error) {
			return nil, githubscim.ErrSCIMUnavailable
		},
	})

	if err := doRun(context.Background(), flags); err != nil {
		t.Fatalf("doRun: want nil for SCIM-unavailable skip; got %v", err)
	}
	if fake.pushed != 0 {
		t.Errorf("pushed = %d; want 0 (no users to push)", fake.pushed)
	}
}

// TestDoRun_SCIMReconcileError: Resolve + sdk ok, !skipSCIM; Reconcile
// returns a generic (non-sentinel) error; doRun wraps with
// "scim reconcile: %w".
func TestDoRun_SCIMReconcileError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: scim list refused")
	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipRepoProt = true
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		scimNewClient: scimNewClientStub,
		scimReconcile: func(_ context.Context, _ githubscim.API, _ string, _ func() time.Time) ([]githubscim.User, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want scim-reconcile error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "scim reconcile: ") {
		t.Errorf("doRun err = %q; want 'scim reconcile: ' prefix", err.Error())
	}
}

// TestDoRun_SCIMPushSuccess: Resolve + sdk ok, !skipSCIM; Reconcile
// returns 2 users; both pushes succeed; pushed counter == 2.
func TestDoRun_SCIMPushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipRepoProt = true
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		scimNewClient: scimNewClientStub,
		scimReconcile: func(_ context.Context, _ githubscim.API, _ string, _ func() time.Time) ([]githubscim.User, error) {
			return []githubscim.User{
				{SCIMUserID: "scim-1", UserName: "alice@example.com", Active: true, Org: "example", ObservedAt: now},
				{SCIMUserID: "scim-2", UserName: "bob@example.com", Active: false, Org: "example", ObservedAt: now},
			}, nil
		},
	})

	if err := doRun(context.Background(), flags); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fake.pushed)
	}
}

// TestDoRun_SCIMPushError: Resolve + sdk ok, !skipSCIM; Reconcile
// returns 1 user; push errors; doRun wraps with
// "push scim_user scim-1: %w".
func TestDoRun_SCIMPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: scim push refused")
	fake := &fakeSDKClient{pushErr: sentinel}
	flags := okRunFlags()
	flags.skipRepoProt = true
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	installRunSeams(t, runSeams{
		resolve:       resolveNoop,
		newClient:     func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		scimNewClient: scimNewClientStub,
		scimReconcile: func(_ context.Context, _ githubscim.API, _ string, _ func() time.Time) ([]githubscim.User, error) {
			return []githubscim.User{
				{SCIMUserID: "scim-1", UserName: "alice@example.com", Active: true, Org: "example", ObservedAt: now},
			}, nil
		},
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want scim-push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push scim_user scim-1: ") {
		t.Errorf("doRun err = %q; want 'push scim_user scim-1: ' prefix", err.Error())
	}
}

// TestDoRun_BothSkipsFastPath: Resolve + sdk ok; skipRepoProt=true and
// skipSCIM=true; both loops are skipped; doRun returns nil with
// pushed=0. Drives the "all skipped" branch.
func TestDoRun_BothSkipsFastPath(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fake := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipRepoProt = true
	flags.skipSCIM = true
	installRunSeams(t, runSeams{
		resolve:   resolveNoop,
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})

	if err := doRun(context.Background(), flags); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 0 {
		t.Errorf("pushed = %d; want 0", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called via defer")
	}
}

// --- doWebhook seam tests ---

// webhookSeams holds optional overrides for the doWebhook seam set.
type webhookSeams struct {
	newClient   func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
	newHandler  func(secret []byte, pusher githubwebhook.Pusher, now func() time.Time) (http.Handler, error)
	listenServe func(srv *http.Server) error
	notify      func(c chan<- os.Signal, sig ...os.Signal)
}

// installWebhookSeams swaps the doWebhook-side seams for the duration
// of the test. The newClient seam is shared with doRun; reuse the same
// global.
func installWebhookSeams(t *testing.T, o webhookSeams) {
	t.Helper()
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
	if o.newHandler != nil {
		prev := webhookNewHandler
		webhookNewHandler = o.newHandler
		t.Cleanup(func() { webhookNewHandler = prev })
	}
	if o.listenServe != nil {
		prev := serverListenAndServe
		serverListenAndServe = o.listenServe
		t.Cleanup(func() { serverListenAndServe = prev })
	}
	if o.notify != nil {
		prev := signalNotify
		signalNotify = o.notify
		t.Cleanup(func() { signalNotify = prev })
	}
}

// okWebhookFlags returns a fully-wired webhookFlags. Tests pass a copy.
func okWebhookFlags() webhookFlags {
	return webhookFlags{
		addr:        ":0", // never bound — serverListenAndServe is seamed
		path:        "/webhook",
		environment: "prod",
		controlID:   "scf:MON-01",
	}
}

// TestDoWebhook_SDKClientError: newSDKClient errors; doWebhook wraps
// with "sdk client: %w".
func TestDoWebhook_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv(EnvWebhookSecret, "test-secret-value")

	sentinel := errors.New("sentinel: dial refused")
	installWebhookSeams(t, webhookSeams{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) {
			return nil, sentinel
		},
	})

	err := doWebhook(context.Background(), okWebhookFlags())
	if err == nil {
		t.Fatal("doWebhook: want sdk error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doWebhook err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Errorf("doWebhook err = %q; want 'sdk client: ' prefix", err.Error())
	}
}

// TestDoWebhook_HandlerError: sdk ok; webhookNewHandler errors;
// doWebhook wraps with "webhook handler: %w".
func TestDoWebhook_HandlerError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv(EnvWebhookSecret, "test-secret-value")

	sentinel := errors.New("sentinel: handler refused")
	fake := &fakeSDKClient{}
	installWebhookSeams(t, webhookSeams{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		newHandler: func(_ []byte, _ githubwebhook.Pusher, _ func() time.Time) (http.Handler, error) {
			return nil, sentinel
		},
	})

	err := doWebhook(context.Background(), okWebhookFlags())
	if err == nil {
		t.Fatal("doWebhook: want handler error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doWebhook err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "webhook handler: ") {
		t.Errorf("doWebhook err = %q; want 'webhook handler: ' prefix", err.Error())
	}
	if !fake.closeCalled {
		t.Error("sdk client Close not called via defer")
	}
}

// TestDoWebhook_ListenError: sdk + handler ok; serverListenAndServe
// returns a non-ErrServerClosed error; doWebhook wraps with
// "listen: %w".
func TestDoWebhook_ListenError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv(EnvWebhookSecret, "test-secret-value")

	sentinel := errors.New("sentinel: bind refused")
	fake := &fakeSDKClient{}
	// notifyNoop: don't actually register signal handlers (would
	// hijack the parent process's signals during the test).
	notifyNoop := func(_ chan<- os.Signal, _ ...os.Signal) {}
	installWebhookSeams(t, webhookSeams{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		newHandler: func(_ []byte, _ githubwebhook.Pusher, _ func() time.Time) (http.Handler, error) {
			return http.NewServeMux(), nil
		},
		listenServe: func(_ *http.Server) error { return sentinel },
		notify:      notifyNoop,
	})

	err := doWebhook(context.Background(), okWebhookFlags())
	if err == nil {
		t.Fatal("doWebhook: want listen error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doWebhook err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "listen: ") {
		t.Errorf("doWebhook err = %q; want 'listen: ' prefix", err.Error())
	}
}

// TestDoWebhook_ListenErrServerClosedIsClean: serverListenAndServe
// returns http.ErrServerClosed (the canonical clean-shutdown sentinel);
// doWebhook returns nil. Drives the errors.Is branch.
func TestDoWebhook_ListenErrServerClosedIsClean(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv(EnvWebhookSecret, "test-secret-value")

	fake := &fakeSDKClient{}
	notifyNoop := func(_ chan<- os.Signal, _ ...os.Signal) {}
	installWebhookSeams(t, webhookSeams{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		newHandler: func(_ []byte, _ githubwebhook.Pusher, _ func() time.Time) (http.Handler, error) {
			return http.NewServeMux(), nil
		},
		listenServe: func(_ *http.Server) error { return http.ErrServerClosed },
		notify:      notifyNoop,
	})

	if err := doWebhook(context.Background(), okWebhookFlags()); err != nil {
		t.Fatalf("doWebhook: want nil on ErrServerClosed; got %v", err)
	}
}

// TestDoWebhook_CtxCancelExitsCleanly: serverListenAndServe blocks
// forever; the parent context is cancelled; doWebhook returns nil
// via the ctx.Done branch. Demonstrates the caller-cancel-graceful
// exit path.
func TestDoWebhook_CtxCancelExitsCleanly(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv(EnvWebhookSecret, "test-secret-value")

	fake := &fakeSDKClient{}
	// blockUntilCancel blocks until the test releases it via the
	// release channel — letting the ctx.Done branch fire.
	release := make(chan struct{})
	notifyNoop := func(_ chan<- os.Signal, _ ...os.Signal) {}
	installWebhookSeams(t, webhookSeams{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
		newHandler: func(_ []byte, _ githubwebhook.Pusher, _ func() time.Time) (http.Handler, error) {
			return http.NewServeMux(), nil
		},
		listenServe: func(_ *http.Server) error {
			<-release
			return http.ErrServerClosed
		},
		notify: notifyNoop,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a brief delay to ensure doWebhook reaches the select.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { done <- doWebhook(ctx, okWebhookFlags()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("doWebhook: want nil on ctx-cancel exit; got %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release) // unblock the goroutine before failing
		t.Fatal("doWebhook did not return within 2s after ctx cancel")
	}
	// Release the still-blocked listener so the goroutine exits cleanly.
	select {
	case release <- struct{}{}:
	default:
		close(release)
	}
}

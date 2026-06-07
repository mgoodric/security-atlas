// Seam tests for doRun. All Kubernetes reads + the sdk client constructor are
// swapped for fakes so doRun is exercised end-to-end without touching a live
// cluster or a real platform. Seams are restored via t.Cleanup.
//
// No real cluster tokens in fixtures — neutral "test-*" strings only.
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

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
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
	rbacPull     func(ctx context.Context, api rbac.API, now func() time.Time) ([]rbac.Binding, error)
	workloadScan func(ctx context.Context, api workload.API, now func() time.Time) ([]workload.SecurityContext, error)
	newClient    func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.rbacPull != nil {
		prev := rbacPull
		rbacPull = o.rbacPull
		t.Cleanup(func() { rbacPull = prev })
	}
	if o.workloadScan != nil {
		prev := workloadScan
		workloadScan = o.workloadScan
		t.Cleanup(func() { workloadScan = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KUBERNETES_API_SERVER", "https://kube:6443")
	t.Setenv("KUBECONFIG_TOKEN", "test-k8s-token")
}

func okFlags() runFlags {
	return runFlags{
		cluster:         "cluster-1",
		environment:     "prod",
		authMode:        "kubeconfig-token",
		rbacControl:     "scf:IAC-21",
		workloadControl: "scf:CFG-02",
	}
}

func oneBinding() []rbac.Binding {
	return []rbac.Binding{{
		BindingName: "admins", BindingScope: rbac.ScopeCluster,
		RoleKind: rbac.RoleKindClusterRole, RoleName: "cluster-admin",
		ObservedAt: time.Now().UTC(),
	}}
}

func oneWorkload() []workload.SecurityContext {
	return []workload.SecurityContext{{
		WorkloadKind: workload.KindDeployment, WorkloadName: "api", Namespace: "prod",
		RunAsNonRoot: true, ReadOnlyRootFilesystem: true, Result: workload.ResultPass,
		ObservedAt: time.Now().UTC(),
	}}
}

func TestDoRun_PushSuccessBothKinds(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)

	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		rbacPull: func(_ context.Context, _ rbac.API, _ func() time.Time) ([]rbac.Binding, error) {
			return oneBinding(), nil
		},
		workloadScan: func(_ context.Context, _ workload.API, _ func() time.Time) ([]workload.SecurityContext, error) {
			return oneWorkload(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})

	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 2 {
		t.Errorf("pushed = %d; want 2 (one rbac + one workload)", fake.pushed)
	}
	if !fake.closeCalled {
		t.Error("Close not called")
	}
}

func TestDoRun_SkipRBAC(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		workloadScan: func(_ context.Context, _ workload.API, _ func() time.Time) ([]workload.SecurityContext, error) {
			return oneWorkload(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipRBAC = true
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fake.pushed != 1 {
		t.Errorf("pushed = %d; want 1", fake.pushed)
	}
}

func TestDoRun_SkipWorkload(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	fake := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		rbacPull: func(_ context.Context, _ rbac.API, _ func() time.Time) ([]rbac.Binding, error) {
			return oneBinding(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipWorkload = true
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

func TestDoRun_RBACPullError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("k8s 403")
	installSeams(t, seamOverrides{
		rbacPull: func(_ context.Context, _ rbac.API, _ func() time.Time) ([]rbac.Binding, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "rbac pull: ") {
		t.Fatalf("want wrapped rbac pull error; got %v", err)
	}
}

func TestDoRun_WorkloadInspectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("k8s 403")
	installSeams(t, seamOverrides{
		workloadScan: func(_ context.Context, _ workload.API, _ func() time.Time) ([]workload.SecurityContext, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})
	f := okFlags()
	f.skipRBAC = true
	err := doRun(context.Background(), f)
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "workload inspect: ") {
		t.Fatalf("want wrapped workload inspect error; got %v", err)
	}
}

func TestDoRun_WorkloadPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		workloadScan: func(_ context.Context, _ workload.API, _ func() time.Time) ([]workload.SecurityContext, error) {
			return oneWorkload(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipRBAC = true
	err := doRun(context.Background(), f)
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push workload ") {
		t.Fatalf("want wrapped push workload error; got %v", err)
	}
}

func TestDoRun_RBACPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	okEnv(t)
	sentinel := errors.New("push rejected")
	fake := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		rbacPull: func(_ context.Context, _ rbac.API, _ func() time.Time) ([]rbac.Binding, error) {
			return oneBinding(), nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fake, nil },
	})
	f := okFlags()
	f.skipWorkload = true
	err := doRun(context.Background(), f)
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "push rbac ") {
		t.Fatalf("want wrapped push rbac error; got %v", err)
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

func TestNewRBACAPIAndWorkloadAPI_Constructors(t *testing.T) {
	// Exercise the live constructors (seam defaults) so they aren't dead code.
	if newRBACAPI(http.DefaultClient, "https://k", "test-k8s-token") == nil {
		t.Error("newRBACAPI returned nil")
	}
	if newWorkloadAPI(http.DefaultClient, "https://k", "test-k8s-token") == nil {
		t.Error("newWorkloadAPI returned nil")
	}
}

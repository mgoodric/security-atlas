// Slice 309 — seam-driven coverage tests for doRun's post-Resolve
// body. Pairs with slice 305 (aws-connector) and slice 308 (github
// cmd) as the third application of the same pattern.
//
// Load-bearing functions and the branches each test exercises:
//
//   - doRun sdk-client construction: newSDKClient error wrap with
//     "sdk client: " prefix.
//   - doRun mfa policy branch: pull error wrap ("mfa policy pull: ")
//     + push error wrap ("push mfa_policy <id>: ") + happy path.
//   - doRun app assignment branch: pull error wrap ("app assignment
//     pull: ") + push error wrap ("push app_assignment <id>: ") +
//     happy path.
//   - doRun user lifecycle branch: pull error wrap ("user lifecycle
//     pull: ") + push error wrap ("push user_lifecycle <id>: ") +
//     happy path.
//   - doRun skip-all path: skipMFAPolicy + skipAppAssign +
//     skipUserLife all true returns nil with zero records pushed.
//
// Test seams are restored via t.Cleanup so package-level vars are
// left untouched between tests. No vendor-prefixed tokens
// (Okta `00...` 42-char API tokens) appear in fixtures — neutral
// "test-*" strings only.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktausers"
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

// seamOverrides bundles the optional seam swaps a test can install.
// nil fields are left at the production default.
type seamOverrides struct {
	policyPull func(ctx context.Context, api oktapolicy.API, now func() time.Time) ([]oktapolicy.PolicyState, error)
	appsPull   func(ctx context.Context, api oktaapps.API, now func() time.Time) ([]oktaapps.Assignment, error)
	usersPull  func(ctx context.Context, api oktausers.API, now func() time.Time) ([]oktausers.Lifecycle, error)
	newClient  func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

// installSeams swaps the package-level seams for the duration of the
// test. Each installed seam is restored via t.Cleanup.
func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.policyPull != nil {
		prev := oktapolicyPull
		oktapolicyPull = o.policyPull
		t.Cleanup(func() { oktapolicyPull = prev })
	}
	if o.appsPull != nil {
		prev := oktaappsPull
		oktaappsPull = o.appsPull
		t.Cleanup(func() { oktaappsPull = prev })
	}
	if o.usersPull != nil {
		prev := oktausersPull
		oktausersPull = o.usersPull
		t.Cleanup(func() { oktausersPull = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

// okRunFlags returns runFlags wired with valid values; tests pass a copy.
func okRunFlags() runFlags {
	return runFlags{
		org:              "example",
		environment:      "prod",
		oktaBaseURL:      "https://example.okta.com",
		token:            "test-okta-token",
		mfaPolicyControl: "scf:IAC-06",
		appAssignControl: "scf:IAC-21",
		userLifeControl:  "scf:IAC-22",
	}
}

// samplePolicyState returns a canonical PolicyState fixture with a
// stable PolicyID so wrap-prefix assertions can match.
func samplePolicyState(id string) oktapolicy.PolicyState {
	return oktapolicy.PolicyState{
		PolicyID:    id,
		PolicyName:  "test policy " + id,
		MFARequired: true,
		Result:      oktapolicy.ResultPass,
		ObservedAt:  time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
	}
}

// sampleAssignment returns a canonical Assignment fixture with a
// stable AppID.
func sampleAssignment(id string) oktaapps.Assignment {
	return oktaapps.Assignment{
		AppID:              id,
		AppName:            "test app " + id,
		Status:             "ACTIVE",
		AssignedGroupIDs:   []string{"g1", "g2"},
		AssignedGroupCount: 2,
		ObservedAt:         time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
	}
}

// sampleLifecycle returns a canonical Lifecycle fixture with a stable
// UserID.
func sampleLifecycle(id string) oktausers.Lifecycle {
	return oktausers.Lifecycle{
		UserID:      id,
		Login:       id + "@example.com",
		Status:      "ACTIVE",
		MFAEnrolled: true,
		Result:      oktausers.ResultPass,
		ObservedAt:  time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
	}
}

// TestDoRun_SDKClientError: oktaauth.Resolve succeeds (env token set);
// newSDKClient errors. doRun wraps with "sdk client: ".
func TestDoRun_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: bad endpoint")
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), okRunFlags())
	if err == nil {
		t.Fatal("doRun: want error from newSDKClient")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Errorf("doRun err = %q; want 'sdk client: ' prefix", err.Error())
	}
}

// TestDoRun_MFAPolicyPullError: oktapolicyPull errors. doRun wraps
// with "mfa policy pull: ".
func TestDoRun_MFAPolicyPullError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: policy 401")
	installSeams(t, seamOverrides{
		policyPull: func(_ context.Context, _ oktapolicy.API, _ func() time.Time) ([]oktapolicy.PolicyState, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})

	err := doRun(context.Background(), okRunFlags())
	if err == nil {
		t.Fatal("doRun: want error from policy pull")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "mfa policy pull: ") {
		t.Errorf("doRun err = %q; want 'mfa policy pull: ' prefix", err.Error())
	}
}

// TestDoRun_AppAssignmentPullError: policy succeeds but skipped (or
// returns empty); oktaappsPull errors. doRun wraps with
// "app assignment pull: ".
func TestDoRun_AppAssignmentPullError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: apps 403")
	flags := okRunFlags()
	flags.skipMFAPolicy = true // skip first branch to isolate
	installSeams(t, seamOverrides{
		appsPull: func(_ context.Context, _ oktaapps.API, _ func() time.Time) ([]oktaapps.Assignment, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want error from apps pull")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "app assignment pull: ") {
		t.Errorf("doRun err = %q; want 'app assignment pull: ' prefix", err.Error())
	}
}

// TestDoRun_UserLifecyclePullError: first two branches skipped;
// oktausersPull errors. doRun wraps with "user lifecycle pull: ".
func TestDoRun_UserLifecyclePullError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: users 500")
	flags := okRunFlags()
	flags.skipMFAPolicy = true
	flags.skipAppAssign = true
	installSeams(t, seamOverrides{
		usersPull: func(_ context.Context, _ oktausers.API, _ func() time.Time) ([]oktausers.Lifecycle, error) {
			return nil, sentinel
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return &fakeSDKClient{}, nil },
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want error from users pull")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "user lifecycle pull: ") {
		t.Errorf("doRun err = %q; want 'user lifecycle pull: ' prefix", err.Error())
	}
}

// TestDoRun_PushSuccessAllThree: full happy path through doRun.
// Two policy states + two app assignments + two user lifecycles
// drive six iterations of the for-loops; the fake sdk client records
// six Push calls; doRun returns nil and defer-Close fires.
func TestDoRun_PushSuccessAllThree(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fakeClient := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		policyPull: func(_ context.Context, _ oktapolicy.API, _ func() time.Time) ([]oktapolicy.PolicyState, error) {
			return []oktapolicy.PolicyState{samplePolicyState("p1"), samplePolicyState("p2")}, nil
		},
		appsPull: func(_ context.Context, _ oktaapps.API, _ func() time.Time) ([]oktaapps.Assignment, error) {
			return []oktaapps.Assignment{sampleAssignment("a1"), sampleAssignment("a2")}, nil
		},
		usersPull: func(_ context.Context, _ oktausers.API, _ func() time.Time) ([]oktausers.Lifecycle, error) {
			return []oktausers.Lifecycle{sampleLifecycle("u1"), sampleLifecycle("u2")}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	if err := doRun(context.Background(), okRunFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fakeClient.pushed != 6 {
		t.Errorf("pushed = %d; want 6 (2 policy + 2 apps + 2 users)", fakeClient.pushed)
	}
	if !fakeClient.closeCalled {
		t.Error("Close not called via defer")
	}
}

// TestDoRun_MFAPolicyPushError: pull succeeds but Push errors on the
// first policy record. doRun wraps with "push mfa_policy p1: " and
// stops without attempting the second.
func TestDoRun_MFAPolicyPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: push rejected")
	fakeClient := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		policyPull: func(_ context.Context, _ oktapolicy.API, _ func() time.Time) ([]oktapolicy.PolicyState, error) {
			return []oktapolicy.PolicyState{samplePolicyState("p1"), samplePolicyState("p2")}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	err := doRun(context.Background(), okRunFlags())
	if err == nil {
		t.Fatal("doRun: want push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push mfa_policy p1: ") {
		t.Errorf("doRun err = %q; want 'push mfa_policy p1: ' prefix", err.Error())
	}
	if fakeClient.pushed != 0 {
		t.Errorf("pushed = %d; want 0 (push errored before counter incremented)", fakeClient.pushed)
	}
}

// TestDoRun_AppAssignmentPushError: skip policy; apps pull succeeds;
// first Push errors. doRun wraps with "push app_assignment a1: ".
func TestDoRun_AppAssignmentPushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: push rejected")
	fakeClient := &fakeSDKClient{pushErr: sentinel}
	flags := okRunFlags()
	flags.skipMFAPolicy = true
	installSeams(t, seamOverrides{
		appsPull: func(_ context.Context, _ oktaapps.API, _ func() time.Time) ([]oktaapps.Assignment, error) {
			return []oktaapps.Assignment{sampleAssignment("a1")}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push app_assignment a1: ") {
		t.Errorf("doRun err = %q; want 'push app_assignment a1: ' prefix", err.Error())
	}
}

// TestDoRun_UserLifecyclePushError: skip first two branches; users
// pull succeeds; first Push errors. doRun wraps with
// "push user_lifecycle u1: ".
func TestDoRun_UserLifecyclePushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: push rejected")
	fakeClient := &fakeSDKClient{pushErr: sentinel}
	flags := okRunFlags()
	flags.skipMFAPolicy = true
	flags.skipAppAssign = true
	installSeams(t, seamOverrides{
		usersPull: func(_ context.Context, _ oktausers.API, _ func() time.Time) ([]oktausers.Lifecycle, error) {
			return []oktausers.Lifecycle{sampleLifecycle("u1")}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	err := doRun(context.Background(), flags)
	if err == nil {
		t.Fatal("doRun: want push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push user_lifecycle u1: ") {
		t.Errorf("doRun err = %q; want 'push user_lifecycle u1: ' prefix", err.Error())
	}
}

// TestDoRun_SkipAllFlags: every branch skipped via flags. doRun
// returns nil; sdk client constructed + closed; zero records pushed.
func TestDoRun_SkipAllFlags(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fakeClient := &fakeSDKClient{}
	flags := okRunFlags()
	flags.skipMFAPolicy = true
	flags.skipAppAssign = true
	flags.skipUserLife = true
	installSeams(t, seamOverrides{
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	if err := doRun(context.Background(), flags); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fakeClient.pushed != 0 {
		t.Errorf("pushed = %d; want 0 (all skip flags true)", fakeClient.pushed)
	}
	if !fakeClient.closeCalled {
		t.Error("Close not called via defer")
	}
}

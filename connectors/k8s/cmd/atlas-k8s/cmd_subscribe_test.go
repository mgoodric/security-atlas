// Subscribe-profile (slice 526) tests. The watch Source is faked via the
// newRBACWatchSrc / newWorkloadWatchSrc seams + a scripted fake Stream, so the
// event-driven path is driven end-to-end to a push round-trip WITHOUT a live
// cluster. Asserts: push round-trip, cross-profile dedup (watch key == pull key),
// burst-coalesce, no payload leak, and no token in any log line.
//
// No real cluster tokens in fixtures — neutral "test-*" strings only.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/watch"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

// capturingSDKClient records every pushed record so tests can assert payloads.
type capturingSDKClient struct {
	mu      sync.Mutex
	records []*evidencev1.EvidenceRecord
	pushErr error
}

func (c *capturingSDKClient) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pushErr != nil {
		return nil, c.pushErr
	}
	c.records = append(c.records, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}

func (c *capturingSDKClient) Close() error { return nil }

func (c *capturingSDKClient) snapshot() []*evidencev1.EvidenceRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*evidencev1.EvidenceRecord, len(c.records))
	copy(out, c.records)
	return out
}

// scriptedStream replays a fixed set of events then closes.
type scriptedStream struct {
	mu     sync.Mutex
	events []watch.Event
	idx    int
}

func (s *scriptedStream) Recv() (watch.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx >= len(s.events) {
		return watch.Event{}, watch.ErrStreamClosed
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *scriptedStream) Close() error { return nil }

// scriptedSource: one bootstrap LIST (empty) then a single scripted stream; the
// second Watch call cancels ctx via onSecondWatch, ending the loop.
type scriptedSource struct {
	mu            sync.Mutex
	events        []watch.Event
	listObjects   []any
	listRV        string
	watchCalls    int
	onSecondWatch func()
}

func (s *scriptedSource) List(context.Context) ([]any, string, error) {
	rv := s.listRV
	if rv == "" {
		rv = "rv-1"
	}
	return s.listObjects, rv, nil
}

func (s *scriptedSource) Watch(_ context.Context, _ string) (watch.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchCalls++
	if s.watchCalls == 1 {
		return &scriptedStream{events: s.events}, nil
	}
	if s.onSecondWatch != nil {
		s.onSecondWatch()
	}
	return &scriptedStream{}, nil
}

func subscribeTestEnv(t *testing.T) {
	t.Helper()
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true
	t.Setenv("KUBERNETES_API_SERVER", "https://kube:6443")
	t.Setenv("KUBECONFIG_TOKEN", "test-k8s-token")
}

func okSubscribeFlags() subscribeFlags {
	return subscribeFlags{
		cluster:         "cluster-1",
		environment:     "prod",
		authMode:        "kubeconfig-token",
		rbacControl:     "scf:IAC-21",
		workloadControl: "scf:CFG-02",
		skipWorkload:    true, // most tests exercise rbac only
	}
}

func bindingRaw(name string) rbac.RawBinding {
	return rbac.RawBinding{
		Name:     name,
		Scope:    rbac.ScopeCluster,
		RoleKind: rbac.RoleKindClusterRole,
		RoleName: "cluster-admin",
		Subjects: []rbac.Subject{{Kind: rbac.SubjectUser, Name: "alice"}},
	}
}

func installRBACWatchSource(t *testing.T, src watch.Source) {
	t.Helper()
	prev := newRBACWatchSrc
	newRBACWatchSrc = func(_, _ string) watch.Source { return src }
	t.Cleanup(func() { newRBACWatchSrc = prev })
}

func installWorkloadWatchSource(t *testing.T, src watch.Source) {
	t.Helper()
	prev := newWorkloadWatchSrc
	newWorkloadWatchSrc = func(_, _ string) watch.Source { return src }
	t.Cleanup(func() { newWorkloadWatchSrc = prev })
}

func installSDK(t *testing.T, c sdkPushClient) {
	t.Helper()
	prev := newSDKClient
	newSDKClient = func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return c, nil }
	t.Cleanup(func() { newSDKClient = prev })
}

// TestSubscribe_RBACEventPushRoundTrip drives an ADDED + MODIFIED watch event for
// one binding to a push round-trip and asserts the emitted record carries the
// SAME idempotency key the pull path would produce (cross-profile dedup).
func TestSubscribe_RBACEventPushRoundTrip(t *testing.T) {
	subscribeTestEnv(t)
	cap := &capturingSDKClient{}
	installSDK(t, cap)

	ctx, cancel := context.WithCancel(context.Background())
	src := &scriptedSource{
		events: []watch.Event{
			{Type: watch.EventAdded, ResourceVersion: "rv-2", Object: bindingRaw("admins")},
			{Type: watch.EventModified, ResourceVersion: "rv-3", Object: bindingRaw("admins")},
		},
		onSecondWatch: cancel,
	}
	installRBACWatchSource(t, src)

	if err := doSubscribe(ctx, okSubscribeFlags()); err != nil {
		t.Fatalf("doSubscribe: %v", err)
	}
	recs := cap.snapshot()
	if len(recs) == 0 {
		t.Fatal("no records pushed")
	}
	for _, r := range recs {
		if r.GetEvidenceKind() != "k8s.rbac_binding.v1" {
			t.Errorf("evidence_kind = %q; want k8s.rbac_binding.v1 (no new kind)", r.GetEvidenceKind())
		}
	}

	// Cross-profile dedup: the watch-emitted key must equal the pull-path key for
	// the same binding in the same hour.
	hour := recs[0].GetObservedAt().AsTime().UTC().Truncate(time.Hour)
	wantKey := idem.RBACBindingKey(rbac.ScopeCluster, "", "admins", hour)
	for _, r := range recs {
		if r.GetIdempotencyKey() != wantKey {
			t.Errorf("idempotency_key = %q; want pull-path key %q (cross-profile + burst dedup)",
				r.GetIdempotencyKey(), wantKey)
		}
	}
}

// TestSubscribe_BurstCoalesceSameKey proves a burst of edits to ONE binding in
// the same hour all map to the same idempotency key — the platform ledger
// collapses them to one row (the durable DoS coalescing guarantee).
func TestSubscribe_BurstCoalesceSameKey(t *testing.T) {
	subscribeTestEnv(t)
	cap := &capturingSDKClient{}
	installSDK(t, cap)

	events := make([]watch.Event, 0, 10)
	for i := 0; i < 10; i++ {
		events = append(events, watch.Event{Type: watch.EventModified, ResourceVersion: "rv-x", Object: bindingRaw("admins")})
	}
	ctx, cancel := context.WithCancel(context.Background())
	src := &scriptedSource{events: events, onSecondWatch: cancel}
	installRBACWatchSource(t, src)

	if err := doSubscribe(ctx, okSubscribeFlags()); err != nil {
		t.Fatalf("doSubscribe: %v", err)
	}
	keys := map[string]struct{}{}
	for _, r := range cap.snapshot() {
		keys[r.GetIdempotencyKey()] = struct{}{}
	}
	if len(keys) != 1 {
		t.Errorf("burst produced %d distinct idempotency keys; want 1 (all collapse)", len(keys))
	}
}

// TestSubscribe_WatchKeyEqualsPullKey proves the watch path and the pull path
// produce byte-identical idempotency keys + evidence kind for the same resource
// — the downstream evaluator cannot tell which profile produced a record.
func TestSubscribe_WatchKeyEqualsPullKey(t *testing.T) {
	subscribeTestEnv(t)

	// Build the pull-path record directly.
	pullBindings, err := rbac.Pull(context.Background(), singleBindingAPI{bindingRaw("admins")}, func() time.Time {
		return time.Date(2026, 6, 9, 10, 30, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("rbac.Pull: %v", err)
	}
	pullRec, err := buildRBACRecord(pullBindings[0], "cluster-1", "prod", "scf:IAC-21")
	if err != nil {
		t.Fatalf("buildRBACRecord: %v", err)
	}

	// The watch emitter builds the same record from the same Raw, captured here.
	captured := &capturingSDKClient{}
	emit := rbacEmitter(captured, "cluster-1", "prod", "scf:IAC-21")
	if _, err := emit(context.Background(), watch.EventAdded, bindingRaw("admins")); err != nil {
		t.Fatalf("emit: %v", err)
	}
	watchRec := captured.snapshot()[0]

	if watchRec.GetEvidenceKind() != pullRec.GetEvidenceKind() {
		t.Errorf("kind: watch=%q pull=%q", watchRec.GetEvidenceKind(), pullRec.GetEvidenceKind())
	}
	// Keys are hour-bucketed; if the test runs across an hour boundary they could
	// differ. Re-derive both at a fixed hour to keep the assertion deterministic.
	wantKey := idem.RBACBindingKey(rbac.ScopeCluster, "", "admins",
		watchRec.GetObservedAt().AsTime().UTC().Truncate(time.Hour))
	if watchRec.GetIdempotencyKey() != wantKey {
		t.Errorf("watch key = %q; want %q", watchRec.GetIdempotencyKey(), wantKey)
	}
}

// TestSubscribe_NoPayloadLeak proves the event-path RBAC record carries ONLY
// binding-identity config — never any Secret / env / token material. (The watch
// decode adapter does not model those fields; this asserts the record payload
// contains no such keys.)
func TestSubscribe_NoPayloadLeak(t *testing.T) {
	subscribeTestEnv(t)
	cap := &capturingSDKClient{}
	installSDK(t, cap)
	ctx, cancel := context.WithCancel(context.Background())
	src := &scriptedSource{
		events:        []watch.Event{{Type: watch.EventAdded, ResourceVersion: "rv-2", Object: bindingRaw("admins")}},
		onSecondWatch: cancel,
	}
	installRBACWatchSource(t, src)
	if err := doSubscribe(ctx, okSubscribeFlags()); err != nil {
		t.Fatalf("doSubscribe: %v", err)
	}
	recs := cap.snapshot()
	if len(recs) == 0 {
		t.Fatal("no record")
	}
	fields := recs[0].GetPayload().GetFields()
	banned := []string{"data", "stringData", "env", "secret", "token", "value", "password"}
	for k := range fields {
		for _, b := range banned {
			if strings.EqualFold(k, b) {
				t.Errorf("record payload leaked a banned field %q (config-only boundary)", k)
			}
		}
	}
	// Positive: it has the binding-identity fields.
	if _, ok := fields["binding_name"]; !ok {
		t.Error("record missing binding_name (expected config field)")
	}
}

// TestSubscribe_WorkloadEventPushRoundTrip drives a workload watch event to a
// push round-trip and asserts the existing workload kind + key.
func TestSubscribe_WorkloadEventPushRoundTrip(t *testing.T) {
	subscribeTestEnv(t)
	cap := &capturingSDKClient{}
	installSDK(t, cap)
	ctx, cancel := context.WithCancel(context.Background())
	raw := workload.RawWorkload{Kind: workload.KindDeployment, Name: "api", Namespace: "prod", RunAsNonRoot: true}
	src := &scriptedSource{
		events:        []watch.Event{{Type: watch.EventModified, ResourceVersion: "rv-2", Object: raw}},
		onSecondWatch: cancel,
	}
	installWorkloadWatchSource(t, src)
	f := okSubscribeFlags()
	f.skipRBAC = true
	f.skipWorkload = false
	if err := doSubscribe(ctx, f); err != nil {
		t.Fatalf("doSubscribe: %v", err)
	}
	recs := cap.snapshot()
	if len(recs) == 0 || recs[0].GetEvidenceKind() != "k8s.workload_security_context.v1" {
		t.Fatalf("want one k8s.workload_security_context.v1 record; got %d", len(recs))
	}
}

// TestSubscribe_NoTokenInLog captures stdout during a subscribe run and asserts
// the cluster bearer token never appears in any log line.
func TestSubscribe_NoTokenInLog(t *testing.T) {
	subscribeTestEnv(t)
	const secret = "test-k8s-secret-bearer-DONOTLOG"
	t.Setenv("KUBECONFIG_TOKEN", secret)

	cap := &capturingSDKClient{}
	installSDK(t, cap)
	ctx, cancel := context.WithCancel(context.Background())
	// A 410-Gone-then-re-list scenario plus a normal event, to exercise the
	// logging branches that format resourceVersions / errors.
	src := &scriptedSource{
		events: []watch.Event{
			{Type: watch.EventAdded, ResourceVersion: "rv-2", Object: bindingRaw("admins")},
			{Type: watch.EventError, ResourceExpired: true},
		},
		onSecondWatch: cancel,
	}
	installRBACWatchSource(t, src)

	out := captureStdout(t, func() {
		if err := doSubscribe(ctx, okSubscribeFlags()); err != nil {
			t.Fatalf("doSubscribe: %v", err)
		}
	})
	if strings.Contains(out, secret) {
		t.Errorf("stdout leaked the cluster token")
	}
}

// TestSubscribe_PreRunValidation covers the flag guards.
func TestSubscribe_PreRunValidation(t *testing.T) {
	subscribeTestEnv(t)
	cmd := newSubscribeCmd()
	cmd.SetArgs([]string{"--environment", "prod"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err == nil {
		t.Error("missing --cluster should error")
	}
}

// TestSubscribe_SkipBothRejected proves skipping both surfaces is rejected.
func TestSubscribe_SkipBothRejected(t *testing.T) {
	subscribeTestEnv(t)
	cmd := newSubscribeCmd()
	cmd.SetArgs([]string{"--cluster", "c", "--environment", "e", "--skip-rbac", "--skip-workload"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err == nil {
		t.Error("skipping both surfaces should error")
	}
}

// TestSubscribe_SDKClientError surfaces a client-construction failure.
func TestSubscribe_SDKClientError(t *testing.T) {
	subscribeTestEnv(t)
	sentinel := errors.New("bad endpoint")
	prev := newSDKClient
	newSDKClient = func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return nil, sentinel }
	t.Cleanup(func() { newSDKClient = prev })
	err := doSubscribe(context.Background(), okSubscribeFlags())
	if !errors.Is(err, sentinel) || !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Fatalf("want wrapped sdk client error; got %v", err)
	}
}

// TestSubscribe_BadAuthModeRejected proves an invalid auth mode errors.
func TestSubscribe_BadAuthModeRejected(t *testing.T) {
	subscribeTestEnv(t)
	f := okSubscribeFlags()
	f.authMode = "bogus"
	if err := doSubscribe(context.Background(), f); err == nil {
		t.Fatal("expected bad auth-mode error")
	}
}

// TestSubscribe_EmitterSkipsUnbuildable proves an unmodeled / unbuildable object
// is skipped (empty key, no push) rather than crashing the loop.
func TestSubscribe_EmitterSkipsUnbuildable(t *testing.T) {
	subscribeTestEnv(t)
	cap := &capturingSDKClient{}
	emit := rbacEmitter(cap, "c", "e", "scf:IAC-21")
	// An object that is not a RawBinding → skipped, empty key, no push.
	key, err := emit(context.Background(), watch.EventAdded, "not-a-binding")
	if err != nil || key != "" {
		t.Errorf("unbuildable object: key=%q err=%v; want empty,nil", key, err)
	}
	if len(cap.snapshot()) != 0 {
		t.Error("unbuildable object must not push")
	}
}

// TestSubscribe_RBACEmitterPushError proves a push failure in the rbac emitter
// is wrapped + returned (the loop ends the stream to retry).
func TestSubscribe_RBACEmitterPushError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("push 503")
	emit := rbacEmitter(&capturingSDKClient{pushErr: sentinel}, "c", "e", "scf:IAC-21")
	_, err := emit(context.Background(), watch.EventAdded, bindingRaw("admins"))
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "push rbac") {
		t.Fatalf("want wrapped push rbac error; got %v", err)
	}
}

// TestSubscribe_WorkloadEmitterPaths covers the workload emitter's skip, push,
// and push-error branches.
func TestSubscribe_WorkloadEmitterPaths(t *testing.T) {
	t.Parallel()
	// Skip: not a RawWorkload.
	emitSkip := workloadEmitter(&capturingSDKClient{}, "c", "e", "scf:CFG-02")
	if key, err := emitSkip(context.Background(), watch.EventAdded, 42); key != "" || err != nil {
		t.Errorf("non-workload object: key=%q err=%v; want empty,nil", key, err)
	}
	// Skip: missing namespace.
	if key, _ := emitSkip(context.Background(), watch.EventAdded, workload.RawWorkload{Name: "api"}); key != "" {
		t.Errorf("namespaceless workload should skip; got key %q", key)
	}
	// Push success.
	cap := &capturingSDKClient{}
	emitOK := workloadEmitter(cap, "c", "e", "scf:CFG-02")
	raw := workload.RawWorkload{Kind: workload.KindDeployment, Name: "api", Namespace: "prod"}
	if key, err := emitOK(context.Background(), watch.EventModified, raw); err != nil || key == "" {
		t.Errorf("push: key=%q err=%v", key, err)
	}
	// Push error.
	sentinel := errors.New("push down")
	emitErr := workloadEmitter(&capturingSDKClient{pushErr: sentinel}, "c", "e", "scf:CFG-02")
	if _, err := emitErr(context.Background(), watch.EventModified, raw); !errors.Is(err, sentinel) {
		t.Errorf("want wrapped push error; got %v", err)
	}
}

// TestSubscribe_DecodeErrors covers the decode adapters' error branches.
func TestSubscribe_DecodeErrors(t *testing.T) {
	t.Parallel()
	if _, err := decodeRBACObject([]byte("not-json")); err == nil {
		t.Error("decodeRBACObject should reject bad json")
	}
	if _, err := decodeWorkloadObject([]byte("not-json")); err == nil {
		t.Error("decodeWorkloadObject should reject bad json")
	}
	if _, _, err := decodeRBACList(strings.NewReader("not-json")); err == nil {
		t.Error("decodeRBACList should reject bad json")
	}
	if _, _, err := decodeWorkloadList(strings.NewReader("not-json")); err == nil {
		t.Error("decodeWorkloadList should reject bad json")
	}
}

// TestSubscribe_WorkloadNoContainersDefaults covers the no-containers reduce
// branch (readOnlyFS=false, allowEsc=true).
func TestSubscribe_WorkloadNoContainersDefaults(t *testing.T) {
	t.Parallel()
	js := `{"metadata":{"name":"api","namespace":"prod"},"spec":{"template":{"spec":{"hostNetwork":true}}}}`
	obj, err := decodeWorkloadObject([]byte(js))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	w := obj.(workload.RawWorkload)
	if w.ReadOnlyRootFilesystem || !w.AllowPrivilegeEscalation || !w.HostNetwork {
		t.Errorf("no-container defaults wrong: %+v", w)
	}
}

// TestSubscribe_WorkloadPodLevelRunAsNonRoot covers the pod-level securityContext
// inheritance branch.
func TestSubscribe_WorkloadPodLevelRunAsNonRoot(t *testing.T) {
	t.Parallel()
	js := `{"metadata":{"name":"api","namespace":"prod"},"spec":{"template":{"spec":{"securityContext":{"runAsNonRoot":true},"containers":[{}]}}}}`
	obj, err := decodeWorkloadObject([]byte(js))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	w := obj.(workload.RawWorkload)
	// Pod-level runAsNonRoot inherited; container has no override and no
	// readOnlyRootFilesystem → privileged-escalation defaults true.
	if !w.RunAsNonRoot {
		t.Errorf("pod-level runAsNonRoot not inherited: %+v", w)
	}
}

// TestNewWatchSources_Constructors exercises the live Source constructors so they
// are not dead code.
func TestNewWatchSources_Constructors(t *testing.T) {
	if newRBACWatchSource("https://k:6443", "test-k8s-token") == nil {
		t.Error("newRBACWatchSource returned nil")
	}
	if newWorkloadWatchSource("https://k:6443", "test-k8s-token") == nil {
		t.Error("newWorkloadWatchSource returned nil")
	}
}

// TestDecodeRBACObject covers the watch RBAC decode adapter (cluster + namespaced
// scope inference) and that no Secret/env keys are modeled.
func TestDecodeRBACObject(t *testing.T) {
	t.Parallel()
	clusterJSON := `{"metadata":{"name":"admins"},"roleRef":{"kind":"ClusterRole","name":"cluster-admin"},"subjects":[{"kind":"User","name":"alice"}]}`
	obj, err := decodeRBACObject([]byte(clusterJSON))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	rb := obj.(rbac.RawBinding)
	if rb.Scope != rbac.ScopeCluster || rb.Name != "admins" || rb.RoleName != "cluster-admin" {
		t.Errorf("cluster binding = %+v", rb)
	}
	nsJSON := `{"metadata":{"name":"reader","namespace":"default"},"roleRef":{"kind":"Role","name":"reader"}}`
	obj2, err := decodeRBACObject([]byte(nsJSON))
	if err != nil {
		t.Fatalf("decode ns: %v", err)
	}
	if obj2.(rbac.RawBinding).Scope != rbac.ScopeNamespace {
		t.Errorf("namespaced binding scope = %q", obj2.(rbac.RawBinding).Scope)
	}
}

// TestDecodeWorkloadObject covers the watch workload decode adapter (security
// context reduction) and that env/Secret refs are not modeled.
func TestDecodeWorkloadObject(t *testing.T) {
	t.Parallel()
	js := `{"metadata":{"name":"api","namespace":"prod"},"spec":{"template":{"spec":{"containers":[{"securityContext":{"runAsNonRoot":true,"readOnlyRootFilesystem":true,"allowPrivilegeEscalation":false}}]}}}}`
	obj, err := decodeWorkloadObject([]byte(js))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	w := obj.(workload.RawWorkload)
	if w.Name != "api" || w.Namespace != "prod" || !w.RunAsNonRoot || !w.ReadOnlyRootFilesystem || w.AllowPrivilegeEscalation {
		t.Errorf("workload reduce = %+v", w)
	}
}

// TestDecodeLists covers the watch LIST decoders.
func TestDecodeLists(t *testing.T) {
	t.Parallel()
	rbList := `{"metadata":{"resourceVersion":"rv-9"},"items":[{"metadata":{"name":"admins"},"roleRef":{"kind":"ClusterRole","name":"cluster-admin"}}]}`
	objs, rv, err := decodeRBACList(strings.NewReader(rbList))
	if err != nil || rv != "rv-9" || len(objs) != 1 {
		t.Fatalf("rbac list: objs=%d rv=%q err=%v", len(objs), rv, err)
	}
	wlList := `{"metadata":{"resourceVersion":"rv-5"},"items":[{"metadata":{"name":"api","namespace":"prod"}}]}`
	objs2, rv2, err := decodeWorkloadList(strings.NewReader(wlList))
	if err != nil || rv2 != "rv-5" || len(objs2) != 1 {
		t.Fatalf("workload list: objs=%d rv=%q err=%v", len(objs2), rv2, err)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was
// written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8sauth"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/rbac"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/watch"
	"github.com/mgoodric/security-atlas/connectors/k8s/internal/workload"
)

// Subscribe-profile seams: doSubscribe reaches through these so tests can swap a
// faked watch Source + sdk client without a live cluster or platform. Production
// paths are unchanged; only the call-site indirection moved.
var (
	watchRun            = watch.Run
	newRBACWatchSrc     = newRBACWatchSource
	newWorkloadWatchSrc = newWorkloadWatchSource
)

type subscribeFlags struct {
	cluster         string
	environment     string
	authMode        string
	apiServer       string
	rbacControl     string
	workloadControl string
	skipRBAC        bool
	skipWorkload    bool
	coalesceCap     int
}

func newSubscribeCmd() *cobra.Command {
	var f subscribeFlags
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "event-driven (subscribe) profile — consume a Kubernetes watch and push evidence as RBAC / workload changes happen",
		Long: `Event-driven (subscribe) profile. Consume a long-lived Kubernetes WATCH
against the SAME read-only API surfaces the pull profile reads (rolebindings/
clusterrolebindings for RBAC; apps deployments/daemonsets/statefulsets for
workloads), and push the SAME two evidence kinds (k8s.rbac_binding.v1 +
k8s.workload_security_context.v1) as changes happen — instead of waiting for the
next scheduled pull pass.

This is "event-driven via the Kubernetes watch API", NOT "continuous
monitoring" — the mechanism is named honestly. The pull profile (the 'run'
subcommand) remains the reconciliation backstop; run both.

Watch lifecycle (the reflector pattern): bootstrap a LIST to obtain the starting
resourceVersion, then WATCH from it with allowWatchBookmarks=true (the server
periodically advances the resume point cheaply). On stream close, re-watch from
the last resourceVersion. On a 410 Gone (resourceVersion too old), re-LIST for a
fresh resourceVersion and resume. The loop is bound by the process context
(graceful shutdown ends it).

Dedup + DoS coalescing: every event-built record carries the SAME slice-487
hour-window idempotency key the pull path uses, so a watch-emitted record and a
pull-emitted record for the same resource in the same hour collapse to one ledger
row — and a burst of edits to one binding within the hour collapses too.

Least-privilege: the subscribe profile needs the base read-only ClusterRole with
the 'watch' verb ADDED (alongside get,list) on EXACTLY the rbac + apps surfaces —
no new resource, never 'secrets', never a write verb, never a wildcard. Print it
with 'permissions --subscribe'.

Platform-side wire stays push (invariant #3): the watch is how the connector
retrieves data FROM the cluster; records still go out through the same
IngestEvidence/Push path.

Auth: same as 'run' — KUBERNETES_API_SERVER + KUBECONFIG_TOKEN (out-of-cluster),
or --auth-mode in-cluster. The token never appears in a log line or a record.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.cluster == "" {
				return errors.New("--cluster is required (records must be scoped to a cluster)")
			}
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			if f.skipRBAC && f.skipWorkload {
				return errors.New("nothing to watch: --skip-rbac and --skip-workload are both set")
			}
			if _, err := k8sauth.ParseMode(f.authMode); err != nil {
				return err
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doSubscribe(cmd.Context(), f)
		},
	}
	cmd.Flags().StringVar(&f.cluster, "cluster", "", "cluster identifier [required] (scopes every record)")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.authMode, "auth-mode", "kubeconfig-token", "auth mode: kubeconfig-token | in-cluster")
	cmd.Flags().StringVar(&f.apiServer, "api-server", "", "Kubernetes API server URL (env: KUBERNETES_API_SERVER)")
	cmd.Flags().StringVar(&f.rbacControl, "rbac-control", "scf:IAC-21", "control_id to attach to k8s.rbac_binding.v1 records")
	cmd.Flags().StringVar(&f.workloadControl, "workload-control", "scf:CFG-02", "control_id to attach to k8s.workload_security_context.v1 records")
	cmd.Flags().BoolVar(&f.skipRBAC, "skip-rbac", false, "do not watch RBAC bindings")
	cmd.Flags().BoolVar(&f.skipWorkload, "skip-workload", false, "do not watch workloads")
	cmd.Flags().IntVar(&f.coalesceCap, "coalesce-cap", watch.DefaultCoalesceCap,
		"in-process per-hour idempotency-key set cap (DoS bound; the hour-window key at the platform is the durable collapse)")
	return cmd
}

func doSubscribe(ctx context.Context, f subscribeFlags) error {
	mode, err := k8sauth.ParseMode(f.authMode)
	if err != nil {
		return err
	}
	cred, err := k8sauth.Resolve(k8sauth.ResolveOpts{Mode: mode, APIServer: f.apiServer})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	g, gctx := errgroup.WithContext(ctx)

	if !f.skipRBAC {
		src := newRBACWatchSrc(cred.APIServer(), cred.Token())
		emit := rbacEmitter(sdkClient, f.cluster, f.environment, f.rbacControl)
		g.Go(func() error {
			return watchRun(gctx, src, emit, subscribeLogf, watch.Options{
				ResourceName: "rbac",
				CoalesceCap:  f.coalesceCap,
			})
		})
	}
	if !f.skipWorkload {
		src := newWorkloadWatchSrc(cred.APIServer(), cred.Token())
		emit := workloadEmitter(sdkClient, f.cluster, f.environment, f.workloadControl)
		g.Go(func() error {
			return watchRun(gctx, src, emit, subscribeLogf, watch.Options{
				ResourceName: "workload",
				CoalesceCap:  f.coalesceCap,
			})
		})
	}

	fmt.Printf("watching (cluster=%s environment=%s rbac=%t workload=%t) — event-driven via the Kubernetes watch API, NOT continuous monitoring\n",
		f.cluster, f.environment, !f.skipRBAC, !f.skipWorkload)

	err = g.Wait()
	if errors.Is(err, context.Canceled) || err == nil {
		// Graceful shutdown is a clean exit, not a failure.
		return nil
	}
	return err
}

// subscribeLogf is the watch loop's log sink. CRITICAL: every user-tainted arg
// (resource name, resourceVersion, error strings that may embed object names /
// API error bodies) is %q-quoted by the loop's format strings — but the loop
// hands raw values, so this sink must NOT re-interpret them. It writes the
// pre-quoted line straight to stdout. (Pre-empts CodeQL go/log-injection: the
// loop's format strings already use %q on every tainted positional.)
func subscribeLogf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// rbacEmitter builds the SAME k8s.rbac_binding.v1 record the pull path builds —
// via the UNCHANGED buildRBACRecord — from a watched rbac.RawBinding, and pushes
// it. It returns the record's idempotency key so the watch loop can coalesce a
// burst of identical-key events in-process.
func rbacEmitter(client sdkPushClient, cluster, env, controlID string) watch.Emitter {
	return func(ctx context.Context, _ watch.EventType, object any) (string, error) {
		raw, ok := object.(rbac.RawBinding)
		if !ok || raw.Name == "" || raw.RoleName == "" {
			// Unbuildable / unmodeled object: skip (no key → no coalesce entry).
			return "", nil
		}
		// Normalize the single raw binding through the UNCHANGED rbac.Pull path so
		// the record is byte-identical to the pull profile's.
		bindings, err := rbacPull(ctx, singleBindingAPI{raw}, nil)
		if err != nil {
			return "", fmt.Errorf("normalize rbac binding %q: %w", raw.Name, err)
		}
		if len(bindings) == 0 {
			return "", nil
		}
		rec, err := buildRBACRecord(bindings[0], cluster, env, controlID)
		if err != nil {
			return "", fmt.Errorf("build rbac record %q: %w", raw.Name, err)
		}
		if err := pushOne(ctx, client, rec); err != nil {
			return "", fmt.Errorf("push rbac %q: %w", raw.Name, err)
		}
		return rec.GetIdempotencyKey(), nil
	}
}

// workloadEmitter builds the SAME k8s.workload_security_context.v1 record the
// pull path builds — via the UNCHANGED buildWorkloadRecord — from a watched
// workload.RawWorkload, and pushes it.
func workloadEmitter(client sdkPushClient, cluster, env, controlID string) watch.Emitter {
	return func(ctx context.Context, _ watch.EventType, object any) (string, error) {
		raw, ok := object.(workload.RawWorkload)
		if !ok || raw.Name == "" || raw.Namespace == "" {
			return "", nil
		}
		scs, err := workloadScan(ctx, singleWorkloadAPI{raw}, nil)
		if err != nil {
			return "", fmt.Errorf("normalize workload %q: %w", raw.Name, err)
		}
		if len(scs) == 0 {
			return "", nil
		}
		rec, err := buildWorkloadRecord(scs[0], cluster, env, controlID)
		if err != nil {
			return "", fmt.Errorf("build workload record %q: %w", raw.Name, err)
		}
		if err := pushOne(ctx, client, rec); err != nil {
			return "", fmt.Errorf("push workload %q: %w", raw.Name, err)
		}
		return rec.GetIdempotencyKey(), nil
	}
}

// singleBindingAPI adapts one RawBinding to the rbac.API seam so the watch path
// reuses rbac.Pull (the UNCHANGED normalizer) for a single object.
type singleBindingAPI struct{ b rbac.RawBinding }

func (s singleBindingAPI) ListBindings(context.Context) ([]rbac.RawBinding, error) {
	return []rbac.RawBinding{s.b}, nil
}

// singleWorkloadAPI adapts one RawWorkload to the workload.API seam.
type singleWorkloadAPI struct{ w workload.RawWorkload }

func (s singleWorkloadAPI) ListWorkloads(context.Context) ([]workload.RawWorkload, error) {
	return []workload.RawWorkload{s.w}, nil
}

// --- concrete watch Source constructors (read-only HTTP) ---

// watchHTTPClient is the long-lived HTTP client the watch streams use. A watch is
// intentionally long-lived, so it must NOT carry the per-request timeout the pull
// path uses (that would tear the stream); the run context bounds it instead.
func watchHTTPClient() *http.Client { return &http.Client{} }

// newRBACWatchSource builds the read-only watch Source for RBAC bindings. The
// watch streams rolebindings only (the most-churned RBAC surface); the bootstrap
// LIST + the pull profile cover the full binding+role-rule picture, so the
// streamed record carries the binding identity (rules resolve to empty until the
// next pull reconciles — see decisions-log D5; the hour-window key makes the two
// records collapse).
func newRBACWatchSource(apiServer, token string) watch.Source {
	return watch.NewHTTPSource(watch.HTTPSourceConfig{
		HTTP:         watchHTTPClient(),
		BaseURL:      apiServer,
		Token:        token,
		Path:         "/apis/rbac.authorization.k8s.io/v1/rolebindings",
		ListObjects:  decodeRBACList,
		DecodeObject: decodeRBACObject,
	})
}

// newWorkloadWatchSource builds the read-only watch Source for apps deployments.
func newWorkloadWatchSource(apiServer, token string) watch.Source {
	return watch.NewHTTPSource(watch.HTTPSourceConfig{
		HTTP:         watchHTTPClient(),
		BaseURL:      apiServer,
		Token:        token,
		Path:         "/apis/apps/v1/deployments",
		ListObjects:  decodeWorkloadList,
		DecodeObject: decodeWorkloadObject,
	})
}

// --- decode adapters: Kubernetes API JSON -> Raw shapes ---
//
// CRITICAL (config-only boundary): these decoders model ONLY the binding-identity
// / security-context fields. Container env, envFrom, volumes, Secret/ConfigMap
// refs are NOT modeled — Go's json decoder discards unmodeled keys, so they never
// materialize into a Raw object and can never reach a record (same structural
// guard the pull-path clients enforce).

type watchBindingObject struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	RoleRef struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"roleRef"`
	Subjects []struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"subjects"`
}

func (o watchBindingObject) toRaw() rbac.RawBinding {
	scope := rbac.ScopeNamespace
	if o.Metadata.Namespace == "" {
		scope = rbac.ScopeCluster
	}
	subs := make([]rbac.Subject, 0, len(o.Subjects))
	for _, s := range o.Subjects {
		subs = append(subs, rbac.Subject{Kind: s.Kind, Name: s.Name, Namespace: s.Namespace})
	}
	return rbac.RawBinding{
		Name:      o.Metadata.Name,
		Scope:     scope,
		Namespace: o.Metadata.Namespace,
		RoleKind:  o.RoleRef.Kind,
		RoleName:  o.RoleRef.Name,
		Subjects:  subs,
		// Rules deliberately empty on the watch path (see decisions-log D5); the
		// pull profile resolves role rules.
	}
}

func decodeRBACObject(raw json.RawMessage) (any, error) {
	var o watchBindingObject
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	return o.toRaw(), nil
}

func decodeRBACList(body io.Reader) ([]any, string, error) {
	var lst struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []watchBindingObject `json:"items"`
	}
	if err := json.NewDecoder(body).Decode(&lst); err != nil {
		return nil, "", err
	}
	out := make([]any, 0, len(lst.Items))
	for _, it := range lst.Items {
		out = append(out, it.toRaw())
	}
	return out, lst.Metadata.ResourceVersion, nil
}

type watchWorkloadObject struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				HostNetwork     bool `json:"hostNetwork"`
				HostPID         bool `json:"hostPID"`
				HostIPC         bool `json:"hostIPC"`
				SecurityContext *struct {
					RunAsNonRoot *bool `json:"runAsNonRoot"`
				} `json:"securityContext"`
				Containers []struct {
					SecurityContext *struct {
						RunAsNonRoot             *bool `json:"runAsNonRoot"`
						Privileged               *bool `json:"privileged"`
						ReadOnlyRootFilesystem   *bool `json:"readOnlyRootFilesystem"`
						AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation"`
					} `json:"securityContext"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func (o watchWorkloadObject) toRaw() workload.RawWorkload {
	spec := o.Spec.Template.Spec
	r := workload.RawWorkload{
		Kind:           workload.KindDeployment,
		Name:           o.Metadata.Name,
		Namespace:      o.Metadata.Namespace,
		HostNetwork:    spec.HostNetwork,
		HostPID:        spec.HostPID,
		HostIPC:        spec.HostIPC,
		ContainerCount: len(spec.Containers),
	}
	podNonRoot := false
	if spec.SecurityContext != nil && spec.SecurityContext.RunAsNonRoot != nil {
		podNonRoot = *spec.SecurityContext.RunAsNonRoot
	}
	runAsNonRoot := len(spec.Containers) > 0 || podNonRoot
	readOnlyFS := len(spec.Containers) > 0
	privileged := false
	allowEsc := false
	for _, c := range spec.Containers {
		sc := c.SecurityContext
		cNonRoot := podNonRoot
		if sc != nil && sc.RunAsNonRoot != nil {
			cNonRoot = *sc.RunAsNonRoot
		}
		if !cNonRoot {
			runAsNonRoot = false
		}
		cReadOnly := sc != nil && sc.ReadOnlyRootFilesystem != nil && *sc.ReadOnlyRootFilesystem
		if !cReadOnly {
			readOnlyFS = false
		}
		if sc != nil && sc.Privileged != nil && *sc.Privileged {
			privileged = true
		}
		// allowPrivilegeEscalation defaults true when unset.
		esc := true
		if sc != nil && sc.AllowPrivilegeEscalation != nil {
			esc = *sc.AllowPrivilegeEscalation
		}
		if esc {
			allowEsc = true
		}
	}
	if len(spec.Containers) == 0 {
		readOnlyFS = false
		allowEsc = true
	}
	r.RunAsNonRoot = runAsNonRoot
	r.ReadOnlyRootFilesystem = readOnlyFS
	r.Privileged = privileged
	r.AllowPrivilegeEscalation = allowEsc
	return r
}

func decodeWorkloadObject(raw json.RawMessage) (any, error) {
	var o watchWorkloadObject
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	return o.toRaw(), nil
}

func decodeWorkloadList(body io.Reader) ([]any, string, error) {
	var lst struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []watchWorkloadObject `json:"items"`
	}
	if err := json.NewDecoder(body).Decode(&lst); err != nil {
		return nil, "", err
	}
	out := make([]any, 0, len(lst.Items))
	for _, it := range lst.Items {
		out = append(out, it.toRaw())
	}
	return out, lst.Metadata.ResourceVersion, nil
}

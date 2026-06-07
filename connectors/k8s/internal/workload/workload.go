// Package workload inspects Kubernetes workload security-context posture — the
// load-bearing signal for the connector's workload evidence kind.
//
// Source: read-only Kubernetes API (get/list on apps deployments / daemonsets /
// statefulsets). The connector reads pod-template security CONTEXT only — NEVER
// Secret values, ConfigMap values, container env, or logs.
package workload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConfigResult enumerates what the connector reports per workload. Maps 1:1 onto
// the gRPC Result enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// Workload controller kinds.
const (
	KindDeployment  = "Deployment"
	KindDaemonSet   = "DaemonSet"
	KindStatefulSet = "StatefulSet"
)

// SecurityContext is the per-workload payload the connector emits. Field names
// map 1:1 to k8s.workload_security_context.v1 schema.
type SecurityContext struct {
	WorkloadKind             string
	WorkloadName             string
	Namespace                string
	RunAsNonRoot             bool
	Privileged               bool
	ReadOnlyRootFilesystem   bool
	AllowPrivilegeEscalation bool
	HostNetwork              bool
	HostPID                  bool
	HostIPC                  bool
	ContainerCount           int
	Result                   ConfigResult
	Reason                   string
	ObservedAt               time.Time
}

// RawWorkload is the narrow view the API surface returns for one workload's
// effective security context. The concrete client maps the Kubernetes API
// response into this shape; tests construct it directly. Security context only —
// no env, no Secret refs resolved, no payload data.
type RawWorkload struct {
	Kind                     string
	Name                     string
	Namespace                string
	RunAsNonRoot             bool
	Privileged               bool
	ReadOnlyRootFilesystem   bool
	AllowPrivilegeEscalation bool
	HostNetwork              bool
	HostPID                  bool
	HostIPC                  bool
	ContainerCount           int
	// ReadError, when non-empty, marks the workload INCONCLUSIVE (a per-workload
	// read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// issues read-only Kubernetes API calls; tests pass a fake. v0 lists the first
// bounded page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListWorkloads(ctx context.Context) ([]RawWorkload, error)
}

// Inspect returns the security-context posture for every visible workload. now
// is injectable for deterministic tests (nil → time.Now UTC).
func Inspect(ctx context.Context, api API, now func() time.Time) ([]SecurityContext, error) {
	if api == nil {
		return nil, errors.New("workload: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListWorkloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workloads: %w", err)
	}
	observedAt := now()
	out := make([]SecurityContext, 0, len(raw))
	for _, r := range raw {
		if r.Name == "" || r.Namespace == "" {
			continue
		}
		sc := SecurityContext{
			WorkloadKind:             normalizeKind(r.Kind),
			WorkloadName:             r.Name,
			Namespace:                r.Namespace,
			RunAsNonRoot:             r.RunAsNonRoot,
			Privileged:               r.Privileged,
			ReadOnlyRootFilesystem:   r.ReadOnlyRootFilesystem,
			AllowPrivilegeEscalation: r.AllowPrivilegeEscalation,
			HostNetwork:              r.HostNetwork,
			HostPID:                  r.HostPID,
			HostIPC:                  r.HostIPC,
			ContainerCount:           r.ContainerCount,
			ObservedAt:               observedAt,
		}
		sc.Result, sc.Reason = verdict(r)
		out = append(out, sc)
	}
	return out, nil
}

// verdict deterministically scores the workload's hardening posture. PASS only
// when it runs non-root, non-privileged, with a read-only root filesystem, no
// privilege escalation, and no host namespaces. FAIL when any of those is off.
// INCONCLUSIVE when the per-workload read errored.
func verdict(r RawWorkload) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read workload security context: " + r.ReadError
	}
	switch {
	case r.Privileged:
		return ResultFail, "a container runs privileged"
	case !r.RunAsNonRoot:
		return ResultFail, "workload does not enforce runAsNonRoot"
	case r.AllowPrivilegeEscalation:
		return ResultFail, "privilege escalation is permitted"
	case !r.ReadOnlyRootFilesystem:
		return ResultFail, "root filesystem is not read-only"
	case r.HostNetwork || r.HostPID || r.HostIPC:
		return ResultFail, "workload shares a host namespace (hostNetwork/hostPID/hostIPC)"
	default:
		return ResultPass, ""
	}
}

func normalizeKind(s string) string {
	switch s {
	case KindDeployment, KindDaemonSet, KindStatefulSet:
		return s
	default:
		return KindDeployment
	}
}

// Package pss assesses Kubernetes Pod-Security-Standards (PSS) admission
// configuration — the load-bearing signal for the connector's
// admission-enforcement evidence kind.
//
// Source: read-only Kubernetes API (get/list on core namespaces — a rule the
// base connector already holds). PSS admission is configured per-namespace via
// labels on the Namespace object:
//
//	pod-security.kubernetes.io/enforce        = privileged | baseline | restricted
//	pod-security.kubernetes.io/enforce-version = <pinned kube minor> | latest
//	pod-security.kubernetes.io/audit          = privileged | baseline | restricted
//	pod-security.kubernetes.io/audit-version  = ...
//	pod-security.kubernetes.io/warn           = privileged | baseline | restricted
//	pod-security.kubernetes.io/warn-version   = ...
//
// The connector reads ONLY the pod-security.kubernetes.io/* labels on each
// namespace — NEVER pod specs, Secret / ConfigMap values, container env, logs,
// nor any other namespace label / annotation. The per-namespace struct has no
// field that could carry workload payload or arbitrary namespace metadata; a
// reflection guard (pss_test.go) fails the build if such a field is added.
//
// Scope discipline (decisions-log D3): namespace PSS LABEL configuration only.
// This does NOT read the cluster's AdmissionConfiguration file (out of API
// reach), validating/mutating webhooks, or third-party policy engines
// (OPA/Gatekeeper, Kyverno) — those are documented follow-ons.
package pss

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Level is a Pod-Security-Standards level. The three canonical PSS levels plus
// the empty sentinel for "no label set in this mode".
type Level string

const (
	// LevelUnset means the namespace carries no label for this PSS mode — the
	// mode is not enforced/audited/warned. Recorded honestly rather than
	// silently assumed-restricted.
	LevelUnset Level = ""
	// LevelPrivileged is the unrestricted PSS level (least hardened).
	LevelPrivileged Level = "privileged"
	// LevelBaseline is the minimally-restrictive PSS level.
	LevelBaseline Level = "baseline"
	// LevelRestricted is the heavily-restricted PSS level (most hardened).
	LevelRestricted Level = "restricted"
)

// Mode names the three PSS admission modes.
const (
	ModeEnforce = "enforce"
	ModeAudit   = "audit"
	ModeWarn    = "warn"
)

// AssessResult enumerates the per-namespace verdict the connector reports. The
// connector emits a DESCRIPTIVE verdict; the platform evaluator owns the final
// policy call (mirrors the netpol / workload kinds).
type AssessResult string

const (
	// ResultPass — the namespace ENFORCES a hardened PSS level (baseline or
	// restricted) at admission.
	ResultPass AssessResult = "pass"
	// ResultFail — the namespace has no enforced PSS level, or enforces only the
	// privileged (unrestricted) level. Admission hardening is not in effect.
	ResultFail AssessResult = "fail"
)

// RawNamespace is the narrow view the API surface returns for one namespace's
// PSS admission configuration. The concrete client maps the Kubernetes API
// response into this shape; tests construct it directly. PSS LABEL metadata
// ONLY — no pod specs, no Secret refs, no arbitrary namespace labels /
// annotations. The client populates these fields exclusively from the
// pod-security.kubernetes.io/* labels.
type RawNamespace struct {
	Name string

	EnforceLevel   Level
	EnforceVersion string
	AuditLevel     Level
	AuditVersion   string
	WarnLevel      Level
	WarnVersion    string
}

// Admission is the per-namespace assessment the connector emits. Field names map
// 1:1 to k8s.pod_security_admission.v1 schema. PSS configuration ONLY — there is
// deliberately NO field for pod specs, secrets, workload contents, or arbitrary
// namespace labels / annotations beyond the three PSS modes + their levels +
// optional pinned versions (structural over-collection guard).
type Admission struct {
	Namespace string

	EnforceLevel   Level
	EnforceVersion string
	AuditLevel     Level
	AuditVersion   string
	WarnLevel      Level
	WarnVersion    string

	// Configured is true when at least one PSS mode carries a level — i.e. PSS
	// admission is configured at all for this namespace.
	Configured bool

	Result     AssessResult
	Reason     string
	ObservedAt time.Time
}

// API is the narrow surface Assess depends on. The concrete implementation
// issues read-only Kubernetes API calls; tests pass a fake. v0 lists the first
// bounded page; cursor pagination is a documented follow-on.
type API interface {
	// ListNamespacePSS returns one RawNamespace per visible namespace, carrying
	// ONLY that namespace's pod-security.kubernetes.io/* label values.
	ListNamespacePSS(ctx context.Context) ([]RawNamespace, error)
}

// maxNamespaces bounds the per-run namespace count the assessment materializes,
// so a pathological cluster (or a hostile API response) cannot blow up memory.
// The client already bounds the page read; this is the assessment-side cap.
const maxNamespaces = 5000

// Assess returns the PSS admission assessment for every visible namespace. now
// is injectable for deterministic tests (nil → time.Now UTC). The namespace
// list is bounded by maxNamespaces.
func Assess(ctx context.Context, api API, now func() time.Time) ([]Admission, error) {
	if api == nil {
		return nil, errors.New("pss: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListNamespacePSS(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespace pss: %w", err)
	}
	observedAt := now()
	out := make([]Admission, 0, len(raw))
	for _, ns := range raw {
		if ns.Name == "" {
			continue
		}
		if len(out) >= maxNamespaces {
			break
		}
		out = append(out, assessNamespace(ns, observedAt))
	}
	return out, nil
}

// assessNamespace derives one namespace's PSS verdict from its labels.
//
// Verdict call (decisions-log D2): a namespace PASSes when it ENFORCES a
// hardened level (baseline or restricted) at admission. It FAILs when no enforce
// level is set (unenforced — recorded honestly) or when enforce is only
// privileged. audit / warn modes are reported but do not drive the verdict:
// they observe / warn, they do not block admission.
func assessNamespace(ns RawNamespace, observedAt time.Time) Admission {
	a := Admission{
		Namespace:      ns.Name,
		EnforceLevel:   normalizeLevel(ns.EnforceLevel),
		EnforceVersion: ns.EnforceVersion,
		AuditLevel:     normalizeLevel(ns.AuditLevel),
		AuditVersion:   ns.AuditVersion,
		WarnLevel:      normalizeLevel(ns.WarnLevel),
		WarnVersion:    ns.WarnVersion,
		ObservedAt:     observedAt,
	}
	a.Configured = a.EnforceLevel != LevelUnset || a.AuditLevel != LevelUnset || a.WarnLevel != LevelUnset
	a.Result, a.Reason = verdict(a)
	return a
}

func verdict(a Admission) (AssessResult, string) {
	switch a.EnforceLevel {
	case LevelRestricted, LevelBaseline:
		return ResultPass, "namespace enforces the " + string(a.EnforceLevel) + " Pod-Security-Standard at admission"
	case LevelPrivileged:
		return ResultFail, "namespace enforces only the privileged (unrestricted) Pod-Security-Standard"
	default:
		if a.AuditLevel != LevelUnset || a.WarnLevel != LevelUnset {
			return ResultFail, "namespace audits/warns but does not ENFORCE a Pod-Security-Standard at admission"
		}
		return ResultFail, "namespace has no Pod-Security-Standards admission labels (unenforced)"
	}
}

// normalizeLevel keeps only the three valid PSS levels; any other value
// (including an unknown / malformed label) collapses to LevelUnset rather than
// being recorded verbatim.
func normalizeLevel(l Level) Level {
	switch l {
	case LevelPrivileged, LevelBaseline, LevelRestricted:
		return l
	default:
		return LevelUnset
	}
}

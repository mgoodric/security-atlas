// Package aks inspects Azure Kubernetes Service (AKS) managed-cluster hardening
// posture — the load-bearing signal for the Azure connector's AKS evidence kind
// (slice 519).
//
// Source: read-only Azure Resource Manager (ARM Reader role) — the SAME role
// the storage kind uses; no new Azure scope (P0-519-2). The connector reads
// managed-cluster CONFIGURATION only — NEVER admin kubeconfig / cluster-admin
// credentials (listClusterAdminCredential is explicitly NOT called: it returns
// admin kubeconfig and is a privilege escalation — P0-519-1), NEVER workload /
// pod manifests, secrets, or container contents (P0-519-3). Cluster-level
// management-plane configuration is the minimum that proves the cloud-config
// control.
package aks

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConfigResult enumerates what the connector reports per cluster. Maps 1:1 onto
// the gRPC Result enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// ClusterConfig is the per-cluster payload the connector emits. Field names map
// 1:1 to azure.aks_cluster_config.v1 schema.
//
// Over-collection guard (P0-519-1 / P0-519-3, structural): this struct carries
// management-plane CONFIGURATION flags ONLY. There is deliberately NO field for
// admin kubeconfig, cluster-admin credentials, service-principal secrets,
// workload/pod manifests, container images, or any secret payload — there is no
// place for such data to land even if a future ARM field exposed it. The
// ConfigOnly test pins this.
type ClusterConfig struct {
	ClusterID             string
	ClusterName           string
	SubscriptionID        string
	ResourceGroup         string
	Location              string
	KubernetesVersion     string
	RBACEnabled           bool
	NetworkPolicy         string // "calico" | "azure" | "" (none)
	PrivateCluster        bool
	AuthorizedIPRanges    bool // API server access restricted to authorized IP ranges
	ManagedIdentity       bool // cluster uses a managed identity (vs a service principal)
	LocalAccountsDisabled bool // local (cluster-admin static) accounts disabled
	OIDCIssuerEnabled     bool
	NodePoolCount         int
	Result                ConfigResult
	Reason                string // human-readable inconclusive / fail reason
	ObservedAt            time.Time
}

// RawCluster is the narrow view the API surface returns for one managed cluster.
// The concrete ARM client maps the SDK response into this shape; tests construct
// it directly.
//
// Over-collection guard: CONFIGURATION fields only — no admin kubeconfig, no
// service-principal secret, no node credentials, no workload manifests.
type RawCluster struct {
	ID                    string
	Name                  string
	ResourceGroup         string
	Location              string
	KubernetesVersion     string
	RBACEnabled           bool
	NetworkPolicy         string
	PrivateCluster        bool
	AuthorizedIPRanges    bool
	ManagedIdentity       bool
	LocalAccountsDisabled bool
	OIDCIssuerEnabled     bool
	NodePoolCount         int
	// ReadError, when non-empty, marks the cluster as INCONCLUSIVE (a per-
	// cluster ARM read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// wraps the read-only Azure Resource Manager managed-clusters list; tests pass a
// fake. v0 lists the first bounded page for one subscription; cursor pagination
// + multi-subscription enumeration are documented follow-ons (threat-model D,
// shared with slice 486 R3).
type API interface {
	ListManagedClusters(ctx context.Context) ([]RawCluster, error)
}

// Inspect returns the hardening posture for every visible AKS managed cluster in
// the subscription. subscriptionID scopes every record. now is injectable for
// deterministic tests (nil -> time.Now UTC).
func Inspect(ctx context.Context, api API, subscriptionID string, now func() time.Time) ([]ClusterConfig, error) {
	if api == nil {
		return nil, errors.New("aks: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListManagedClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list managed clusters: %w", err)
	}
	observedAt := now()
	out := make([]ClusterConfig, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.Name == "" {
			continue
		}
		cfg := ClusterConfig{
			ClusterID:             r.ID,
			ClusterName:           r.Name,
			SubscriptionID:        subscriptionID,
			ResourceGroup:         r.ResourceGroup,
			Location:              r.Location,
			KubernetesVersion:     r.KubernetesVersion,
			RBACEnabled:           r.RBACEnabled,
			NetworkPolicy:         r.NetworkPolicy,
			PrivateCluster:        r.PrivateCluster,
			AuthorizedIPRanges:    r.AuthorizedIPRanges,
			ManagedIdentity:       r.ManagedIdentity,
			LocalAccountsDisabled: r.LocalAccountsDisabled,
			OIDCIssuerEnabled:     r.OIDCIssuerEnabled,
			NodePoolCount:         r.NodePoolCount,
			ObservedAt:            observedAt,
		}
		cfg.Result, cfg.Reason = verdict(r)
		out = append(out, cfg)
	}
	return out, nil
}

// verdict deterministically scores the cluster's hardening posture. PASS only
// when the load-bearing hardening controls are all set; FAIL when a core control
// is off; INCONCLUSIVE when the per-cluster read errored. The platform evaluator
// owns the final pass/fail per (control, scope) — this is a descriptive default.
func verdict(r RawCluster) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read cluster config: " + r.ReadError
	}
	switch {
	case !r.RBACEnabled:
		return ResultFail, "Kubernetes RBAC not enabled"
	case r.NetworkPolicy == "":
		return ResultFail, "no network-policy plugin configured"
	case !r.PrivateCluster && !r.AuthorizedIPRanges:
		return ResultFail, "public API server with no authorized-IP-range restriction"
	default:
		return ResultPass, ""
	}
}

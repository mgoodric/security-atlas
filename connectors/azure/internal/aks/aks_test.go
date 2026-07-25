package aks_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
)

// fakeARM is a faked Azure Resource Manager surface — NO live Azure in tests.
type fakeARM struct {
	clusters []aks.RawCluster
	err      error
}

func (f *fakeARM) ListManagedClusters(_ context.Context) ([]aks.RawCluster, error) {
	return f.clusters, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

func hardened(id, name string) aks.RawCluster {
	return aks.RawCluster{
		ID: id, Name: name, ResourceGroup: "rg", Location: "eastus",
		KubernetesVersion:     "1.29.2",
		RBACEnabled:           true,
		NetworkPolicy:         "calico",
		PrivateCluster:        true,
		AuthorizedIPRanges:    true,
		ManagedIdentity:       true,
		LocalAccountsDisabled: true,
		OIDCIssuerEnabled:     true,
		NodePoolCount:         2,
	}
}

func TestInspect_PassWhenHardened(t *testing.T) {
	api := &fakeARM{clusters: []aks.RawCluster{hardened("/sub/c1", "c1")}}
	got, err := aks.Inspect(context.Background(), api, "sub-1", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 || got[0].Result != aks.ResultPass {
		t.Fatalf("want 1 PASS; got %+v", got)
	}
	if got[0].SubscriptionID != "sub-1" {
		t.Errorf("subscription = %q; want sub-1", got[0].SubscriptionID)
	}
	if got[0].KubernetesVersion != "1.29.2" || got[0].NodePoolCount != 2 {
		t.Errorf("config fields not preserved: %+v", got[0])
	}
}

func TestInspect_PassWhenPublicButAuthorizedIPRanges(t *testing.T) {
	c := hardened("/sub/c", "c")
	c.PrivateCluster = false // public endpoint but authorized-IP-range restricted
	c.AuthorizedIPRanges = true
	got, err := aks.Inspect(context.Background(), &fakeARM{clusters: []aks.RawCluster{c}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != aks.ResultPass {
		t.Errorf("result = %q; want pass (authorized IP ranges compensate)", got[0].Result)
	}
}

func TestInspect_FailMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *aks.RawCluster)
		reason string
	}{
		{"no-rbac", func(c *aks.RawCluster) { c.RBACEnabled = false }, "RBAC"},
		{"no-network-policy", func(c *aks.RawCluster) { c.NetworkPolicy = "" }, "network-policy"},
		{"public-no-authorized-ips", func(c *aks.RawCluster) { c.PrivateCluster = false; c.AuthorizedIPRanges = false }, "public API server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := hardened("/sub/c", "c")
			tc.mutate(&c)
			got, err := aks.Inspect(context.Background(), &fakeARM{clusters: []aks.RawCluster{c}}, "sub", fixedNow)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if got[0].Result != aks.ResultFail {
				t.Errorf("result = %q; want fail", got[0].Result)
			}
			if !strings.Contains(got[0].Reason, tc.reason) {
				t.Errorf("reason = %q; want contains %q", got[0].Reason, tc.reason)
			}
		})
	}
}

func TestInspect_InconclusiveOnReadError(t *testing.T) {
	c := hardened("/sub/c", "c")
	c.ReadError = "throttled"
	got, err := aks.Inspect(context.Background(), &fakeARM{clusters: []aks.RawCluster{c}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != aks.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", got[0].Result)
	}
}

func TestInspect_SkipsIncompleteClusters(t *testing.T) {
	api := &fakeARM{clusters: []aks.RawCluster{
		{ID: "", Name: "x"},
		{ID: "y", Name: ""},
		hardened("/sub/ok", "ok"),
	}}
	got, _ := aks.Inspect(context.Background(), api, "sub", fixedNow)
	if len(got) != 1 || got[0].ClusterName != "ok" {
		t.Fatalf("expected 1 valid cluster; got %+v", got)
	}
}

func TestInspect_PropagatesListError(t *testing.T) {
	sentinel := errors.New("arm 403")
	_, err := aks.Inspect(context.Background(), &fakeARM{err: sentinel}, "sub", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestInspect_NilAPIRejected(t *testing.T) {
	_, err := aks.Inspect(context.Background(), nil, "sub", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

// P0-519-1 / P0-519-3 (structural over-collection guard): the ClusterConfig +
// RawCluster structs must carry management-plane CONFIGURATION fields ONLY —
// never admin kubeconfig, cluster-admin credentials, service-principal secrets,
// workload/pod manifests, container images, or any secret payload. This test
// reflects over both structs' field names and FAILS if any field name even
// hints at a secret / credential / kubeconfig / workload-content surface, so a
// future field that opens an over-collection door trips the build.
func TestStructs_ConfigOnly_NoSecretFields(t *testing.T) {
	banned := []string{
		"secret", "credential", "kubeconfig", "password", "token", "key",
		"manifest", "podspec", "container", "image", "privatekey", "saskey",
		"clientsecret", "adminkube",
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned over-collection token %q — config-only struct must not carry secret/credential/kubeconfig/workload data",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(aks.ClusterConfig{}))
	check(reflect.TypeOf(aks.RawCluster{}))
}

// TestConfigOnly_PayloadFieldsPreserved pins that the documented config fields
// survive the Inspect transform (the positive companion to the structural
// guard).
func TestConfigOnly_PayloadFieldsPreserved(t *testing.T) {
	api := &fakeARM{clusters: []aks.RawCluster{hardened("/sub/c", "c")}}
	got, _ := aks.Inspect(context.Background(), api, "sub", fixedNow)
	c := got[0]
	if !c.RBACEnabled || c.NetworkPolicy != "calico" || !c.PrivateCluster ||
		!c.AuthorizedIPRanges || !c.ManagedIdentity || !c.LocalAccountsDisabled ||
		!c.OIDCIssuerEnabled {
		t.Errorf("config fields not preserved: %+v", c)
	}
}

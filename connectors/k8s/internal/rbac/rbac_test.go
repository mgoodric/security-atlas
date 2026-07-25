package rbac

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAPI struct {
	bindings []RawBinding
	err      error
}

func (f *fakeAPI) ListBindings(_ context.Context) ([]RawBinding, error) {
	return f.bindings, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }
}

func TestPull_NormalizesAndFlagsWildcard(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{bindings: []RawBinding{
		{
			Name:     "cluster-admin-binding",
			Scope:    ScopeCluster,
			RoleKind: RoleKindClusterRole,
			RoleName: "cluster-admin",
			Subjects: []Subject{{Kind: SubjectUser, Name: "alice"}},
			Rules:    []Rule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}},
		},
		{
			Name:      "reader-binding",
			Scope:     ScopeNamespace,
			Namespace: "default",
			RoleKind:  RoleKindRole,
			RoleName:  "reader",
			Subjects:  []Subject{{Kind: SubjectServiceAccount, Name: "sa", Namespace: "default"}},
			Rules:     []Rule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}},
		},
	}}
	got, err := Pull(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if !got[0].GrantsWildcard {
		t.Error("cluster-admin binding should flag grants_wildcard")
	}
	if got[1].GrantsWildcard {
		t.Error("reader binding should NOT flag grants_wildcard")
	}
	if got[0].ObservedAt != fixedNow()() {
		t.Errorf("observedAt = %v", got[0].ObservedAt)
	}
}

func TestPull_SkipsInvalid(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{bindings: []RawBinding{
		{Name: "", RoleName: "x"}, // missing name
		{Name: "y", RoleName: ""}, // missing role name
		{Name: "ok", RoleName: "role", Scope: ScopeCluster, RoleKind: RoleKindClusterRole},
	}}
	got, err := Pull(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(got) != 1 || got[0].BindingName != "ok" {
		t.Fatalf("want only the valid binding; got %+v", got)
	}
}

func TestPull_NormalizesUnknownScopeAndKind(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{bindings: []RawBinding{
		{Name: "b", RoleName: "r", Scope: "bogus", RoleKind: "bogus"},
	}}
	got, _ := Pull(context.Background(), api, fixedNow())
	if got[0].BindingScope != ScopeNamespace {
		t.Errorf("scope = %q; want default namespace", got[0].BindingScope)
	}
	if got[0].RoleKind != RoleKindClusterRole {
		t.Errorf("roleKind = %q; want default ClusterRole", got[0].RoleKind)
	}
}

func TestPull_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Pull(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestPull_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403 forbidden")
	if _, err := Pull(context.Background(), &fakeAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestPull_DefaultNow(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{bindings: []RawBinding{{Name: "b", RoleName: "r", Scope: ScopeCluster, RoleKind: RoleKindClusterRole}}}
	got, _ := Pull(context.Background(), api, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set from time.Now")
	}
}

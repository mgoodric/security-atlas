package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Tests for security-atlas#1739: the tenant an OAuth client is bound to,
// and `atlas-cli oauth bind-client`. The store's Bind is exercised against
// real Postgres in internal/auth/oauthclient's integration suite; here the
// command body runs against a fake binder and resolver.

func resolverReturning(ref tenancy.TenantRef, err error) tenantResolver {
	return func(context.Context) (tenancy.TenantRef, error) { return ref, err }
}

func resolverMustNotRun(t *testing.T) tenantResolver {
	return func(context.Context) (tenancy.TenantRef, error) {
		t.Fatal("resolver ran although --tenant was given")
		return tenancy.TenantRef{}, nil
	}
}

func TestChooseClientTenant(t *testing.T) {
	t.Parallel()
	only := uuid.New()
	flagged := uuid.New()
	ambiguous := &tenancy.AmbiguousTenantError{Tenants: []tenancy.TenantRef{{ID: uuid.New()}, {ID: uuid.New()}}}

	cases := []struct {
		name    string
		flag    string
		resolve tenantResolver
		want    uuid.UUID
		wantErr string
	}{
		{name: "flag wins", flag: flagged.String(), resolve: resolverMustNotRun(t), want: flagged},
		{name: "flag not a uuid", flag: "acme", resolve: resolverMustNotRun(t), wantErr: "not a UUID"},
		{name: "flag all-zeros refused", flag: uuid.Nil.String(), resolve: resolverMustNotRun(t), wantErr: "all-zeros"},
		{name: "single tenant implied", resolve: resolverReturning(tenancy.TenantRef{ID: only}, nil), want: only},
		{name: "several tenants refused", resolve: resolverReturning(tenancy.TenantRef{}, ambiguous), wantErr: "pass --tenant"},
		{name: "no tenant refused", resolve: resolverReturning(tenancy.TenantRef{}, tenancy.ErrNoTenants), wantErr: "pass --tenant"},
		{name: "resolver error surfaces", resolve: resolverReturning(tenancy.TenantRef{}, errors.New("db down")), wantErr: "db down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := chooseClientTenant(context.Background(), tc.flag, tc.resolve)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				if got != uuid.Nil {
					t.Errorf("got %s alongside an error, want uuid.Nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("tenant = %s, want %s", got, tc.want)
			}
		})
	}
}

type fakeBinder struct {
	gotClientID string
	gotTenant   uuid.UUID
	err         error
}

func (f *fakeBinder) Bind(_ context.Context, clientID string, tenant uuid.UUID) (*oauthclient.Client, error) {
	f.gotClientID, f.gotTenant = clientID, tenant
	if f.err != nil {
		return nil, f.err
	}
	return &oauthclient.Client{ClientID: clientID, TenantID: &tenant}, nil
}

func TestRunOAuthBindClient_BindsToFlagTenant(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	b := &fakeBinder{}
	var out bytes.Buffer
	if err := runOAuthBindClient(context.Background(), &out, b, resolverMustNotRun(t), "client-1", tenant.String()); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b.gotClientID != "client-1" || b.gotTenant != tenant {
		t.Errorf("Bind(%q, %s), want Bind(client-1, %s)", b.gotClientID, b.gotTenant, tenant)
	}
	if want := "client_id: client-1\ntenant_id: " + tenant.String() + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunOAuthBindClient_DefaultsToOnlyTenant(t *testing.T) {
	t.Parallel()
	only := uuid.New()
	b := &fakeBinder{}
	if err := runOAuthBindClient(context.Background(), &bytes.Buffer{}, b,
		resolverReturning(tenancy.TenantRef{ID: only}, nil), "client-1", ""); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b.gotTenant != only {
		t.Errorf("bound to %s, want the only tenant %s", b.gotTenant, only)
	}
}

func TestRunOAuthBindClient_AmbiguousTenantRefused(t *testing.T) {
	t.Parallel()
	b := &fakeBinder{}
	amb := &tenancy.AmbiguousTenantError{Tenants: []tenancy.TenantRef{{ID: uuid.New()}, {ID: uuid.New()}}}
	err := runOAuthBindClient(context.Background(), &bytes.Buffer{}, b,
		resolverReturning(tenancy.TenantRef{}, amb), "client-1", "")
	if err == nil || !strings.Contains(err.Error(), "pass --tenant") {
		t.Fatalf("err = %v, want a refusal naming --tenant", err)
	}
	if b.gotClientID != "" {
		t.Error("Bind ran although the tenant was ambiguous")
	}
}

func TestRunOAuthBindClient_StoreErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		wantErr string
	}{
		{"unknown client", oauthclient.ErrUnknownClient, "no active OAuth client"},
		{"bound elsewhere", oauthclient.ErrAlreadyBound, "already bound to a different tenant"},
		{"other", errors.New("boom"), "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := runOAuthBindClient(context.Background(), &bytes.Buffer{}, &fakeBinder{err: tc.err},
				resolverMustNotRun(t), "client-1", uuid.NewString())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestRunOAuthBindClient_RequiresClientID(t *testing.T) {
	t.Parallel()
	if err := runOAuthBindClient(context.Background(), &bytes.Buffer{}, &fakeBinder{},
		resolverMustNotRun(t), "", uuid.NewString()); err == nil {
		t.Fatal("expected an error for an empty client_id")
	}
}

func TestOAuthIssueClient_TenantAndUnboundExclusive(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused")
	cmd := newOAuthCmd()
	cmd.SetArgs([]string{"issue-client", "c", "--tenant", uuid.NewString(), "--unbound"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive", err)
	}
}

func TestOAuthBindClient_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	cmd := newOAuthCmd()
	cmd.SetArgs([]string{"bind-client", "client-1", "--tenant", uuid.NewString()})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want it to mention DATABASE_URL", err)
	}
}

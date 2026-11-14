package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Tests for #1762: the tenant flags on the database-backed commands
// (evidence verify, policy seed-stock, oscal-export) are optional on a
// single-tenant install and refused, with the tenants listed, on a
// multi-tenant one. The resolver stands in for the tenants table here;
// tenancy.ResolveImplicitTenantAnyRole runs against real Postgres in
// internal/tenancy's integration suite. The credentials commands are
// API-backed and default to the credential's tenant server-side.

// unreachableDSN opens lazily and fails fast at first use, so a command
// gets past its tenant choice and stops at its first real query.
const unreachableDSN = "postgres://atlas:atlas@127.0.0.1:1/atlas?connect_timeout=1"

var (
	soloTenant = tenancy.TenantRef{ID: uuid.MustParse("6f1c1f43-6c55-4cf6-9d5e-1b8f0c1a0001"), Slug: "solo-org", Name: "Solo Org"}
	acmeTenant = tenancy.TenantRef{ID: uuid.MustParse("6f1c1f43-6c55-4cf6-9d5e-1b8f0c1a0002"), Slug: "acme", Name: "Acme Corp"}
	betaTenant = tenancy.TenantRef{ID: uuid.MustParse("6f1c1f43-6c55-4cf6-9d5e-1b8f0c1a0003"), Slug: "beta", Name: "Beta Ltd"}
)

// stubTenants replaces the commands' resolver for one test with one that
// answers as tenancy.ResolveImplicitTenant would over tenants.
func stubTenants(t *testing.T, tenants ...tenancy.TenantRef) {
	t.Helper()
	prev := newTenantResolver
	newTenantResolver = func(*pgxpool.Pool) tenantResolver {
		return func(context.Context) (tenancy.TenantRef, error) {
			switch len(tenants) {
			case 0:
				return tenancy.TenantRef{}, tenancy.ErrNoTenants
			case 1:
				return tenants[0], nil
			}
			return tenancy.TenantRef{}, &tenancy.AmbiguousTenantError{Tenants: tenants}
		}
	}
	t.Cleanup(func() { newTenantResolver = prev })
}

type dbCommand struct {
	name string
	flag string
	cmd  func() *cobra.Command
	args []string
}

func dbCommands() []dbCommand {
	return []dbCommand{
		{name: "evidence verify", flag: "--tenant", cmd: newEvidenceVerifyCmd,
			args: []string{"--dsn=" + unreachableDSN}},
		{name: "policy seed-stock", flag: "--tenant-id", cmd: newPolicySeedStockCmd,
			args: []string{"--dsn=" + unreachableDSN}},
		{name: "oscal-export", flag: "--tenant-id", cmd: newOscalCmd,
			args: []string{"--dsn=" + unreachableDSN, "--period-id=" + uuid.NewString(), "--out=" + os.TempDir(), "--bridge-addr=127.0.0.1:1"}},
	}
}

// runDBCommand runs c with its args and returns its stderr and error.
// evidence verify reports operational errors on stderr and through
// exitVerify, which is stubbed out so the test process survives.
func runDBCommand(t *testing.T, c dbCommand) (string, error) {
	t.Helper()
	prevExit := exitVerify
	exitVerify = func(int) {}
	t.Cleanup(func() { exitVerify = prevExit })

	cmd := c.cmd()
	cmd.SetArgs(c.args)
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return stderr.String(), err
}

func TestDBCommands_SingleTenant_NoFlag_UsesOnlyTenant(t *testing.T) {
	for _, c := range dbCommands() {
		t.Run(c.name, func(t *testing.T) {
			stubTenants(t, soloTenant)
			stderr, err := runDBCommand(t, c)
			if !strings.Contains(stderr, `tenant: "Solo Org", the install's only tenant`) {
				t.Fatalf("stderr = %q, want the only tenant named", stderr)
			}
			// Past the tenant choice the command reaches the database,
			// which is unreachable here; that failure must not be a
			// complaint about the missing flag.
			for _, s := range []string{stderr, errText(err)} {
				if strings.Contains(s, c.flag+" is required") || strings.Contains(s, "pass "+c.flag) {
					t.Fatalf("command still asks for %s: %q", c.flag, s)
				}
			}
		})
	}
}

// evidence verify reports the tenant it walked on failure, which shows the
// implied tenant is the one acted on.
func TestEvidenceVerify_SingleTenant_WalksOnlyTenant(t *testing.T) {
	stubTenants(t, soloTenant)
	stderr, _ := runDBCommand(t, dbCommands()[0])
	if !strings.Contains(stderr, "tenant "+soloTenant.ID.String()+":") {
		t.Fatalf("stderr = %q, want the walk to run in tenant %s", stderr, soloTenant.ID)
	}
}

func TestDBCommands_MultiTenant_NoFlag_ListsTenants(t *testing.T) {
	for _, c := range dbCommands() {
		t.Run(c.name, func(t *testing.T) {
			stubTenants(t, acmeTenant, betaTenant)
			stderr, err := runDBCommand(t, c)
			msg := stderr + errText(err)
			if !strings.Contains(msg, "pass "+c.flag) {
				t.Fatalf("output = %q, want it to ask for %s", msg, c.flag)
			}
			for _, tn := range []tenancy.TenantRef{acmeTenant, betaTenant} {
				for _, want := range []string{tn.ID.String(), tn.Slug, tn.Name} {
					if !strings.Contains(msg, want) {
						t.Errorf("output = %q, want it to list %q", msg, want)
					}
				}
			}
			if strings.Contains(msg, "the install's only tenant") {
				t.Errorf("output = %q: picked a tenant although several exist", msg)
			}
		})
	}
}

// An explicit flag wins and the resolver never runs.
func TestDBCommands_FlagGiven_SkipsResolver(t *testing.T) {
	for _, c := range dbCommands() {
		t.Run(c.name, func(t *testing.T) {
			prev := newTenantResolver
			newTenantResolver = func(*pgxpool.Pool) tenantResolver { return resolverMustNotRun(t) }
			t.Cleanup(func() { newTenantResolver = prev })
			c.args = append(c.args, c.flag+"="+acmeTenant.ID.String())
			stderr, _ := runDBCommand(t, c)
			if strings.Contains(stderr, "the install's only tenant") {
				t.Fatalf("stderr = %q: resolved a tenant although %s was given", stderr, c.flag)
			}
		})
	}
}

func TestTenantFlagHelp_SaysOptionalOnSingleTenant(t *testing.T) {
	cmds := map[string]*cobra.Command{}
	for _, c := range dbCommands() {
		cmds[c.name] = c.cmd()
	}
	cmds["credentials issue"] = newCredentialsIssueCmd()
	cmds["credentials list"] = newCredentialsListCmd()
	flags := map[string]string{"credentials issue": "--tenant", "credentials list": "--tenant"}
	for _, c := range dbCommands() {
		flags[c.name] = c.flag
	}
	for name, cmd := range cmds {
		f := cmd.Flags().Lookup(strings.TrimPrefix(flags[name], "--"))
		if f == nil {
			t.Fatalf("%s: no %s flag", name, flags[name])
		}
		if !strings.Contains(f.Usage, "optional on a single-tenant install") {
			t.Errorf("%s %s usage = %q, want it to say the flag is optional on a single-tenant install", name, flags[name], f.Usage)
		}
	}
}

// credentials issue and list without --tenant act in the tenant of the
// --token credential, against the real gRPC admin service.
func TestCredentialsCommands_NoTenant_DefaultToCredentialTenant(t *testing.T) {
	srv := api.New(api.Config{})
	_, bearer, err := srv.IssueBootstrapCredential(soloTenant.ID.String())
	if err != nil {
		t.Fatalf("issue bootstrap credential: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(srv.GRPC.Stop)

	prevEndpoint, prevToken, prevInsecure := common.endpoint, common.token, common.insecure
	common.endpoint, common.token, common.insecure = lis.Addr().String(), bearer, true
	t.Cleanup(func() { common.endpoint, common.token, common.insecure = prevEndpoint, prevToken, prevInsecure })

	issued := captureStdout(t, func() error {
		cmd := newCredentialsIssueCmd()
		cmd.SetArgs(nil)
		return cmd.Execute()
	})
	if !strings.Contains(issued, `"tenantId": "`+soloTenant.ID.String()+`"`) {
		t.Fatalf("issue output = %s, want a key in tenant %s", issued, soloTenant.ID)
	}

	listed := captureStdout(t, func() error {
		cmd := newCredentialsListCmd()
		cmd.SetArgs(nil)
		return cmd.Execute()
	})
	if got := strings.Count(listed, `"tenantId": "`+soloTenant.ID.String()+`"`); got != 2 {
		t.Fatalf("list output = %s, want the 2 keys of tenant %s (bootstrap + issued)", listed, soloTenant.ID)
	}
}

// captureStdout runs fn with os.Stdout redirected (runAdminRPC prints
// there) and returns what it wrote.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = prev
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("command: %v", runErr)
	}
	return string(out)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

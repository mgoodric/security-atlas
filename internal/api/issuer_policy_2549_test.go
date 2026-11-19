package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/netguard"
)

// lazyPool is a pool that never connects: pgxpool dials on first use, and
// these tests stop before any request.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://atlas@127.0.0.1:1/atlas")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// #2549: an invalid ATLAS_OIDC_ALLOW_PRIVATE_ISSUERS stops every entry
// point that builds the server, not only cmd/atlas's own check.
func TestRunHTTP_RefusesInvalidPrivateIssuerOptIn(t *testing.T) {
	t.Setenv(oidc.AllowPrivateIssuersEnv, "yes")
	srv := New(Config{})
	srv.AttachDB(lazyPool(t))

	err := srv.RunHTTP(context.Background(), "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), oidc.AllowPrivateIssuersEnv) {
		t.Fatalf("RunHTTP err = %v; want the %s parse error", err, oidc.AllowPrivateIssuersEnv)
	}
}

func TestHTTPHandlerForTests_PanicsOnInvalidPrivateIssuerOptIn(t *testing.T) {
	t.Setenv(oidc.AllowPrivateIssuersEnv, "1")
	srv := New(Config{})
	srv.AttachDB(lazyPool(t))

	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok || !strings.Contains(err.Error(), oidc.AllowPrivateIssuersEnv) {
			t.Fatalf("recover() = %v; want the %s parse error", r, oidc.AllowPrivateIssuersEnv)
		}
	}()
	srv.HTTPHandlerForTests()
}

func TestNew_ParsesPrivateIssuerOptIn(t *testing.T) {
	cases := map[string]netguard.Policy{
		"":      {},
		"false": {},
		"true":  {AllowLoopback: true, AllowPrivate: true},
	}
	for val, want := range cases {
		t.Run(val, func(t *testing.T) {
			t.Setenv(oidc.AllowPrivateIssuersEnv, val)
			srv := New(Config{})
			if srv.configErr() != nil {
				t.Fatalf("configErr = %v; want nil", srv.configErr())
			}
			if srv.issuerPolicy != want {
				t.Fatalf("issuerPolicy = %+v; want %+v", srv.issuerPolicy, want)
			}
		})
	}
}

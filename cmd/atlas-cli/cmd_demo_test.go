package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// #2153: --dry-run prints the per-table counts and says nothing was
// deleted; a real run says the tenant was removed.
func TestPrintLegacyTeardown(t *testing.T) {
	res := demoseed.LegacyTeardownResult{
		TenantID: uuid.New(),
		DryRun:   true,
		Deleted:  map[string]int64{"tenants": 1, "controls": 47},
	}
	var dry bytes.Buffer
	printLegacyTeardown(&dry, "demo", res)
	out := dry.String()
	if !strings.Contains(out, "controls") || !strings.Contains(out, "47") || !strings.Contains(out, "NOT deleted") {
		t.Errorf("dry-run output = %q, want counts and a NOT deleted line", out)
	}
	if strings.Index(out, "controls") > strings.Index(out, "tenants") {
		t.Errorf("tables not sorted: %q", out)
	}

	res.DryRun = false
	var real bytes.Buffer
	printLegacyTeardown(&real, "demo", res)
	if !strings.Contains(real.String(), "removed") || strings.Contains(real.String(), "NOT deleted") {
		t.Errorf("real-run output = %q, want a removed line", real.String())
	}
}

// #2153: `demo teardown-legacy` refuses before touching the database when
// the opt-in env var is unset or no super admin is named.
func TestDemoTeardownLegacy_PreDatabaseGates(t *testing.T) {
	cases := []struct {
		name, env, email, want string
	}{
		{"env gate unset", "", "root@example.com", demoEnableEnvVar + "=true is required"},
		{"no super admin email", "true", "", "--super-admin-email is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(demoEnableEnvVar, tc.env)
			t.Setenv(demoDatabaseURLEnvVar, "")
			cmd := newDemoTeardownLegacyCmd()
			cmd.SetArgs([]string{"--super-admin-email=" + tc.email})
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

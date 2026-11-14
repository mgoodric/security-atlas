package main

import (
	"strings"
	"testing"
)

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

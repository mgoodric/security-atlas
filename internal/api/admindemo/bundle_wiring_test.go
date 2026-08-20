// OE-553 — the demo-seed enable posture, pinned.
//
// The decided posture is OPERATOR-ENABLEABLE-BUT-OFF-BY-DEFAULT: a
// self-host operator can turn the demo-seed surface on through the
// documented `.env` mechanism, and a bundle that nobody edits ships with
// it off. Three things have to agree for that posture to hold, and before
// OE-553 they did not:
//
//  1. the server gate admits ONLY the exact lowercase string "true"
//     (DefaultEnabledFunc);
//  2. `deploy/docker/docker-compose.yml` forwards that same variable to
//     the `atlas` service, with an empty default so an unset variable
//     leaves the feature off;
//  3. `deploy/docker/.env.example` — the operator's authoritative
//     copy-and-edit surface — names the variable, shipped COMMENTED OUT
//     (default off) and carrying the destructive-surface warning.
//
// The defect this file guards against was a half-wire: (1) and (3'd
// intent) existed, but (2) did not, so /admin/demo told the operator to
// "set ATLAS_ENABLE_DEMO_SEED=true in the docker-compose env" while
// compose silently dropped it and the seed endpoint returned 503 no
// matter what `.env` said.
//
// These are pure-Go file assertions — no Postgres, no Docker. They are
// deliberately anchored on the `demoEnableEnvVar` constant rather than a
// string literal, so renaming the gate without re-wiring the bundle
// fails here.

package admindemo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoFile reads a path relative to the repository root.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestDefaultEnabledFuncIsExactTrue pins the env-gate's strictness. The
// conservative read is load-bearing: an operator who typed "1" or "yes"
// into `.env` must NOT get a destructive seed/teardown surface they did
// not knowingly ask for.
func TestDefaultEnabledFuncIsExactTrue(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"", false},
		{"1", false},
		{"yes", false},
		{"TRUE", false},
		{"True", false},
		{"true ", false},
		{" true", false},
		{"false", false},
	} {
		t.Setenv(demoEnableEnvVar, tc.value)
		if got := DefaultEnabledFunc(); got != tc.want {
			t.Errorf("DefaultEnabledFunc() with %s=%q = %v, want %v",
				demoEnableEnvVar, tc.value, got, tc.want)
		}
	}
}

// TestComposeForwardsDemoSeedFlagToAtlas asserts leg 2 of the posture:
// the shipped self-host bundle passes the gate variable through to the
// `atlas` service, with an empty default so the shipped bundle stays off.
func TestComposeForwardsDemoSeedFlagToAtlas(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(repoFile(t, "deploy/docker/docker-compose.yml")), &compose); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}

	atlas, ok := compose.Services["atlas"]
	if !ok {
		t.Fatalf("docker-compose.yml has no `atlas` service; services present: %v", serviceNames(compose.Services))
	}

	got, ok := atlas.Environment[demoEnableEnvVar]
	if !ok {
		t.Fatalf("docker-compose.yml: the `atlas` service does not forward %s.\n"+
			"Without the passthrough, setting it in .env has no effect and\n"+
			"/v1/admin/demo/seed returns 503 on every self-host deployment —\n"+
			"the OE-553 half-wire. Add `%s: ${%s:-}` to the atlas environment block.",
			demoEnableEnvVar, demoEnableEnvVar, demoEnableEnvVar)
	}

	// Empty default, NOT `${...:-true}`. docker-compose.edge.yml defaults
	// it ON because that is the maintainer's own throwaway deployment; the
	// self-host bundle must not. This is the OE-553 boundary "do NOT
	// enable demo-seed by DEFAULT".
	want := "${" + demoEnableEnvVar + ":-}"
	if got != want {
		t.Errorf("docker-compose.yml atlas %s = %q, want %q.\n"+
			"A non-empty default would turn a DESTRUCTIVE seed/teardown surface\n"+
			"on for an operator who never opted in.", demoEnableEnvVar, got, want)
	}
}

// TestEnvExampleDocumentsDemoSeedFlagOff asserts leg 3: the template names
// the variable, ships it commented out (default off), and carries the
// destructive-surface warning an operator needs before uncommenting it.
func TestEnvExampleDocumentsDemoSeedFlagOff(t *testing.T) {
	tmpl := repoFile(t, "deploy/docker/.env.example")

	if !strings.Contains(tmpl, demoEnableEnvVar) {
		t.Fatalf(".env.example does not document %s. The template is the operator's\n"+
			"authoritative copy-and-edit surface; a compose passthrough nobody\n"+
			"knows about is still an unreachable feature.", demoEnableEnvVar)
	}

	// Shipped commented out: `# ATLAS_ENABLE_DEMO_SEED=true`, matching the
	// TRUSTED_PROXY_CIDRS / ATLAS_METRICS_FALLBACK_ENABLE opt-in pattern.
	// The slice-430 drift guard counts this form as a documented opt-in.
	commented := regexp.MustCompile(`(?m)^# *` + regexp.QuoteMeta(demoEnableEnvVar) + `=`)
	if !commented.MatchString(tmpl) {
		t.Errorf(".env.example must ship %s as a COMMENTED opt-in (`# %s=true`).",
			demoEnableEnvVar, demoEnableEnvVar)
	}

	// And never as an active key — an uncommented entry in the template
	// would enable the destructive surface on a fresh `cp .env.example .env`.
	active := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(demoEnableEnvVar) + `=`)
	if active.MatchString(tmpl) {
		t.Errorf(".env.example must NOT ship %s as an active key — a fresh\n"+
			"`cp .env.example .env` would then enable a destructive\n"+
			"seed/teardown surface the operator never opted into.", demoEnableEnvVar)
	}

	// The warning is part of the posture, not decoration: the operator
	// decides from the template, and "teardown drops the tenant" is the
	// fact that decision turns on.
	for _, want := range []string{"SECURITY WARNING", "DESTRUCTIVE", "docs/getting-started/demo-seed.md"} {
		if !strings.Contains(tmpl, want) {
			t.Errorf(".env.example %s block is missing %q", demoEnableEnvVar, want)
		}
	}
}

func serviceNames(m map[string]struct {
	Environment map[string]string `yaml:"environment"`
},
) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return names
}

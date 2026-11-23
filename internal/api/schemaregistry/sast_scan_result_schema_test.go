package schemaregistry

import (
	"context"
	"io/fs"
	"testing"
)

func readSASTScan(t *testing.T, v string) []byte {
	t.Helper()
	b, err := fs.ReadFile(PlatformSchemasFS(), "sast.scan_result/"+v+".json")
	if err != nil {
		t.Fatalf("read %s: %v", v, err)
	}
	return b
}

// #2831: sast.scan_result 1.1.0 adds repository and findings, and must stay
// additive over 1.0.0, so records already pushed at 1.0.0 stay valid under
// kind .v1.
func TestSASTScanResult110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readSASTScan(t, "1.0.0"), readSASTScan(t, "1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

func TestSASTScanResult110_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const base = `"tool":"semgrep","findings_count":2`
	const f1 = `{"rule_id":"injection.sql","severity":"critical","suppressed":false}`
	const f2 = `{"rule_id":"python.flask.debug","severity":"low","suppressed":true}`
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"repository_and_findings", "1.1.0", `{` + base + `,"repository":"acme/api","findings":[` + f1 + `,` + f2 + `]}`, true},
		{"empty_findings", "1.1.0", `{"tool":"semgrep","findings_count":0,"repository":"acme/api","findings":[]}`, true},
		{"none_of_the_new_fields", "1.1.0", `{` + base + `}`, true},
		{"v100_payload_under_v110", "1.1.0", `{` + base + `,"tool_version":"1.90.0","ruleset":"p/ci","scanned_files":40,"scan_duration_seconds":12.5,"report_uri":"https://ci.example/r/1"}`, true},
		{"empty_repository", "1.1.0", `{` + base + `,"repository":""}`, false},
		{"findings_not_array", "1.1.0", `{` + base + `,"findings":` + f1 + `}`, false},
		{"finding_without_suppressed", "1.1.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","severity":"critical"}]}`, false},
		{"finding_without_rule_id", "1.1.0", `{` + base + `,"findings":[{"severity":"critical","suppressed":false}]}`, false},
		{"finding_without_severity", "1.1.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","suppressed":false}]}`, false},
		{"unknown_severity", "1.1.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","severity":"Critical","suppressed":false}]}`, false},
		{"suppressed_not_boolean", "1.1.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","severity":"critical","suppressed":"yes"}]}`, false},
		{"empty_rule_id", "1.1.0", `{` + base + `,"findings":[{"rule_id":"","severity":"critical","suppressed":false}]}`, false},
		{"unknown_finding_field", "1.1.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","severity":"critical","suppressed":false,"path":"a.go"}]}`, false},
		{"v100_rejects_findings", "1.0.0", `{` + base + `,"findings":[` + f1 + `]}`, false},
		{"v100_rejects_repository", "1.0.0", `{` + base + `,"repository":"acme/api"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "sast.scan_result.v1", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}

// #2973: 1.2.0 adds an optional cwe list per finding and stays additive over
// 1.1.0, so 1.1.0 and 1.0.0 records stay valid under kind .v1.
func TestSASTScanResult120_AdditiveOver110(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readSASTScan(t, "1.1.0"), readSASTScan(t, "1.2.0")); err != nil {
		t.Fatalf("1.2.0 is not additive over 1.1.0: %v", err)
	}
}

func TestSASTScanResult120_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const base = `"tool":"semgrep","findings_count":1,"repository":"acme/api"`
	finding := func(cwe string) string {
		return `{` + base + `,"findings":[{"rule_id":"python.sqli","severity":"high","suppressed":false` + cwe + `}]}`
	}
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"cwe_list", "1.2.0", finding(`,"cwe":["CWE-89","CWE-20"]`), true},
		{"empty_cwe_list", "1.2.0", finding(`,"cwe":[]`), true},
		{"no_cwe", "1.2.0", finding(``), true},
		{"v110_payload_under_v120", "1.2.0", `{` + base + `,"findings":[{"rule_id":"injection.sql","severity":"critical","suppressed":false}]}`, true},
		{"cwe_not_array", "1.2.0", finding(`,"cwe":"CWE-89"`), false},
		{"cwe_lowercase", "1.2.0", finding(`,"cwe":["cwe-89"]`), false},
		{"cwe_bare_number", "1.2.0", finding(`,"cwe":["89"]`), false},
		{"cwe_with_title", "1.2.0", finding(`,"cwe":["CWE-89: SQL Injection"]`), false},
		{"cwe_leading_zero", "1.2.0", finding(`,"cwe":["CWE-089"]`), false},
		{"cwe_duplicate", "1.2.0", finding(`,"cwe":["CWE-89","CWE-89"]`), false},
		{"v110_rejects_cwe", "1.1.0", finding(`,"cwe":["CWE-89"]`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "sast.scan_result.v1", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}

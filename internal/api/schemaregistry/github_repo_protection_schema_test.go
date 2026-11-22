package schemaregistry

import (
	"context"
	"io/fs"
	"testing"
)

func readRepoProtection(t *testing.T, v string) []byte {
	t.Helper()
	b, err := fs.ReadFile(PlatformSchemasFS(), "github.repo_protection/"+v+".json")
	if err != nil {
		t.Fatalf("read %s: %v", v, err)
	}
	return b
}

// #2827: github.repo_protection 2.1.0 adds required_status_checks,
// allow_force_pushes, topics and deploys_to_production, and must stay
// additive over 2.0.0, so records already pushed at 2.0.0 stay valid under
// kind .v2.
func TestGitHubRepoProtection210_AdditiveOver200(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readRepoProtection(t, "2.0.0"), readRepoProtection(t, "2.1.0")); err != nil {
		t.Fatalf("2.1.0 is not additive over 2.0.0: %v", err)
	}
}

func TestGitHubRepoProtection210_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const id = `"repo_id":1296269,"repo_full_name":"octocat/Hello-World","default_branch":"master"`
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"all_new_fields", "2.1.0", `{` + id + `,"required_status_checks":["ci/build","ci/test"],"allow_force_pushes":false,"topics":["iac","production"],"deploys_to_production":true}`, true},
		{"none_of_the_new_fields", "2.1.0", `{` + id + `}`, true},
		{"no_status_checks", "2.1.0", `{` + id + `,"required_status_checks":[]}`, true},
		{"duplicate_status_check", "2.1.0", `{` + id + `,"required_status_checks":["ci","ci"]}`, false},
		{"empty_status_check", "2.1.0", `{` + id + `,"required_status_checks":[""]}`, false},
		{"deploys_not_boolean", "2.1.0", `{` + id + `,"deploys_to_production":"yes"}`, false},
		{"topics_not_array", "2.1.0", `{` + id + `,"topics":"iac"}`, false},
		{"v200_rejects_new_fields", "2.0.0", `{` + id + `,"topics":["iac"]}`, false},
		{"v200_payload_under_v200", "2.0.0", `{` + id + `,"required_reviews":1,"enforce_admins":true}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "github.repo_protection.v2", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}

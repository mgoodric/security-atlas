package schemaregistry

import (
	"context"
	"io/fs"
	"testing"
)

func readAppAssignment(t *testing.T, v string) []byte {
	t.Helper()
	b, err := fs.ReadFile(PlatformSchemasFS(), "okta.app_assignment/"+v+".json")
	if err != nil {
		t.Fatalf("read %s: %v", v, err)
	}
	return b
}

// #2828: okta.app_assignment 1.1.0 adds classification, sign_on_policy_id,
// sign_on_mfa_required and reason, and must stay additive over 1.0.0, so
// records already pushed at 1.0.0 stay valid under kind .v1.
func TestOktaAppAssignment110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readAppAssignment(t, "1.0.0"), readAppAssignment(t, "1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

func TestOktaAppAssignment110_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const id = `"app_id":"0oa1","app_name":"Okta Admin Console","status":"ACTIVE","assigned_group_ids":["00g1"]`
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"all_new_fields", "1.1.0", `{` + id + `,"classification":"admin","sign_on_policy_id":"rst1","sign_on_mfa_required":true}`, true},
		{"undecided_with_reason", "1.1.0", `{` + id + `,"classification":"infrastructure","sign_on_policy_id":"rst1","reason":"rule r1 verification method is not ASSURANCE"}`, true},
		{"none_of_the_new_fields", "1.1.0", `{` + id + `}`, true},
		{"unknown_classification", "1.1.0", `{` + id + `,"classification":"Admin"}`, false},
		{"mfa_not_boolean", "1.1.0", `{` + id + `,"sign_on_mfa_required":"true"}`, false},
		{"empty_policy_id", "1.1.0", `{` + id + `,"sign_on_policy_id":""}`, false},
		{"empty_reason", "1.1.0", `{` + id + `,"reason":""}`, false},
		{"v100_rejects_new_fields", "1.0.0", `{` + id + `,"classification":"admin"}`, false},
		{"v100_payload_under_v100", "1.0.0", `{` + id + `,"sign_on_mode":"SAML_2_0","assigned_group_count":1}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "okta.app_assignment.v1", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}

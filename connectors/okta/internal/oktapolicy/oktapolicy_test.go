package oktapolicy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
)

// everyoneID is the Everyone group's id in these fixtures. Real Okta group
// ids are 20-character `00g…` strings; the policies below reference groups
// only by id, never by name.
const everyoneID = "00g1emaKYZTWRYYRRTSK"

// identityEnginePolicies is shaped on the `list-mfa-enroll-policy-response`
// example in Okta's management OpenAPI spec
// (okta/okta-management-openapi-spec, dist/current/management-minimal.yaml):
// settings.type AUTHENTICATORS, an authenticators[] list keyed by `key`, the
// password among them, group ids in conditions.people.groups.include. The
// keys and enroll.self values are from the spec's
// AuthenticatorEnrollmentPolicyAuthenticatorType and ...Status enums.
const identityEnginePolicies = `[
	{
		"type": "MFA_ENROLL",
		"id": "00p1ie0webauthn0everyone",
		"status": "ACTIVE",
		"name": "Default Policy",
		"description": "The default authenticator enrollment policy",
		"priority": 2,
		"system": true,
		"created": "2026-01-05T17:12:41.000Z",
		"lastUpdated": "2026-10-02T09:01:13.000Z",
		"conditions": {"people": {"groups": {"include": ["` + everyoneID + `"]}}},
		"settings": {
			"type": "AUTHENTICATORS",
			"authenticators": [
				{"key": "okta_email", "enroll": {"self": "NOT_ALLOWED", "autoEnroll": false}},
				{"key": "okta_verify", "enroll": {"self": "OPTIONAL"}},
				{"key": "webauthn", "enroll": {"self": "REQUIRED"}},
				{"key": "okta_password", "enroll": {"self": "REQUIRED"}}
			]
		}
	},
	{
		"type": "MFA_ENROLL",
		"id": "00p1ie0password0only000",
		"status": "ACTIVE",
		"name": "Contractors",
		"priority": 1,
		"system": false,
		"conditions": {"people": {"groups": {"include": ["00g1contractors0000001"]}}},
		"settings": {
			"type": "AUTHENTICATORS",
			"authenticators": [
				{"key": "okta_password", "enroll": {"self": "REQUIRED"}},
				{"key": "phone_number", "enroll": {"self": "OPTIONAL"}}
			]
		}
	}
]`

// classicPolicies is shaped on the factor-based settings snippet in Okta's
// "Understand authenticator enrollment policy API changes after the upgrade"
// guide (a `settings.factors` map, each entry with enroll + consent, no
// settings.type). The fido_webauthn and fido_u2f keys are the Classic factor
// names Okta's Terraform provider models in sdk/policy.go
// (PolicyFactorsSettings).
const classicPolicies = `[
	{
		"type": "MFA_ENROLL",
		"id": "00p1classic0fido0000001",
		"status": "ACTIVE",
		"name": "Default Policy",
		"priority": 1,
		"system": true,
		"conditions": {"people": {"groups": {"include": ["` + everyoneID + `", "00g1engineering0000001"]}}},
		"settings": {
			"factors": {
				"okta_otp":      {"enroll": {"self": "REQUIRED"}, "consent": {"type": "NONE"}},
				"fido_webauthn": {"enroll": {"self": "OPTIONAL"}, "consent": {"type": "NONE"}},
				"fido_u2f":      {"enroll": {"self": "OPTIONAL"}, "consent": {"type": "NONE"}},
				"okta_sms":      {"enroll": {"self": "NOT_ALLOWED"}, "consent": {"type": "NONE"}}
			}
		}
	},
	{
		"type": "MFA_ENROLL",
		"id": "00p1classic0inactive0001",
		"status": "INACTIVE",
		"name": "Legacy SMS",
		"priority": 2,
		"system": false,
		"conditions": {"people": {"groups": {"include": ["` + everyoneID + `"]}}},
		"settings": {
			"factors": {
				"okta_sms": {"enroll": {"self": "REQUIRED"}, "consent": {"type": "NONE"}}
			}
		}
	}
]`

// builtInGroups is the `search=type eq "BUILT_IN"` response, shaped on the
// Group object in Okta's spec (id, type, profile.name).
const builtInGroups = `[
	{"id": "` + everyoneID + `", "type": "BUILT_IN", "profile": {"name": "Everyone", "description": "All users in your organization"}}
]`

// newServer serves the two endpoints Pull calls and checks the queries.
func newServer(t *testing.T, policies, groups string) *oktapolicy.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "MFA_ENROLL" {
			t.Errorf("missing type=MFA_ENROLL param; got %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "SSWS test-token" {
			t.Errorf("Authorization header = %q; want SSWS test-token", got)
		}
		_, _ = w.Write([]byte(policies))
	})
	mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != `type eq "BUILT_IN"` {
			t.Errorf("groups search = %q; want type eq \"BUILT_IN\"", got)
		}
		_, _ = w.Write([]byte(groups))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return oktapolicy.NewClient(srv.Client(), srv.URL, creds)
}

var fixed = func() time.Time { return time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC) }

func pull(t *testing.T, policies string) map[string]oktapolicy.PolicyState {
	t.Helper()
	states, err := oktapolicy.Pull(context.Background(), newServer(t, policies, builtInGroups), fixed)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	byID := map[string]oktapolicy.PolicyState{}
	for _, s := range states {
		byID[s.PolicyID] = s
	}
	return byID
}

func TestPull_IdentityEngineAuthenticators(t *testing.T) {
	got := pull(t, identityEnginePolicies)
	if len(got) != 2 {
		t.Fatalf("len(states) = %d; want 2", len(got))
	}

	def := got["00p1ie0webauthn0everyone"]
	want := oktapolicy.PolicyState{
		PolicyID:          "00p1ie0webauthn0everyone",
		PolicyName:        "Default Policy",
		Status:            "ACTIVE",
		Priority:          2,
		MFARequired:       true,
		FactorsAllowed:    []string{"okta_verify", "webauthn"},
		AppliesToGroups:   []string{everyoneID},
		AppliesToEveryone: true,
		Result:            oktapolicy.ResultPass,
		Reason:            "mfa_required=true factors=2",
		ObservedAt:        fixed(),
	}
	if !reflect.DeepEqual(def, want) {
		t.Errorf("default policy:\n got  %+v\n want %+v", def, want)
	}

	// Only the password is REQUIRED: that is one factor, not MFA.
	pw := got["00p1ie0password0only000"]
	if pw.MFARequired {
		t.Error("password-only policy: MFARequired = true; the password is not a second factor")
	}
	if !reflect.DeepEqual(pw.FactorsAllowed, []string{"phone_number"}) {
		t.Errorf("password-only policy: FactorsAllowed = %v; want [phone_number] (password excluded)", pw.FactorsAllowed)
	}
	if pw.AppliesToEveryone {
		t.Error("contractors policy: AppliesToEveryone = true; it lists only a contractors group id")
	}
	if pw.Result != oktapolicy.ResultFail {
		t.Errorf("password-only policy: Result = %q; want fail", pw.Result)
	}
}

func TestPull_ClassicFactors(t *testing.T) {
	got := pull(t, classicPolicies)
	if len(got) != 2 {
		t.Fatalf("len(states) = %d; want 2", len(got))
	}

	def := got["00p1classic0fido0000001"]
	// fido_webauthn and fido_u2f both become webauthn, once; okta_sms is
	// NOT_ALLOWED so it is not listed; the list is sorted.
	if !reflect.DeepEqual(def.FactorsAllowed, []string{"okta_otp", "webauthn"}) {
		t.Errorf("FactorsAllowed = %v; want [okta_otp webauthn]", def.FactorsAllowed)
	}
	if !def.MFARequired || def.Status != "ACTIVE" || def.Priority != 1 {
		t.Errorf("default classic policy = %+v; want MFARequired, ACTIVE, priority 1", def)
	}
	if !def.AppliesToEveryone {
		t.Errorf("AppliesToEveryone = false; include %v holds the Everyone id", def.AppliesToGroups)
	}
	if def.Result != oktapolicy.ResultPass {
		t.Errorf("Result = %q; want pass", def.Result)
	}

	// An INACTIVE policy is reported with its status, and its verdict is na:
	// Okta does not apply it.
	in := got["00p1classic0inactive0001"]
	if in.Status != "INACTIVE" {
		t.Errorf("inactive policy Status = %q; want INACTIVE", in.Status)
	}
	if in.Result != oktapolicy.ResultNA || in.Reason != "status=INACTIVE" {
		t.Errorf("inactive policy verdict = %q (%q); want na (status=INACTIVE)", in.Result, in.Reason)
	}
}

func TestPull_EveryoneMissingStopsThePull(t *testing.T) {
	// A Group Administrator token gets a groups list without Everyone.
	client := newServer(t, identityEnginePolicies, `[]`)
	_, err := oktapolicy.Pull(context.Background(), client, fixed)
	if !errors.Is(err, oktapolicy.ErrEveryoneNotFound) {
		t.Fatalf("err = %v; want ErrEveryoneNotFound", err)
	}
}

func TestPull_EveryoneMustBeBuiltIn(t *testing.T) {
	// An admin-created OKTA_GROUP named "Everyone" is not the org's group.
	impostor := `[{"id": "00g1impostor0000000001", "type": "OKTA_GROUP", "profile": {"name": "Everyone"}}]`
	client := newServer(t, identityEnginePolicies, impostor)
	_, err := oktapolicy.Pull(context.Background(), client, fixed)
	if !errors.Is(err, oktapolicy.ErrEveryoneNotFound) {
		t.Fatalf("err = %v; want ErrEveryoneNotFound", err)
	}
}

func TestPull_GroupsAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policies", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(identityEnginePolicies))
	})
	mux.HandleFunc("/api/v1/groups", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorCode":"E0000006"}`, http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	creds, _ := oktaauth.Resolve(oktaauth.ResolveOpts{Token: "test-token"})
	_, err := oktapolicy.Pull(context.Background(), oktapolicy.NewClient(srv.Client(), srv.URL, creds), fixed)
	var apiErr *oktapolicy.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v; want an APIError with status 403", err)
	}
}

func TestPull_SkipsPolicyMissingRequiredFields(t *testing.T) {
	got := pull(t, `[
		{"id": "", "name": "no id", "status": "ACTIVE"},
		{"id": "00p1nostatus", "name": "no status"},
		{"id": "00p1ok", "name": "ok", "status": "active"}
	]`)
	if len(got) != 1 {
		t.Fatalf("states = %v; want only 00p1ok", got)
	}
	if s := got["00p1ok"]; s.Status != "ACTIVE" || s.Result != oktapolicy.ResultFail {
		t.Errorf("00p1ok = %+v; want status upper-cased to ACTIVE, fail (no factors)", s)
	}
}

func TestPull_NilAPI(t *testing.T) {
	if _, err := oktapolicy.Pull(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on nil API")
	}
}

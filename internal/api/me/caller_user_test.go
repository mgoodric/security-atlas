// caller_user_test.go — #2055: the users.id behind each credential type.
//
// Pure-Go (slice-353 Q-2 fast loop). A local-login / PKCE / device-grant JWT
// bridges Subject "user:<uuid>" into cred.UserID; before #2055 the /v1/me
// handlers parsed that raw, failed, and served the "API key" credential
// profile to a signed-in person.

package me

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestCallerUserUUID_PerCredentialType(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	credID := uuid.NewString()
	cases := []struct {
		name   string
		userID string
		want   uuid.UUID
		wantOK bool
	}{
		{"user JWT (local login, PKCE, device grant)", "user:" + userID.String(), userID, true},
		// An API key's UserID is its own credential id. It may parse, but no
		// users row carries it, so buildProfile falls through to the
		// credential profile (TestGetMe_APIKeyKeepsCredentialProfile).
		{"API key (own credential id)", credID, uuid.MustParse(credID), true},
		{"bootstrap admin (opaque credential id)", "bootstrap-admin", uuid.Nil, false},
		{"OAuth client (machine token)", "oauth_client:ci-runner", uuid.Nil, false},
		{"empty", "", uuid.Nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := callerUserUUID(credstore.Credential{UserID: tc.userID})
			if (err == nil) != tc.wantOK {
				t.Fatalf("callerUserUUID(%q) err = %v; wantOK %v", tc.userID, err, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("callerUserUUID(%q) = %s; want %s", tc.userID, got, tc.want)
			}
		})
	}
}

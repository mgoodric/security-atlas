package identity

import (
	"testing"

	"github.com/google/uuid"
)

func TestUserID(t *testing.T) {
	t.Parallel()
	u := uuid.New()
	for in, want := range map[string]bool{
		"user:" + u.String():            true,
		u.String():                      true,
		"key_abc":                       false,
		"bootstrap-admin":               false,
		"oauth_client:ci-runner":        false,
		"oauth_client:" + u.String():    false,
		"demo-operator@example.invalid": false,
		"user:" + uuid.Nil.String():     false,
		"user:":                         false,
		"":                              false,
	} {
		got, ok := UserID(in)
		if ok != want || (ok && got != u) {
			t.Errorf("UserID(%q) = (%s, %v); want ok=%v", in, got, ok, want)
		}
	}
}

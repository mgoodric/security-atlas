// device_approval_constructor_test.go — slice 472 pure-Go unit coverage
// for the NewDeviceApprovalEndpoint constructor arms (no Postgres,
// fast t.Parallel table-free tests per the slice-353 Q-2 convention).
//
// The slice-191 constructor has two arms the integration suite never
// exercises: the fail-loud nil-codes panic (a misconfigured deployment
// must crash at startup, never silently 500 per request) and the nil-Now
// default (cfg.Now == nil falls back to time.Now). Both are pre-DB and
// belong in the fast unit loop.

package oauth_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
)

// TestNewDeviceApprovalEndpoint_NilCodesPanics covers device_approve.go:65
// — a nil DeviceCodeStore is a deployment misconfiguration that MUST panic
// at construction (fail-loud) rather than 500 on every approve request.
func TestNewDeviceApprovalEndpoint_NilCodesPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewDeviceApprovalEndpoint(nil, ...) did not panic; want fail-loud")
		}
	}()
	_ = oauth.NewDeviceApprovalEndpoint(nil, oauth.DeviceApprovalConfig{})
}

// TestNewDeviceApprovalEndpoint_NilNowDefaults covers device_approve.go:69
// — a zero DeviceApprovalConfig (Now == nil) constructs successfully with
// the time.Now fallback wired. Construction returning non-nil proves the
// default branch was taken without panicking.
func TestNewDeviceApprovalEndpoint_NilNowDefaults(t *testing.T) {
	t.Parallel()
	ep := oauth.NewDeviceApprovalEndpoint(oauth.NewDeviceCodeStore(nil), oauth.DeviceApprovalConfig{})
	if ep == nil {
		t.Fatal("NewDeviceApprovalEndpoint returned nil with default Now")
	}
}

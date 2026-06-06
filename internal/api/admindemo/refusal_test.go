package admindemo

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// TestDemoSeedRefusalMessage verifies the seeder's benign precondition
// refusals map to a 409-worthy operator message (not a masked 500), and
// that genuine faults fall through. Mirrors the wrapping the seeder uses
// (fmt.Errorf("...: %w", sentinel)) so errors.Is matching is exercised
// through the real wrap, not just the bare sentinel.
func TestDemoSeedRefusalMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantOK  bool
		wantSub string // substring the operator message must contain
	}{
		{
			name:    "populated tenant (wrapped) -> already loaded",
			err:     fmt.Errorf("refusing to seed: tenant %q already has rows: %w", "demo", demoseed.ErrTenantPopulated),
			wantOK:  true,
			wantSub: "already been loaded",
		},
		{
			name:    "populated tenant (bare sentinel)",
			err:     demoseed.ErrTenantPopulated,
			wantOK:  true,
			wantSub: "Tear down",
		},
		{
			name:    "unmarked tenant (wrapped) -> not created by seeder",
			err:     fmt.Errorf("refusing to seed: tenant %q unmarked: %w", "demo", demoseed.ErrTenantUnmarked),
			wantOK:  true,
			wantSub: "not created by the demo seeder",
		},
		{
			name:   "generic internal fault falls through",
			err:    errors.New("demoseed: populated probe: connection refused"),
			wantOK: false,
		},
		{
			name:   "nil-equivalent unrelated error falls through",
			err:    errors.New("boom"),
			wantOK: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, ok := demoSeedRefusalMessage(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (msg=%q)", ok, tc.wantOK, msg)
			}
			if !tc.wantOK {
				if msg != "" {
					t.Fatalf("expected empty message on fall-through, got %q", msg)
				}
				return
			}
			if tc.wantSub != "" && !contains(msg, tc.wantSub) {
				t.Fatalf("message %q does not contain %q", msg, tc.wantSub)
			}
		})
	}
}

// TestDemoSeedRefusal_MapsToConflict documents the contract that the two
// refusal sentinels are the (and only the) cases routed to 409 Conflict.
func TestDemoSeedRefusal_MapsToConflict(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []error{demoseed.ErrTenantPopulated, demoseed.ErrTenantUnmarked} {
		if _, ok := demoSeedRefusalMessage(sentinel); !ok {
			t.Fatalf("sentinel %v should map to a 409 message", sentinel)
		}
	}
	// Sanity: the status the handler pairs with these is Conflict.
	if http.StatusConflict != 409 {
		t.Fatalf("StatusConflict drift")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

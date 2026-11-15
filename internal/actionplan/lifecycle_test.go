package actionplan

import "testing"

// TestLifecycle_MatchesAllowedTransition: the declared table and the state
// machine the store runs agree on every from -> to pair (#2146).
func TestLifecycle_MatchesAllowedTransition(t *testing.T) {
	t.Parallel()
	if err := Lifecycle.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, from := range Lifecycle.States {
		for _, to := range Lifecycle.States {
			if from == to {
				continue
			}
			if got, want := Lifecycle.Allows(from, to), AllowedTransition(from, to); got != want {
				t.Errorf("%s -> %s: table allows=%v, AllowedTransition=%v", from, to, got, want)
			}
		}
	}
}

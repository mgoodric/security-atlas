package unbound

import (
	"slices"
	"testing"
)

func TestKindsIsTheClosedList(t *testing.T) {
	t.Parallel()
	if got, want := Kinds(), []string{"policy.acknowledgment.v1"}; !slices.Equal(got, want) {
		t.Fatalf("Kinds() = %v, want %v", got, want)
	}
}

func TestKindsReturnsACopy(t *testing.T) {
	t.Parallel()
	k := Kinds()
	k[0] = "widened"
	if Kinds()[0] != "policy.acknowledgment.v1" {
		t.Fatal("mutating the result of Kinds() changed the list")
	}
}

package demoseed

import (
	"strings"
	"testing"
	"unicode"
)

// TestGenerateDemoPassword_Length verifies the password is exactly
// DemoPasswordLength characters long. AC-12 floor (>=16) is also
// checked statically.
func TestGenerateDemoPassword_Length(t *testing.T) {
	if DemoPasswordLength < 16 {
		t.Fatalf("DemoPasswordLength must be >= 16; got %d", DemoPasswordLength)
	}
	for i := 0; i < 100; i++ {
		got, err := GenerateDemoPassword()
		if err != nil {
			t.Fatalf("GenerateDemoPassword: %v", err)
		}
		if len(got) != DemoPasswordLength {
			t.Errorf("len(got) = %d; want %d", len(got), DemoPasswordLength)
		}
	}
}

// TestGenerateDemoPassword_AllClasses verifies the password contains
// at least one of each character class (lower / upper / digit / symbol).
// AC-12: mixed alphabet requirement.
func TestGenerateDemoPassword_AllClasses(t *testing.T) {
	for i := 0; i < 100; i++ {
		got, err := GenerateDemoPassword()
		if err != nil {
			t.Fatalf("GenerateDemoPassword: %v", err)
		}
		var hasLower, hasUpper, hasDigit, hasSymbol bool
		for _, r := range got {
			switch {
			case unicode.IsLower(r):
				hasLower = true
			case unicode.IsUpper(r):
				hasUpper = true
			case unicode.IsDigit(r):
				hasDigit = true
			case strings.ContainsRune(passwordSymbols, r):
				hasSymbol = true
			}
		}
		if !hasLower || !hasUpper || !hasDigit || !hasSymbol {
			t.Errorf("password %q missing class (lower=%v upper=%v digit=%v symbol=%v)",
				got, hasLower, hasUpper, hasDigit, hasSymbol)
		}
	}
}

// TestGenerateDemoPassword_NoAmbiguousChars verifies the password
// contains no ambiguous characters (0/O/I/l/1) per AC-12 (D4).
func TestGenerateDemoPassword_NoAmbiguousChars(t *testing.T) {
	const ambiguous = "0OIl1"
	for i := 0; i < 100; i++ {
		got, err := GenerateDemoPassword()
		if err != nil {
			t.Fatalf("GenerateDemoPassword: %v", err)
		}
		if strings.ContainsAny(got, ambiguous) {
			t.Errorf("password %q contains ambiguous char from %q", got, ambiguous)
		}
	}
}

// TestGenerateDemoPassword_Distinct verifies that two consecutive
// invocations almost always produce different passwords. With 20
// chars sampled from ~70-char alphabet, the collision probability is
// astronomically low — a collision indicates a broken RNG seed.
func TestGenerateDemoPassword_Distinct(t *testing.T) {
	a, err := GenerateDemoPassword()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := GenerateDemoPassword()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a == b {
		t.Errorf("two consecutive passwords are identical: %q (RNG broken?)", a)
	}
}

// TestNamesNotEmpty verifies the curated lists ship non-empty. AC-2
// gate: a future patch that empties these lists must fail the test.
func TestNamesNotEmpty(t *testing.T) {
	if len(fictionalPeople) < 20 {
		t.Errorf("fictionalPeople has %d entries; want >= 20 for AC-6 breadth", len(fictionalPeople))
	}
	if len(fictionalVendors) < 10 {
		t.Errorf("fictionalVendors has %d entries; want >= 10", len(fictionalVendors))
	}
	if len(fictionalAssets) < 10 {
		t.Errorf("fictionalAssets has %d entries; want >= 10", len(fictionalAssets))
	}
}

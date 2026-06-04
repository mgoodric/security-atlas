package schemaregistry

import "testing"

// FuzzParseSemver drives the strict X.Y.Z semver parser with arbitrary
// strings. The schema registry parses operator-supplied version strings
// (untrusted ingest input); a panic here is a DoS on the registry's
// register/list path (slice 421, headline threat = DoS).
//
// Contract asserted: ParseSemver returns either a clean error OR a valid
// Semver — it never panics. When it succeeds, the parse must round-trip
// (the canonical String() re-parses to the identical struct), proving the
// parser does not silently mis-parse bytes into a divergent record
// (slice 421 threat model: T — parser confusion).
//
// No recover() — a panic surfaces as a fuzz failure (P0-421-1).
func FuzzParseSemver(f *testing.F) {
	// Seed corpus: the valid + boundary strings the unit test pins, plus a
	// few adversarial shapes (leading zeros, signs, overflow-shaped, control
	// bytes, multibyte). Golden-derived / synthetic only (P0-421-4).
	seeds := []string{
		"1.0.0",
		"0.0.0",
		"10.20.30",
		"1.0",
		"1.0.0.0",
		"1.0.0-rc1",
		"01.0.0",
		"-1.0.0",
		"a.b.c",
		"",
		"999999999999999999999.0.0", // strconv overflow boundary
		"1.0.0\x00",                 // embedded null
		"ÿ.0.0",                     // multibyte
		"+1.0.0",
		"...",
		"1..0",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		got, err := ParseSemver(s)
		if err != nil {
			return // clean error is an acceptable outcome
		}
		// On success the parse must round-trip through its canonical form.
		// A divergence would mean ParseSemver accepted bytes it cannot
		// faithfully reproduce — a silent mis-parse (threat: T).
		reparsed, rerr := ParseSemver(got.String())
		if rerr != nil {
			t.Fatalf("ParseSemver(%q)=%v parsed OK but its canonical form %q failed to re-parse: %v",
				s, got, got.String(), rerr)
		}
		if reparsed != got {
			t.Fatalf("ParseSemver round-trip diverged: ParseSemver(%q)=%v, ParseSemver(%q)=%v",
				s, got, got.String(), reparsed)
		}
	})
}

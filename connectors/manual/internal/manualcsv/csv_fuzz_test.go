package manualcsv

import (
	"bytes"
	"testing"
)

// FuzzParse drives the manual-CSV importer with arbitrary byte streams.
// This parser ingests operator-uploaded CSV files (untrusted input); a
// malformed-input panic is a DoS on the manual-connector ingest path
// (slice 421, headline threat = DoS). The parser already enforces row +
// field-byte caps; the fuzz target proves no input shape gets past them
// into a panic.
//
// Contract asserted: Parse returns either a clean error OR a deterministic
// slice of Rows — it never panics. When it succeeds, re-parsing the same
// bytes yields the same row count (determinism / no parser confusion —
// threat T). No recover() (P0-421-1).
func FuzzParse(f *testing.F) {
	// Seed corpus: the valid + boundary shapes the unit tests pin, plus
	// adversarial shapes (embedded nulls, unbalanced quotes, lone CR,
	// ragged rows). Synthetic only (P0-421-4).
	seeds := [][]byte{
		[]byte("id,name,severity\n1,alpha,high\n2,beta,low\n3,gamma,medium\n"),
		[]byte(""),
		[]byte("a\n"),
		[]byte("a,b,c"),
		[]byte("\"unterminated quote\n"),
		[]byte("a,b\n1,\"two\nlines\",3\n"),
		[]byte("col\n\x00\x01\x02\n"),
		[]byte("a\r1\r2\r"),
		[]byte("ragged\n1,2,3,4,5\n1\n"),
		[]byte("\xff\xfe\x00bom-ish\n"),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// Fixed, generous-but-bounded limits: the fuzz engine explores input
	// *shape*, not the cap values. Caps stay > 0 so we exercise the parse
	// loop rather than the config-error early-return.
	lim := Limits{MaxRows: 10_000, MaxFieldBytes: 1 << 20}

	f.Fuzz(func(t *testing.T, data []byte) {
		rows, err := Parse(bytes.NewReader(data), lim)
		if err != nil {
			return // clean error is an acceptable outcome
		}
		// Determinism: the same bytes must parse to the same row count.
		// A divergence would indicate non-deterministic / aliasing behavior
		// (the parser copies fields specifically to avoid this).
		again, againErr := Parse(bytes.NewReader(data), lim)
		if againErr != nil {
			t.Fatalf("Parse succeeded then failed on identical bytes: %v", againErr)
		}
		if len(again) != len(rows) {
			t.Fatalf("Parse non-deterministic row count: first=%d second=%d", len(rows), len(again))
		}
	})
}

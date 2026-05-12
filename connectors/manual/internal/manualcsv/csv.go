// Package manualcsv parses CSV inputs into evidence-record-shaped rows.
// The parser enforces explicit row + field-byte caps so an attacker-supplied
// CSV cannot exhaust memory before the connector emits its first record
// (anti-criterion P0: CSV parser without caps → reject).
package manualcsv

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
)

// Errors returned by Parse. Wrapped at the caller with file context.
var (
	ErrTooManyRows   = errors.New("manualcsv: row count exceeds MaxRows")
	ErrFieldTooLarge = errors.New("manualcsv: field bytes exceed MaxFieldBytes")
)

// Limits caps the parser to prevent DoS. Zero values are rejected — callers
// must set both. Recommended defaults: MaxRows=100_000, MaxFieldBytes=1<<20.
type Limits struct {
	MaxRows       int
	MaxFieldBytes int
}

// Row is one parsed data row. Index is the 0-based row index excluding the
// header. Header is the parsed header row (nil if the CSV has only one
// row). Fields holds defensive copies of each field's bytes.
type Row struct {
	Index  int
	Header []string
	Fields []string
}

// Parse reads r as RFC 4180 CSV, returning the parsed data rows. The first
// non-empty record is treated as the header and is NOT emitted as a Row.
//
// Caller contract:
//   - Limits.MaxRows and Limits.MaxFieldBytes must both be > 0; zero values
//     return a configuration error before reading any bytes.
//   - If the data-row count would exceed MaxRows, returns ErrTooManyRows.
//   - If any field exceeds MaxFieldBytes, returns ErrFieldTooLarge.
func Parse(r io.Reader, lim Limits) ([]Row, error) {
	if lim.MaxRows <= 0 || lim.MaxFieldBytes <= 0 {
		return nil, fmt.Errorf("manualcsv: Limits.MaxRows and MaxFieldBytes must both be > 0")
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; the platform schema, not the parser, validates shape
	cr.ReuseRecord = false  // belt-and-suspenders against aliasing; we also copy

	var (
		header []string
		out    []Row
		idx    int
	)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("manualcsv: read: %w", err)
		}
		for _, f := range rec {
			if len(f) > lim.MaxFieldBytes {
				return nil, fmt.Errorf("%w (got %d bytes, cap %d)", ErrFieldTooLarge, len(f), lim.MaxFieldBytes)
			}
		}
		if header == nil {
			header = copyFields(rec)
			continue
		}
		if idx >= lim.MaxRows {
			return nil, fmt.Errorf("%w (cap %d)", ErrTooManyRows, lim.MaxRows)
		}
		out = append(out, Row{
			Index:  idx,
			Header: header,
			Fields: copyFields(rec),
		})
		idx++
	}
	return out, nil
}

// copyFields returns a fresh slice of fresh strings so callers cannot be
// surprised by buffer reuse. Cheap insurance against future encoding/csv
// internal changes.
func copyFields(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

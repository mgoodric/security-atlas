// Package xlsxbound opens and reads xlsx uploads under explicit resource
// bounds. An xlsx is a zip of XML parts: the compressed upload size says
// little about what parsing it costs, so an inbound workbook goes through
// the three bounds here before its rows reach a caller.
//
//   - Decompressed size: the declared uncompressed size of every zip entry
//     is summed and checked before excelize inflates anything (zip bomb).
//   - Rows: the sheet is streamed and the read stops past MaxRows, so an
//     oversized sheet is never materialized.
//   - Columns: excelize pads each row out to its highest cell index, so one
//     cell at XFD costs 16,384 strings. A row wider than MaxColumns (after
//     trailing blanks are trimmed) rejects the workbook.
//
// internal/questionnaire is the first adopter (issue 1969);
// internal/controlimport is expected to move onto it.
package xlsxbound

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Limits are the bounds one caller applies. Every field must be positive.
type Limits struct {
	// MaxDecompressedBytes caps the summed uncompressed size of every
	// part in the zip. excelize's own default is 16 GB.
	MaxDecompressedBytes uint64
	// MaxXMLPartBytes is the per-part size excelize unzips into memory;
	// a larger part spills to a temp file, which MaxDecompressedBytes
	// bounds.
	MaxXMLPartBytes int64
	// MaxRows is the per-sheet row ceiling.
	MaxRows int
	// MaxColumns is the per-row width ceiling, measured to the last
	// non-blank cell.
	MaxColumns int
}

// ErrDecompressedTooLarge is returned when the xlsx parts would
// decompress past Limits.MaxDecompressedBytes.
var ErrDecompressedTooLarge = errors.New("xlsx decompresses past size cap")

// ErrTooManyRows is returned when a sheet has more than Limits.MaxRows rows.
var ErrTooManyRows = errors.New("xlsx exceeds row cap")

// ErrTooManyColumns is returned when a row is wider than Limits.MaxColumns.
var ErrTooManyColumns = errors.New("xlsx exceeds column cap")

// Open checks the declared decompressed size, then opens the workbook
// with excelize's unzip limits set to the same bounds. The caller closes
// the returned file.
func Open(raw []byte, lim Limits) (*excelize.File, error) {
	if err := CheckDecompressedSize(raw, lim.MaxDecompressedBytes); err != nil {
		return nil, err
	}
	return excelize.OpenReader(bytes.NewReader(raw), excelize.Options{
		UnzipSizeLimit:    int64(lim.MaxDecompressedBytes),
		UnzipXMLSizeLimit: lim.MaxXMLPartBytes,
	})
}

// CheckDecompressedSize sums the declared uncompressed size of every zip
// entry and rejects the workbook past max. The comparison is written as
// size > max-total so a zip64 entry declaring close to 2^64 cannot wrap
// the sum back under the cap. archive/zip fails a read that runs past an
// entry's declared size, so an entry that under-declares cannot push more
// bytes through excelize later. Non-zip input falls through so excelize
// reports it as an open error.
func CheckDecompressedSize(raw []byte, max uint64) error {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil
	}
	var total uint64
	for _, zf := range zr.File {
		if zf.UncompressedSize64 > max-total {
			return ErrDecompressedTooLarge
		}
		total += zf.UncompressedSize64
	}
	return nil
}

// ReadRows streams the sheet, trimming trailing blank cells from each row
// and trailing blank rows from the sheet (matching excelize's GetRows).
// It stops at the first row past MaxRows or wider than MaxColumns, so
// neither shape is held in memory whole.
func ReadRows(f *excelize.File, sheet string, lim Limits) ([][]string, error) {
	it, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet rows: %w", err)
	}
	defer func() { _ = it.Close() }()

	var rows [][]string
	lastNonBlank := 0
	for it.Next() {
		if len(rows) >= lim.MaxRows {
			return nil, ErrTooManyRows
		}
		row, err := it.Columns()
		if err != nil {
			return nil, fmt.Errorf("read sheet rows: %w", err)
		}
		row = trimTrailingBlank(row)
		if len(row) > lim.MaxColumns {
			return nil, ErrTooManyColumns
		}
		rows = append(rows, row)
		if len(row) > 0 {
			lastNonBlank = len(rows)
		}
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("read sheet rows: %w", err)
	}
	return rows[:lastNonBlank], nil
}

// trimTrailingBlank drops trailing whitespace-only cells. A trimmed row is
// copied so the padded backing array can be collected.
func trimTrailingBlank(row []string) []string {
	n := len(row)
	for n > 0 && strings.TrimSpace(row[n-1]) == "" {
		n--
	}
	if n == len(row) {
		return row
	}
	return append([]string(nil), row[:n]...)
}

package xlsxbound

import (
	"archive/zip"
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/xuri/excelize/v2"
)

// zipOf builds a zip whose entries declare the given uncompressed sizes
// without carrying that many bytes (stored, empty bodies).
func zipOf(t *testing.T, sizes ...uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, n := range sizes {
		if _, err := zw.CreateRaw(&zip.FileHeader{
			Name:               string(rune('a'+i)) + ".bin",
			Method:             zip.Store,
			UncompressedSize64: n,
		}); err != nil {
			t.Fatalf("create entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestCheckDecompressedSize(t *testing.T) {
	t.Parallel()
	const max = 1000
	cases := []struct {
		name  string
		sizes []uint64
		want  error
	}{
		{"empty", nil, nil},
		{"at cap", []uint64{600, 400}, nil},
		{"one past cap", []uint64{600, 401}, ErrDecompressedTooLarge},
		{"single entry past cap", []uint64{1001}, ErrDecompressedTooLarge},
		// 10 + (2^64 - 10) wraps a naive running sum to 0.
		{"zip64 wrap to zero", []uint64{10, math.MaxUint64 - 9}, ErrDecompressedTooLarge},
		{"zip64 wrap under cap", []uint64{500, math.MaxUint64 - 100}, ErrDecompressedTooLarge},
		{"zip64 max", []uint64{math.MaxUint64}, ErrDecompressedTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckDecompressedSize(zipOf(t, tc.sizes...), max)
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckDecompressedSize_NonZipFallsThrough(t *testing.T) {
	t.Parallel()
	if err := CheckDecompressedSize([]byte("not a zip"), 1000); err != nil {
		t.Fatalf("non-zip input should defer to excelize, got %v", err)
	}
}

func TestTrimTrailingBlank(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want int
	}{
		{nil, 0},
		{[]string{"", " "}, 0},
		{[]string{"a", "", "\t"}, 1},
		{[]string{"", "b"}, 2},
	}
	for _, tc := range cases {
		if got := trimTrailingBlank(tc.in); len(got) != tc.want {
			t.Fatalf("trimTrailingBlank(%q) = %q, want len %d", tc.in, got, tc.want)
		}
	}
}

var testLimits = Limits{MaxDecompressedBytes: 64 << 20, MaxXMLPartBytes: 16 << 20, MaxRows: 3, MaxColumns: 4}

func workbook(t *testing.T, cells map[string]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)
	for cell, v := range cells {
		if err := f.SetCellStr(sheet, cell, v); err != nil {
			t.Fatalf("set %s: %v", cell, err)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func readFirstSheet(t *testing.T, raw []byte, lim Limits) ([][]string, error) {
	t.Helper()
	f, err := Open(raw, lim)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = f.Close() }()
	return ReadRows(f, f.GetSheetList()[0], lim)
}

func TestReadRows(t *testing.T) {
	t.Parallel()
	t.Run("within bounds trims trailing blanks", func(t *testing.T) {
		t.Parallel()
		rows, err := readFirstSheet(t, workbook(t, map[string]string{
			"A1": "a", "D1": "d", "A2": "b", "C2": " ", "A3": " ",
		}), testLimits)
		if err != nil {
			t.Fatalf("ReadRows: %v", err)
		}
		if len(rows) != 2 || len(rows[0]) != 4 || len(rows[1]) != 1 {
			t.Fatalf("unexpected shape %q", rows)
		}
	})
	t.Run("blank padding past the cap is not counted", func(t *testing.T) {
		t.Parallel()
		if _, err := readFirstSheet(t, workbook(t, map[string]string{"A1": "a", "XFD1": " "}), testLimits); err != nil {
			t.Fatalf("ReadRows: %v", err)
		}
	})
	t.Run("too wide", func(t *testing.T) {
		t.Parallel()
		_, err := readFirstSheet(t, workbook(t, map[string]string{"A1": "a", "E1": "e"}), testLimits)
		if !errors.Is(err, ErrTooManyColumns) {
			t.Fatalf("expected ErrTooManyColumns, got %v", err)
		}
	})
	t.Run("too many rows", func(t *testing.T) {
		t.Parallel()
		_, err := readFirstSheet(t, workbook(t, map[string]string{"A1": "a", "A4": "d"}), testLimits)
		if !errors.Is(err, ErrTooManyRows) {
			t.Fatalf("expected ErrTooManyRows, got %v", err)
		}
	})
	t.Run("missing sheet", func(t *testing.T) {
		t.Parallel()
		f, err := Open(workbook(t, map[string]string{"A1": "a"}), testLimits)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := ReadRows(f, "nope", testLimits); err == nil {
			t.Fatal("expected an error for a missing sheet")
		}
	})
}

func TestOpen_RejectsBeforeExcelize(t *testing.T) {
	t.Parallel()
	if _, err := Open(zipOf(t, 10, math.MaxUint64-9), testLimits); !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("expected ErrDecompressedTooLarge, got %v", err)
	}
}

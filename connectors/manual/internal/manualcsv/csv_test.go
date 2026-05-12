package manualcsv

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_HappyPath_EmitsOneRowPerRecord(t *testing.T) {
	t.Parallel()
	input := strings.NewReader("id,name,severity\n1,alpha,high\n2,beta,low\n3,gamma,medium\n")
	rows, err := Parse(input, Limits{MaxRows: 10, MaxFieldBytes: 1024})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 data rows, got %d", len(rows))
	}
	if rows[0].Index != 0 || rows[0].Fields[0] != "1" {
		t.Fatalf("row[0] unexpected: %+v", rows[0])
	}
	if len(rows[0].Header) != 3 || rows[0].Header[0] != "id" {
		t.Fatalf("header not propagated: %+v", rows[0].Header)
	}
}

func TestParse_RejectsTooManyRows(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	sb.WriteString("a\n")
	for i := 0; i < 6; i++ {
		sb.WriteString("x\n")
	}
	_, err := Parse(strings.NewReader(sb.String()), Limits{MaxRows: 5, MaxFieldBytes: 1024})
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("expected ErrTooManyRows, got %v", err)
	}
}

func TestParse_RejectsFieldTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 2000)
	input := "a\n" + big + "\n"
	_, err := Parse(strings.NewReader(input), Limits{MaxRows: 10, MaxFieldBytes: 1024})
	if !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("expected ErrFieldTooLarge, got %v", err)
	}
}

func TestParse_RejectsZeroLimitsAsConfigError(t *testing.T) {
	t.Parallel()
	_, err := Parse(strings.NewReader("a\nb\n"), Limits{})
	if err == nil {
		t.Fatal("expected error on zero limits")
	}
}

func TestParse_EmptyInputReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	rows, err := Parse(strings.NewReader(""), Limits{MaxRows: 10, MaxFieldBytes: 1024})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows on empty input, got %d", len(rows))
	}
}

func TestParse_HeaderOnly_ReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	rows, err := Parse(strings.NewReader("a,b,c\n"), Limits{MaxRows: 10, MaxFieldBytes: 1024})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 data rows for header-only input, got %d", len(rows))
	}
}

func TestParse_RowFieldsAreCopied(t *testing.T) {
	t.Parallel()
	// Defensive copy: encoding/csv may reuse its backing buffer.
	input := strings.NewReader("a,b\n1,2\n3,4\n")
	rows, err := Parse(input, Limits{MaxRows: 10, MaxFieldBytes: 1024})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rows[0].Fields[0] != "1" || rows[1].Fields[0] != "3" {
		t.Fatalf("row aliasing detected: rows[0]=%+v rows[1]=%+v", rows[0], rows[1])
	}
}

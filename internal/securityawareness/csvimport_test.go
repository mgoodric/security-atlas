package securityawareness

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseCompletionCSV_HappyPath(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		"Work_Email,source_person_id,person_source,COURSE_CODE,campaign_name,completed_at,phishing_simulation_id,phishing_sent_at,phishing_outcome,phishing_clicked_at,phishing_reported_at",
		"alice@example.test,,,SAT-2026,2026 annual,2026-07-10T09:00:00Z,sim-1,2026-07-01,reported,,2026-07-02T08:00:00Z",
		",rippling:worker-2,hris,SAT-2026,2026 annual,2026-07-11,,,,,",
	}, "\n")
	rows, failures, err := parseCompletionCSV(strings.NewReader(in), DefaultCSVImportLimits)
	if err != nil {
		t.Fatalf("parseCompletionCSV: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none", failures)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	r0 := rows[0]
	if r0.row != 1 || r0.workEmail != "alice@example.test" || r0.courseCode != "SAT-2026" || r0.campaignName != "2026 annual" {
		t.Fatalf("row 1 mismatch: %+v", r0)
	}
	if want := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC); !r0.completedAt.Equal(want) {
		t.Fatalf("row 1 completedAt = %s, want %s", r0.completedAt, want)
	}
	if r0.phishing == nil || r0.phishing.SimulationID != "sim-1" || r0.phishing.Outcome != PhishingReported {
		t.Fatalf("row 1 phishing mismatch: %+v", r0.phishing)
	}
	if !r0.phishing.SentAt.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("row 1 phishing sentAt = %s", r0.phishing.SentAt)
	}
	if r0.phishing.ClickedAt != nil || r0.phishing.ReportedAt == nil {
		t.Fatalf("row 1 phishing timestamps mismatch: %+v", r0.phishing)
	}
	r1 := rows[1]
	if r1.row != 2 || r1.sourcePersonID != "rippling:worker-2" || r1.personSource != PersonSourceHRIS || r1.phishing != nil {
		t.Fatalf("row 2 mismatch: %+v", r1)
	}
	if want := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC); !r1.completedAt.Equal(want) {
		t.Fatalf("row 2 completedAt = %s, want %s", r1.completedAt, want)
	}
}

func TestParseCompletionCSV_HeaderBOMAndUnknownColumns(t *testing.T) {
	t.Parallel()
	in := "\uFEFFwork_email,course_code,campaign_name,completed_at,lms_vendor_extra\n" +
		"alice@example.test,SAT-2026,2026 annual,2026-07-10,ignored\n"
	rows, failures, err := parseCompletionCSV(strings.NewReader(in), DefaultCSVImportLimits)
	if err != nil || len(failures) != 0 || len(rows) != 1 {
		t.Fatalf("rows=%d failures=%+v err=%v", len(rows), failures, err)
	}
}

func TestParseCompletionCSV_FatalErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		lim  CSVImportLimits
		want error
	}{
		{"empty file", "", DefaultCSVImportLimits, ErrCSVHeader},
		{"missing completed_at column", "work_email,course_code,campaign_name\n", DefaultCSVImportLimits, ErrCSVHeader},
		{"missing person key columns", "course_code,campaign_name,completed_at\n", DefaultCSVImportLimits, ErrCSVHeader},
		{"duplicate column", "work_email,work_email,course_code,campaign_name,completed_at\n", DefaultCSVImportLimits, ErrCSVHeader},
		{
			"too many rows",
			"work_email,course_code,campaign_name,completed_at\na@x.test,C,N,2026-07-10\nb@x.test,C,N,2026-07-10\n",
			CSVImportLimits{MaxRows: 1, MaxFieldBytes: 1 << 20},
			ErrCSVTooManyRows,
		},
		{
			"field too large",
			"work_email,course_code,campaign_name,completed_at\n" + strings.Repeat("a", 32) + "@x.test,C,N,2026-07-10\n",
			CSVImportLimits{MaxRows: 10, MaxFieldBytes: 16},
			ErrCSVFieldTooLarge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseCompletionCSV(strings.NewReader(tc.in), tc.lim)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseCompletionCSV_RejectsZeroLimits(t *testing.T) {
	t.Parallel()
	if _, _, err := parseCompletionCSV(strings.NewReader("work_email,course_code,campaign_name,completed_at\n"), CSVImportLimits{}); err == nil {
		t.Fatal("zero limits accepted")
	}
}

func TestParseCompletionCSV_PerRowFailures(t *testing.T) {
	t.Parallel()
	header := "work_email,source_person_id,person_source,course_code,campaign_name,completed_at,phishing_simulation_id,phishing_sent_at,phishing_outcome"
	cases := []struct {
		name    string
		row     string
		wantErr string
	}{
		{"missing person key", ",,,SAT,2026,2026-07-10,,,", "need work_email or source_person_id"},
		{"invalid person_source", ",p-1,ldap,SAT,2026,2026-07-10,,,", "invalid person_source"},
		{"missing course_code", "a@x.test,,,,2026,2026-07-10,,,", "missing course_code"},
		{"missing campaign_name", "a@x.test,,,SAT,,2026-07-10,,,", "missing campaign_name"},
		{"missing completed_at", "a@x.test,,,SAT,2026,,,,", "missing completed_at"},
		{"invalid completed_at", "a@x.test,,,SAT,2026,July 10 2026,,,", "invalid completed_at"},
		{"ragged short row", "a@x.test,,,SAT", "missing campaign_name"},
		{"phishing outcome without simulation id", "a@x.test,,,SAT,2026,2026-07-10,,,clicked", "phishing_outcome set without phishing_simulation_id"},
		{"phishing missing sent_at", "a@x.test,,,SAT,2026,2026-07-10,sim-1,,clicked", "missing phishing_sent_at"},
		{"phishing invalid outcome", "a@x.test,,,SAT,2026,2026-07-10,sim-1,2026-07-01,self_reported", "invalid phishing_outcome"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows, failures, err := parseCompletionCSV(strings.NewReader(header+"\n"+tc.row+"\n"), DefaultCSVImportLimits)
			if err != nil {
				t.Fatalf("fatal err = %v, want per-row failure", err)
			}
			if len(rows) != 0 {
				t.Fatalf("rows = %+v, want none", rows)
			}
			if len(failures) != 1 {
				t.Fatalf("failures = %+v, want exactly 1", failures)
			}
			f := failures[0]
			if f.Status != RowError || f.Row != 1 || !strings.Contains(f.Error, tc.wantErr) {
				t.Fatalf("failure = %+v, want row 1 error containing %q", f, tc.wantErr)
			}
		})
	}
}

func TestParseCompletionCSV_RowErrorDoesNotAbortRemainingRows(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		"work_email,course_code,campaign_name,completed_at",
		"a@x.test,SAT,2026,not-a-date",
		"b@x.test,SAT,2026,2026-07-10",
	}, "\n")
	rows, failures, err := parseCompletionCSV(strings.NewReader(in), DefaultCSVImportLimits)
	if err != nil {
		t.Fatalf("parseCompletionCSV: %v", err)
	}
	if len(failures) != 1 || failures[0].Row != 1 {
		t.Fatalf("failures = %+v, want row 1 only", failures)
	}
	if len(rows) != 1 || rows[0].row != 2 || rows[0].workEmail != "b@x.test" {
		t.Fatalf("rows = %+v, want row 2 parsed", rows)
	}
}

func TestParseCSVTime(t *testing.T) {
	t.Parallel()
	if got, err := parseCSVTime(""); got != nil || err != nil {
		t.Fatalf("empty = (%v, %v), want (nil, nil)", got, err)
	}
	got, err := parseCSVTime("2026-07-10T09:00:00-07:00")
	if err != nil || !got.Equal(time.Date(2026, 7, 10, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("rfc3339 = (%v, %v)", got, err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("rfc3339 not normalized to UTC: %v", got)
	}
	if _, err := parseCSVTime("10/07/2026"); err == nil {
		t.Fatal("accepted non-contract date format")
	}
}

func TestImportReportCounters(t *testing.T) {
	t.Parallel()
	var r ImportReport
	r.add(RowResult{Status: RowImported})
	r.add(RowResult{Status: RowAlreadyComplete})
	r.add(RowResult{Status: RowError})
	if r.Imported != 1 || r.AlreadyComplete != 1 || r.Failed != 1 || len(r.Results) != 3 {
		t.Fatalf("report counters mismatch: %+v", r)
	}
}

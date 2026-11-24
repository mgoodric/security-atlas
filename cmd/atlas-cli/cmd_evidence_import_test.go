package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseImportLines(t *testing.T) {
	t.Parallel()
	got, err := parseImportLines(strings.NewReader("{\"evidence_kind\":\"a\"}\n\n  \n{\"evidence_kind\":\"b\"}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || string(got[1]) != `{"evidence_kind":"b"}` {
		t.Fatalf("got %q", got)
	}
	if _, err := parseImportLines(strings.NewReader("{\"a\":1}\n[1,2]\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("array line: err = %v, want line 2 error", err)
	}
	if _, err := parseImportLines(strings.NewReader("\n\n")); err == nil {
		t.Fatal("empty file accepted")
	}
}

func TestImportError_FrozenPeriods(t *testing.T) {
	t.Parallel()
	err := importError(409, []byte(`{"error":"x","code":"frozen_audit_period","frozen_periods":[{"audit_period_id":"p1","name":"FY25","records":3}]}`))
	if !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), "p1") || !strings.Contains(err.Error(), "--amend-frozen") {
		t.Fatalf("err = %v", err)
	}
	if err := importError(403, []byte(`{"error":"admin credential required","code":"forbidden"}`)); !strings.Contains(err.Error(), "HTTP 403: admin credential required") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderEvidenceImport(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	raw := []byte(`{"import_batch_id":"b1","receipts":[{"deduplicated":false},{"deduplicated":true}],
		"amendments":[{"audit_period_id":"p1","revision":2,"frozen_hash":"aa","previous_hash":"bb","evidence_added_n":1}]}`)
	if err := renderEvidenceImport(&out, raw); err != nil {
		t.Fatalf("render: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "2 records (1 new, 1 already in the ledger)") || !strings.Contains(s, "revision 2: +1 records, hash aa (was bb)") {
		t.Fatalf("output = %q", s)
	}
}

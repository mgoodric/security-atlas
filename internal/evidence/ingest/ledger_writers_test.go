package ingest

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// directLedgerWrite matches a write to the evidence ledger that does not
// go through this package: the sqlc InsertEvidenceRecord query, a raw
// INSERT INTO evidence_records, or a COPY into the table.
var directLedgerWrite = regexp.MustCompile(`(?i)\.InsertEvidenceRecord\(|insert\s+into\s+evidence_records\b|Identifier\{"evidence_records"\}`)

// ledgerWriteExceptions names every production file allowed to write the
// evidence ledger outside this package, with the reason. Adding an entry
// needs a reason a reviewer can check (#1830).
var ledgerWriteExceptions = map[string]string{
	// Backdated demo dataset (twelve months of observed_at), which the
	// ingest skew check refuses by design. Confined to a tenant created and
	// demo-marked in the same transaction by assertFreshDemoTenant.
	"internal/demoseed/writers.go": "demo seed",
}

// TestNoDirectEvidenceLedgerWrites fails when production Go code outside
// internal/evidence/ingest writes evidence_records directly. Every
// ledger write must go through Service.Process or Service.ProcessInTx so
// it gets the observed_at skew check, schema validation, kind scope and
// an evidence_audit_log row (#1751, #1830). Test files are exempt: they
// seed fixtures. The generated sqlc package is exempt: it defines the
// query this package calls.
func TestNoDirectEvidenceLedgerWrites(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var offenders []string
	seenExceptions := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".git", rel == ".claude", rel == "node_modules",
				strings.HasPrefix(rel, "web/node_modules"),
				rel == "internal/db/dbx", rel == "internal/evidence/ingest":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		hits, err := ledgerWriteLines(path)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			return nil
		}
		if _, ok := ledgerWriteExceptions[rel]; ok {
			seenExceptions[rel] = true
			return nil
		}
		for _, h := range hits {
			offenders = append(offenders, rel+":"+h)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, o := range offenders {
		t.Errorf("direct evidence-ledger write outside internal/evidence/ingest: %s\n"+
			"\troute it through ingest.Service.Process or ProcessInTx, or add a justified entry to ledgerWriteExceptions", o)
	}
	// A stale exception would silently permit a future direct write in
	// that file; drop it once the file no longer needs it.
	for file := range ledgerWriteExceptions {
		if !seenExceptions[file] {
			t.Errorf("ledgerWriteExceptions lists %s, which no longer writes evidence_records directly; remove the entry", file)
		}
	}
}

// ledgerWriteLines returns "line: text" for each match of
// directLedgerWrite in path. Matching runs over the whole file so a raw
// INSERT split across lines is still caught; // comment lines are blanked
// first so prose mentioning the table does not trip it.
func ledgerWriteLines(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			lines[i] = ""
		}
	}
	src := strings.Join(lines, "\n")
	var hits []string
	for _, loc := range directLedgerWrite.FindAllStringIndex(src, -1) {
		n := strings.Count(src[:loc[0]], "\n")
		hits = append(hits, strconv.Itoa(n+1)+": "+strings.TrimSpace(lines[n]))
	}
	return hits, nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/evidence/ingest/<file> -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

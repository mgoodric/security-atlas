package ingest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// ledgerTable matches the evidence_records table name as SQL may spell
// it: bare, double-quoted, or schema-qualified (public.evidence_records,
// "public"."evidence_records").
const ledgerTable = `(?:"?\w+"?\s*\.\s*)?"?evidence_records"?(?:[^\w]|$)`

// directLedgerWrite matches a write to the evidence ledger that does not
// go through this package: a call to any sqlc InsertEvidenceRecord*
// query, a raw INSERT INTO or COPY into the table, or a pgx.Identifier
// naming it (the CopyFrom target), schema-qualified or not.
var directLedgerWrite = regexp.MustCompile(`(?i)\.InsertEvidenceRecord\w*\(` +
	`|insert\s+into\s+` + ledgerTable +
	`|copy\s+` + ledgerTable +
	`|Identifier\s*\{\s*(?:"\w+"\s*,\s*)?"evidence_records"\s*\}`)

// sqlLedgerWrite matches a raw INSERT INTO or COPY into the ledger in a
// sqlc query file.
var sqlLedgerWrite = regexp.MustCompile(`(?i)insert\s+into\s+` + ledgerTable + `|copy\s+` + ledgerTable)

// ledgerInsertQuery is the one sqlc query allowed to insert into the
// ledger; only this package calls it.
const ledgerInsertQuery = "InsertEvidenceRecord"

// ledgerWriteExceptions names every production function allowed to write
// the evidence ledger outside this package, keyed by file, with the
// reason. Adding an entry needs a reason a reviewer can check (#1830).
var ledgerWriteExceptions = map[string]struct{ fn, reason string }{
	// Backdated demo dataset (twelve months of observed_at), which the
	// ingest skew check refuses by design. Confined to a tenant created and
	// demo-marked in the same transaction by assertFreshDemoTenant.
	"internal/demoseed/writers.go": {fn: "writeEvidence", reason: "demo seed"},
}

// TestNoDirectEvidenceLedgerWrites fails when production Go code outside
// internal/evidence/ingest writes evidence_records directly, or when a
// sqlc query file defines a ledger write other than InsertEvidenceRecord.
// Every ledger write must go through Service.Process or Service.ProcessInTx
// so it gets the observed_at skew check, schema validation, kind scope and
// an evidence_audit_log row (#1751, #1830, #1933). Test files are exempt:
// they seed fixtures. The generated sqlc package is exempt: it defines the
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
		hits, err := ledgerWriteHits(path)
		if err != nil {
			return err
		}
		exc, excepted := ledgerWriteExceptions[rel]
		for _, h := range hits {
			if excepted && h.fn == exc.fn {
				seenExceptions[rel] = true
				continue
			}
			offenders = append(offenders, rel+":"+h.String())
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
	// that function; drop it once the function no longer needs it.
	for file, exc := range ledgerWriteExceptions {
		if !seenExceptions[file] {
			t.Errorf("ledgerWriteExceptions lists %s %s, which no longer writes evidence_records directly; remove the entry", file, exc.fn)
		}
	}

	sqlOffenders, err := sqlLedgerWriteQueries(filepath.Join(root, "internal", "db", "queries"))
	if err != nil {
		t.Fatalf("scan sqlc queries: %v", err)
	}
	for _, o := range sqlOffenders {
		t.Errorf("sqlc query writes evidence_records: %s\n"+
			"\tonly %s may insert into the ledger, and only internal/evidence/ingest calls it", o, ledgerInsertQuery)
	}
}

// TestDirectLedgerWritePattern pins what the guard's matcher catches, so
// tightening or loosening it is a visible change.
func TestDirectLedgerWritePattern(t *testing.T) {
	t.Parallel()
	match := []string{
		`q.InsertEvidenceRecord(ctx, p)`,
		`q.InsertEvidenceRecordBatch(ctx, p)`,
		`INSERT INTO evidence_records (id) VALUES ($1)`,
		"insert into\n\tevidence_records(id)",
		`INSERT INTO public.evidence_records (id)`,
		`INSERT INTO "evidence_records" (id)`,
		`INSERT INTO "public"."evidence_records" (id)`,
		`COPY evidence_records FROM STDIN`,
		`pgx.Identifier{"evidence_records"}`,
		`pgx.Identifier{"public", "evidence_records"}`,
	}
	for _, s := range match {
		if !directLedgerWrite.MatchString(s) {
			t.Errorf("directLedgerWrite does not match %q", s)
		}
	}
	noMatch := []string{
		`INSERT INTO evidence_records_archive (id)`,
		`INSERT INTO evidence_audit_log (id)`,
		`SELECT * FROM evidence_records`,
		`q.GetEvidenceRecordByIdempotency(ctx, p)`,
	}
	for _, s := range noMatch {
		if directLedgerWrite.MatchString(s) {
			t.Errorf("directLedgerWrite matches %q", s)
		}
	}
}

// TestSQLLedgerWriteQueries checks the sqlc-file scan on a fixture: the
// named InsertEvidenceRecord query passes, any other ledger insert fails,
// and a comment mentioning the table does not.
func TestSQLLedgerWriteQueries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `-- name: InsertEvidenceRecord :one
INSERT INTO evidence_records (id) VALUES ($1) RETURNING *;

-- name: InsertControlEvaluation :one
-- Note the absence of any INSERT into evidence_records here.
INSERT INTO control_evaluations (id) VALUES ($1);

-- name: BackfillEvidence :exec
INSERT INTO public.evidence_records (id) SELECT id FROM staging;
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.sql"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := sqlLedgerWriteQueries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "BackfillEvidence") {
		t.Fatalf("offenders = %q, want only BackfillEvidence", got)
	}
}

// TestLedgerWriteHitsNameEnclosingFunc checks each hit carries the
// function it sits in, which is what scopes an exception to one function
// rather than its whole file.
func TestLedgerWriteHitsNameEnclosingFunc(t *testing.T) {
	t.Parallel()
	src := "package p\n\n" +
		"func writeEvidence() {\n\texec(`INSERT INTO evidence_records (id)`)\n}\n\n" +
		"func writeOther() {\n\t// INSERT INTO evidence_records in prose\n\texec(`INSERT INTO evidence_records (id)`)\n}\n"
	path := filepath.Join(t.TempDir(), "w.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	hits, err := ledgerWriteHits(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].fn != "writeEvidence" || hits[1].fn != "writeOther" {
		t.Fatalf("hits = %v, want one in writeEvidence and one in writeOther", hits)
	}
}

type ledgerWriteHit struct {
	line int
	fn   string
	text string
}

func (h ledgerWriteHit) String() string {
	return strconv.Itoa(h.line) + " (" + h.fn + "): " + h.text
}

// ledgerWriteHits returns each match of directLedgerWrite in path with
// its line and enclosing function. Matching runs over the whole file so
// a raw INSERT split across lines is still caught; // comment lines are
// blanked first so prose mentioning the table does not trip it.
func ledgerWriteHits(path string) ([]ledgerWriteHit, error) {
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
	locs := directLedgerWrite.FindAllStringIndex(src, -1)
	if len(locs) == 0 {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, raw, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var hits []ledgerWriteHit
	for _, loc := range locs {
		n := strings.Count(src[:loc[0]], "\n")
		hits = append(hits, ledgerWriteHit{
			line: n + 1,
			fn:   enclosingFunc(fset, file, n+1),
			text: strings.TrimSpace(lines[n]),
		})
	}
	return hits, nil
}

// enclosingFunc names the top-level function or method declared around
// line, or "" at package level.
func enclosingFunc(fset *token.FileSet, file *ast.File, line int) string {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fset.Position(fd.Pos()).Line <= line && line <= fset.Position(fd.End()).Line {
			return fd.Name.Name
		}
	}
	return ""
}

// sqlQueryName matches a sqlc query header.
var sqlQueryName = regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)

// sqlLedgerWriteQueries returns "file: query" for every sqlc query in
// dir/*.sql that inserts into or copies into evidence_records, other than
// ledgerInsertQuery. SQL comment lines are ignored.
func sqlLedgerWriteQueries(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	var offenders []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		src := string(raw)
		heads := sqlQueryName.FindAllStringSubmatchIndex(src, -1)
		for i, h := range heads {
			end := len(src)
			if i+1 < len(heads) {
				end = heads[i+1][0]
			}
			name := src[h[2]:h[3]]
			if name == ledgerInsertQuery {
				continue
			}
			if sqlLedgerWrite.MatchString(stripSQLComments(src[h[1]:end])) {
				offenders = append(offenders, filepath.Base(f)+": "+name)
			}
		}
	}
	return offenders, nil
}

func stripSQLComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "--") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
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

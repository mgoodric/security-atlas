//go:build integration

// #2973 review: the static guard is only as good as the lexer's agreement
// with the server about where code stops and a literal or comment starts.
// These tests ask the server itself. Each query places a sentinel after a
// fragment; when the server reads the sentinel as code it names it in an
// "undefined column" (42703) or "undefined function" (42883) error. Whenever
// it does, the lexer must also read the sentinel as code (or refuse the
// query), and the guard must refuse the schema-qualified form. The lexer
// may see more code than the server does, which only refuses more.

package eval

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const lexSentinel = "zz_sentinel"

// serverReadsSentinel runs q in a savepoint and reports whether the server
// parsed the sentinel as code, and whether the answer is conclusive (the
// query ran, or failed naming the sentinel).
func serverReadsSentinel(t *testing.T, ctx context.Context, tx pgx.Tx, q string) (code, conclusive bool) {
	t.Helper()
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	defer func() { _ = sp.Rollback(ctx) }()
	rows, err := sp.Query(ctx, q)
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	if err == nil {
		return false, true
	}
	// Match the server's own wording for the bare identifier: a quoted
	// identifier that merely contains the sentinel (", zz_sentinel '") is
	// reported as itself, and is one identifier the lexer also reads whole.
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "42703" || pg.Code == "42883") &&
		(strings.Contains(pg.Message, `column "`+lexSentinel+`" does not exist`) ||
			strings.Contains(pg.Message, "function pg_catalog."+lexSentinel+"() does not exist")) {
		return true, true
	}
	return false, false
}

// lexerReadsSentinel reports whether the lexer reads the sentinel as an
// identifier in code; a query it cannot lex counts as code (refused).
func lexerReadsSentinel(q string) bool {
	toks, err := lexSQL(q)
	if err != nil {
		return true
	}
	for _, tk := range toks {
		if tk.kind == tokIdent && tk.val == lexSentinel {
			return true
		}
	}
	return false
}

func beginLexOracle(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.NewAppPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	for _, s := range []string{
		"SET TRANSACTION READ ONLY",
		"SET LOCAL statement_timeout = 2000",
		"SET LOCAL search_path TO ''",
		"SET LOCAL standard_conforming_strings TO on", // as evalSQLQuery runs it
	} {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return ctx, tx
}

// TestLexerAgreesWithServer_Seeds pins each boundary rule both ways: the
// lexer reads the sentinel as code exactly when the server does.
func TestLexerAgreesWithServer_Seeds(t *testing.T) {
	ctx, tx := beginLexOracle(t)
	s := ", " + lexSentinel
	for _, tc := range []struct {
		frag string
		code bool
	}{
		{"--\r" + s, true},
		{"--x\r" + s, true},
		{"--\n" + s, true},
		{"--\r\n" + s, true},
		{", 1 --\r--\r" + s, true},
		{"-- " + s, false},
		{"/* " + s + " */", false},
		{"/* /* */ " + s + " */", false},
		{"/* /* */ */" + s, true},
		{", 1 -- /* \n" + s, true},
		{", 1 /* -- */" + s, true},
		{", 'x' /* ' */" + s, true},
		{", 'x" + s + "'", false},
		{", 'it''s'" + s, true},
		{", 'it''" + s + "'", false},
		{", 'a\\'" + s + " --'", true},
		{", E'a\\'" + s + " --'", false},
		{", e'a\\\\'" + s, true},
		{", 'a'\n'b'" + s, true},
		{", $$" + s + "$$", false},
		{", $q$ $$ " + s + " $q$", false},
		{", $q$x$q$" + s, true},
		{", $q$x$Q$" + s + "$q$", false},
		{", 2 AS x$a$" + s, true},
		{", 2 AS \"x\"\"y\"" + s, true},
		{", 2 AS \"x" + s + "\"", false},
		{", U&'\\0041" + s + "'", false},
		{", U&'a'" + s, true},
		{", N'a'" + s, true},
		{", X'0F'" + s, true},
		{", 1+--\r2" + s, true},
		{", 1-/* */2" + s, true},
	} {
		q := "SELECT 1 AS a" + tc.frag
		code, conclusive := serverReadsSentinel(t, ctx, tx, q)
		if !conclusive {
			t.Errorf("%q: the server's answer is inconclusive; fix the seed", q)
			continue
		}
		if code != tc.code {
			t.Errorf("%q: server reads the sentinel as code = %v, seed says %v", q, code, tc.code)
		}
		if got := lexerReadsSentinel(q); got != code {
			t.Errorf("%q: lexer reads the sentinel as code = %v, server = %v", q, got, code)
		}
	}
}

// lexFuzzPieces covers every quote form, both comment forms and every line
// ending, plus the characters that sit next to them.
var lexFuzzPieces = []string{
	"'", "''", "\"", "\"\"", "E'", "e'", "U&'", "U&\"", "N'", "X'", "\\", "\\'",
	"$$", "$a$", "$b$", "a$", "$1", "--", "-", "/*", "*/", "/", "*", "\n", "\r", "\r\n",
	" ", "\t", "x", "1", "(", ")", "+", ",", "||", "::", ".",
}

// TestLexerNeverMissesCodeTheServerRuns_Fuzz generates fragments around the
// sentinel from a fixed seed. Whenever the server runs the sentinel as code,
// the lexer must read it as code, and the guard must refuse the query with
// the sentinel schema-qualified.
func TestLexerNeverMissesCodeTheServerRuns_Fuzz(t *testing.T) {
	ctx, tx := beginLexOracle(t)
	rng := rand.New(rand.NewPCG(2973, 1))
	piece := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteString(lexFuzzPieces[rng.IntN(len(lexFuzzPieces))])
		}
		return b.String()
	}
	// Most random fragments leave a literal or comment open, or are not
	// valid SQL, so the server answers few of them. Pairing a random prefix
	// with a closer drawn from the same pieces lifts the share that reach
	// the sentinel as code without fixing which forms appear.
	const cases = 6000
	seenCode := 0
	for range cases {
		pre := piece(rng.IntN(6))
		if rng.IntN(2) == 0 {
			pre += piece(1+rng.IntN(2)) + "\n"
		}
		post := piece(rng.IntN(4))
		q := "SELECT 1 AS a" + pre + ", " + lexSentinel + " " + post
		if code, ok := serverReadsSentinel(t, ctx, tx, q); ok && code {
			seenCode++
			if !lexerReadsSentinel(q) {
				t.Errorf("server runs the sentinel as code but the lexer reads it as a literal or comment: %q", q)
			}
		}
		qq := "SELECT 1 AS a" + pre + ", pg_catalog." + lexSentinel + "() " + post
		if code, ok := serverReadsSentinel(t, ctx, tx, qq); ok && code {
			seenCode++
			if err := validateSQLEvidenceQuery(qq); err == nil {
				t.Errorf("server calls pg_catalog.%s but the guard accepts the query: %q", lexSentinel, qq)
			}
		}
	}
	// A corpus the server never runs as code proves nothing.
	if seenCode < 400 {
		t.Fatalf("only %d of %d generated queries reached the server as code; the corpus is too degenerate to test the lexer", seenCode, 2*cases)
	}
	t.Logf("%d of %d generated queries ran the sentinel as code", seenCode, 2*cases)
}

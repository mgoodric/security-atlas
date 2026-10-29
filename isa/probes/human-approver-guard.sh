#!/usr/bin/env bash
#
# human-approver-guard.sh -- falsifier for isa/ISA.md ISC-4: "No audit-binding
# artifact reaches a customer or auditor without a recorded human approver."
# The claim's own falsifier has two halves: a row carrying ai_assisted=true
# and human_approved=true with a null approver, OR any publish path that
# bypasses the guard entirely.
#
# Which artifacts count is answered in isa/ISA.md ("Audit-binding artifact"):
# the ones that can carry model-drafted content today are questionnaire
# responses (the PDF export a customer receives) and the board-pack narrative.
# The deterministic exports (OSCAL SSP/AP/AR/POA&M, audit-period bundles,
# walkthrough exports, the templated board brief) are audit-binding too but
# hold no model output, so the guard has nothing to gate there yet.
#
# The schema guard (ai_assist_human_approver_guard, C11) only stops an
# approval being recorded without an approver. It does not stop an
# UNAPPROVED draft being published: that is the publish path's job, so the
# publish paths are tested directly.
#
#   1. questionnaire export -- questionnaire.PDFItems, the entire content
#      selection of POST /v1/questionnaires/{id}/export-pdf (the handler
#      passes its result to the renderer unchanged), is given an AI-suggested
#      answer no human approved. If that answer reaches a PDF row, it reaches
#      the customer. No database needed.
#   2. board pack -- the approved-sections read the board pack ships from
#      (ListApprovedBoardNarrativeSections, behind
#      boardnarrative.Store.ApprovedNarrative) is run against an unapproved AI
#      section, inside a transaction that is rolled back.
#   3. the guard itself -- every table carrying ai_assisted, human_approved
#      and human_approver has a validated CHECK over all three; the shared
#      guard function refuses an approval with a NULL or empty approver; and
#      no row anywhere violates it. A new AI surface that forgets the guard
#      fails here without this probe having to know its name.
#
# Env:
#   DATABASE_URL -- the migrate/owner role, for 2 and 3: catalog visibility,
#                   a scan across every tenant, and a fixture tenant row.
#                   Unset, check 1 still runs.
#
# Exit 0  every check holds.
# Exit 1  an unapproved AI draft reaches an export, or the guard is missing,
#         wrong, or violated.
# Exit 3  nothing false was found, but the database checks could not run
#         (no DATABASE_URL, no connection, no schema), or the probe program
#         did not build.
set -uo pipefail

name=human-approver-guard

if ! command -v go >/dev/null 2>&1; then
  echo "$name: CANNOT RUN -- no go toolchain on PATH" >&2
  exit 3
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT" || exit 3

# Inside the module so it can import internal/ packages; removed on exit.
SCRATCH="$REPO_ROOT/isa-probe-human-approver-guard-scratch"
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH"
trap 'rm -rf "$SCRATCH"' EXIT

cat >"$SCRATCH/main.go" <<'GOEOF'
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

const marker = "ISA-PROBE-ISC4-UNAPPROVED-DRAFT"

var failed, unrunnable bool

func falsef(format string, a ...any) {
	failed = true
	fmt.Printf("FALSE -- "+format+"\n", a...)
}

func skipf(format string, a ...any) {
	unrunnable = true
	fmt.Printf("CANNOT RUN -- "+format+"\n", a...)
}

// 1. The questionnaire export.
func questionnaireExport() {
	items := questionnaire.PDFItems([]questionnaire.Question{{
		Code: "ISC4-1", Text: "Do you enforce MFA?", ScfAnchorID: "probe",
		Answer: &questionnaire.Answer{
			AnswerValue: marker, Narrative: marker,
			AIAssisted: true, HumanApproved: false,
		},
	}})
	for _, it := range items {
		if strings.Contains(it.AnswerValue, marker) || strings.Contains(it.Narrative, marker) {
			falsef("the questionnaire PDF export (questionnaire.PDFItems, used by ExportPDF) puts an AI-suggested answer that no human approved into the document sent to the customer")
			return
		}
	}
	fmt.Println("the questionnaire export leaves out unapproved AI answers")
}

// 2. The board pack's approved-sections read.
func boardPack(ctx context.Context, conn *pgx.Conn) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		skipf("board pack: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenant := uuid.New()
	const period = "2099-12-31"
	if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'isa-probe ISC-4')`, tenant); err != nil {
		skipf("board pack: seed tenant: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO board_narrative_sections
		  (tenant_id, section_key, period_end, raw_draft, ai_assisted, human_approved,
		   prompt_version, model_name, model_version, model_provider)
		VALUES ($1, 'isc4-probe', $2, $3, TRUE, FALSE, 'probe', 'probe', 'probe', 'ollama-local')`,
		tenant, period, marker); err != nil {
		skipf("board pack: seed an unapproved AI section: %v", err)
		return
	}
	rows, err := dbx.New(tx).ListApprovedBoardNarrativeSections(ctx, dbx.ListApprovedBoardNarrativeSectionsParams{
		TenantID: pgtype.UUID{Bytes: tenant, Valid: true}, PeriodEnd: period,
	})
	if err != nil {
		skipf("board pack: approved-sections read: %v", err)
		return
	}
	for _, r := range rows {
		if strings.Contains(r.RawDraft, marker) {
			falsef("the board pack's approved-sections read returned an AI section no human approved")
			return
		}
	}
	fmt.Println("the board pack read leaves out unapproved AI sections")
}

// 3. The guard: present on every AI-assist table, correct, and unviolated.
func guard(ctx context.Context, conn *pgx.Conn) {
	rows, err := conn.Query(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		WHERE c.table_schema = 'public' AND c.column_name IN ('ai_assisted', 'human_approved', 'human_approver')
		GROUP BY c.table_name
		HAVING count(DISTINCT c.column_name) = 3
		ORDER BY 1`)
	if err != nil {
		skipf("guard: list AI-assist tables: %v", err)
		return
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		skipf("guard: list AI-assist tables: %v", err)
		return
	}
	if len(tables) == 0 {
		skipf("guard: no table carries the AI-assist columns; is the schema migrated?")
		return
	}

	for _, t := range tables {
		var guarded bool
		err := conn.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM pg_constraint
			  WHERE conrelid = to_regclass('public.' || quote_ident($1))
			    AND contype = 'c' AND convalidated
			    AND pg_get_constraintdef(oid) LIKE '%ai_assisted%'
			    AND pg_get_constraintdef(oid) LIKE '%human_approved%'
			    AND pg_get_constraintdef(oid) LIKE '%human_approver%')`, t).Scan(&guarded)
		if err != nil {
			skipf("guard: constraints on %s: %v", t, err)
			return
		}
		if !guarded {
			falsef("%s carries ai_assisted/human_approved/human_approver but no validated CHECK over them, so an approval can be recorded with no approver", t)
		}

		var bad int
		q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE ai_assisted AND human_approved
			AND (human_approver IS NULL OR length(btrim(human_approver)) = 0)`, pgx.Identifier{t}.Sanitize())
		if err := conn.QueryRow(ctx, q).Scan(&bad); err != nil {
			skipf("guard: scan %s: %v", t, err)
			return
		}
		if bad > 0 {
			falsef("%s holds %d row(s) with ai_assisted and human_approved set and no human_approver", t, bad)
		}
	}

	var nullOK, emptyOK, draftOK bool
	err = conn.QueryRow(ctx, `SELECT ai_assist_human_approver_guard(TRUE, TRUE, NULL),
	                                 ai_assist_human_approver_guard(TRUE, TRUE, ''),
	                                 ai_assist_human_approver_guard(TRUE, FALSE, NULL)`).Scan(&nullOK, &emptyOK, &draftOK)
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "42883":
		// undefined_function: the shared guard is gone.
		falsef("the shared guard ai_assist_human_approver_guard does not exist: %v", err)
	case err != nil:
		// Anything else (a dropped connection, a timeout) says nothing about
		// the guard.
		skipf("guard: call ai_assist_human_approver_guard: %v", err)
	case nullOK || emptyOK:
		falsef("ai_assist_human_approver_guard accepts an approval with no approver (NULL accepted: %v, empty accepted: %v)", nullOK, emptyOK)
	case !draftOK:
		// Not this claim's falsifier, but a guard that refuses an unapproved
		// draft is not the guard C11 describes, and the checks above would
		// then pass for the wrong reason.
		skipf("ai_assist_human_approver_guard refuses an unapproved draft; it is not the function C11 describes")
	}
	fmt.Printf("checked the guard on %d AI-assist table(s): %s\n", len(tables), strings.Join(tables, ", "))
}

func main() {
	ctx := context.Background()
	questionnaireExport()

	if url := os.Getenv("DATABASE_URL"); url == "" {
		skipf("DATABASE_URL is not set; the board pack and guard checks did not run")
	} else if conn, err := pgx.Connect(ctx, url); err != nil {
		skipf("connect via DATABASE_URL: %v", err)
	} else {
		defer func() { _ = conn.Close(ctx) }()
		boardPack(ctx, conn)
		guard(ctx, conn)
	}

	switch {
	case failed:
		os.Exit(1)
	case unrunnable:
		os.Exit(3)
	}
	os.Exit(0)
}
GOEOF

# Built, then run, rather than `go run`: `go run` reports any non-zero exit
# of the program as its own exit 1, which would turn exit 3 into a verdict.
if ! build_out="$(go build -o "$SCRATCH/probe" "$SCRATCH/main.go" 2>&1)"; then
  echo "$name: CANNOT RUN -- the probe program did not build: $build_out" >&2
  exit 3
fi
out="$("$SCRATCH/probe" 2>&1)"
rc=$?
printf '%s\n' "$out" | sed "s/^/$name: /"
exit "$rc"

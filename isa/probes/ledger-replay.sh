#!/usr/bin/env bash
#
# ledger-replay.sh -- falsifier for isa/ISA.md ISC-3: "Any past date is
# reconstructible from the evidence ledger." The claim's own falsifier: a
# replay at time T that disagrees with what the platform reported at T.
#
# Both halves of that sentence are taken from the code, not restated in SQL:
#
#   reported at T  the control_evaluations row the real evaluation engine
#                  (internal/eval Engine.EvaluateControl, live horizon
#                  eval.FarFuture) appends, stamped evaluated_at = T. It is
#                  what GET /v1/controls/:id/state serves.
#   replay at T    the same engine re-run with trigger=replay and horizon T --
#                  the recompute-from-ledger path (Engine.Replay is EvaluateAll
#                  over the same call).
#
# The scenario is the ordinary late-arrival case: a connector that pushes
# late, a backdated manual upload, a clock-skewed source. A record whose
# observed_at is before T but which is written to the ledger after T was not
# there when the platform reported at T. A replay of T that counts it is a
# replay of a T that never happened.
#
#   1. seed a control (freshness_class daily) and evidence E1, result pass,
#      observed one hour ago
#   2. evaluate live; the row written is "reported at T"
#   3. append E2, result fail, observed 30 minutes before T, ingested now
#   4. replay with horizon T, and compare result, freshness_status,
#      evidence_count_in_window and last_observed_at with step 2
#
# Then a second falsifier, for the property every replay rests on: the ledger
# rows themselves cannot be rewritten. As the app role, an UPDATE of E1's
# result and observed_at and a DELETE of E1 must each change nothing. Run in a
# transaction that is rolled back, so it leaves nothing either way. (ADR-0020's
# erasure tombstone redacts personal-data fields only; it never touches
# result, observed_at or ingested_at, so it is not an exception here.)
#
# Not exercised, and worth knowing: the engine computes the freshness window
# from wall-clock now, not from the horizon (Engine.now feeds computeRow), so
# a replay of a date more than one freshness window old can also disagree.
# This probe cannot show that without waiting a day, and step 4 already
# decides the claim.
#
# Residue: the engine opens and commits its own transactions, and the ledger
# is append-only, so steps 1-4 leave one synthetic tenant behind (a random
# tenant id with no tenants row; titles begin "isa-probe ISC-3"). Run it
# against a throwaway database, as the `ISA · probes` CI job does.
#
# Env:
#   DATABASE_URL_APP -- the non-owner app role RLS binds on. The owner role is
#                       not used: it bypasses RLS, so a mutation check run as
#                       it would prove nothing about the app's ledger.
#
# Exit 0  replay at T agrees with T, and the ledger refused every rewrite.
# Exit 1  replay at T disagrees with T, or a ledger row was rewritten.
# Exit 3  cannot run: no go toolchain, no DATABASE_URL_APP, no connection,
#         or the engine failed for a reason unrelated to the claim.
set -uo pipefail

name=ledger-replay

if ! command -v go >/dev/null 2>&1; then
  echo "$name: CANNOT RUN -- no go toolchain on PATH" >&2
  exit 3
fi
if [ -z "${DATABASE_URL_APP:-}" ]; then
  echo "$name: CANNOT RUN -- DATABASE_URL_APP is not set" >&2
  exit 3
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT" || exit 3

# Inside the module so it can import internal/eval (Go's internal-import rule);
# removed on exit. Same shape as isa/probes/delivery-ssrf.sh.
SCRATCH="$REPO_ROOT/isa-probe-ledger-replay-scratch"
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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type state struct {
	result, freshness string
	inWindow          int
	lastObserved      *time.Time
	evaluatedAt       time.Time
}

func (s state) String() string {
	lo := "none"
	if s.lastObserved != nil {
		lo = s.lastObserved.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("result=%s freshness=%s in_window=%d last_observed_at=%s", s.result, s.freshness, s.inWindow, lo)
}

func (s state) same(o state) bool {
	if s.result != o.result || s.freshness != o.freshness || s.inWindow != o.inWindow {
		return false
	}
	if (s.lastObserved == nil) != (o.lastObserved == nil) {
		return false
	}
	return s.lastObserved == nil || s.lastObserved.Equal(*o.lastObserved)
}

func cannotRun(format string, a ...any) {
	fmt.Printf("CANNOT RUN -- "+format+"\n", a...)
	os.Exit(3)
}

// inTenant runs fn in a transaction bound to the tenant, committing only when
// commit is true.
func inTenant(ctx context.Context, pool *pgxpool.Pool, commit bool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if commit {
		return tx.Commit(ctx)
	}
	return nil
}

func latest(ctx context.Context, pool *pgxpool.Pool, control uuid.UUID, trigger string) (state, error) {
	var s state
	err := inTenant(ctx, pool, false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT result::text, freshness_status, evidence_count_in_window, last_observed_at, evaluated_at
			FROM control_evaluations
			WHERE control_id = $1 AND trigger = $2
			ORDER BY evaluated_at DESC, created_at DESC
			LIMIT 1`, control, trigger).
			Scan(&s.result, &s.freshness, &s.inWindow, &s.lastObserved, &s.evaluatedAt)
	})
	return s, err
}

func insertEvidence(ctx context.Context, tx pgx.Tx, tenant, control, id uuid.UUID, observed time.Time, result string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO evidence_records
		  (id, tenant_id, control_id, control_ref, observed_at, provenance, result, hash, freshness_class, ingestion_path)
		VALUES ($1, $2, $3, $4, $5, '{"source":"isa-probe ISC-3"}', $6::evidence_result, $7, 'daily', 'push')`,
		id, tenant, control, control.String(), observed, result, "isa-probe-isc3-"+id.String())
	return err
}

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL_APP"))
	if err != nil {
		cannotRun("open pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		cannotRun("connect via DATABASE_URL_APP: %v", err)
	}

	tenant, control, e1, e2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx, err = tenancy.WithTenant(ctx, tenant.String())
	if err != nil {
		cannotRun("tenant context: %v", err)
	}

	// 1. A control and the evidence the platform had before T.
	err = inTenant(ctx, pool, true, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, bundle_id, title, control_family, implementation_type, freshness_class)
			VALUES ($1, $2, $3, 'isa-probe ISC-3 control', 'probe', 'automated', 'daily')`,
			control, tenant, "isa-probe-isc3-"+control.String()); err != nil {
			return err
		}
		return insertEvidence(ctx, tx, tenant, control, e1, time.Now().Add(-time.Hour), "pass")
	})
	if err != nil {
		cannotRun("seed control and E1: %v", err)
	}

	engine := eval.NewEngine(eval.NewStore(pool), scope.NewStore(pool))

	// 2. What the platform reported at T.
	if _, err := engine.EvaluateControl(ctx, control, eval.TriggerManual, eval.FarFuture); err != nil {
		cannotRun("live evaluation: %v", err)
	}
	reported, err := latest(ctx, pool, control, eval.TriggerManual)
	if err != nil {
		cannotRun("read the reported row: %v", err)
	}
	t := reported.evaluatedAt

	// 3. A record about a moment before T, written after T.
	time.Sleep(50 * time.Millisecond)
	err = inTenant(ctx, pool, true, func(tx pgx.Tx) error {
		return insertEvidence(ctx, tx, tenant, control, e2, t.Add(-30*time.Minute), "fail")
	})
	if err != nil {
		cannotRun("append late evidence E2: %v", err)
	}

	// 4. Replay T.
	if _, err := engine.EvaluateControl(ctx, control, eval.TriggerReplay, t); err != nil {
		cannotRun("replay: %v", err)
	}
	replayed, err := latest(ctx, pool, control, eval.TriggerReplay)
	if err != nil {
		cannotRun("read the replay row: %v", err)
	}

	failed := false
	if !replayed.same(reported) {
		failed = true
		fmt.Printf("FALSE -- replay at T disagrees with what the platform reported at T (T = %s)\n", t.UTC().Format(time.RFC3339Nano))
		fmt.Printf("  reported at T: %s\n", reported)
		fmt.Printf("  replay of T:   %s\n", replayed)
		fmt.Println("  E2 (observed before T, ingested after it) was not in the ledger at T, but the replay counted it.")
	} else {
		fmt.Printf("replay at T agrees with T: %s\n", reported)
	}

	// The ledger rows themselves, as the app role. Rolled back regardless.
	var rewrites []string
	err = inTenant(ctx, pool, false, func(tx pgx.Tx) error {
		attempts := []struct{ what, sql string }{
			{"UPDATE result and observed_at", `UPDATE evidence_records SET result = 'fail', observed_at = observed_at - interval '1 day' WHERE id = $1`},
			{"DELETE", `DELETE FROM evidence_records WHERE id = $1`},
		}
		for _, a := range attempts {
			if _, err := tx.Exec(ctx, "SAVEPOINT attempt"); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, a.sql, e1)
			if err != nil {
				// Refused outright (a privilege, a policy, a trigger): the
				// ledger held. Undo the aborted savepoint and carry on.
				if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT attempt"); rbErr != nil {
					return errors.Join(err, rbErr)
				}
				continue
			}
			if tag.RowsAffected() > 0 {
				rewrites = append(rewrites, fmt.Sprintf("%s changed %d row(s)", a.what, tag.RowsAffected()))
			}
		}
		return nil
	})
	if err != nil {
		cannotRun("ledger rewrite check: %v", err)
	}
	if len(rewrites) > 0 {
		failed = true
		fmt.Println("FALSE -- the app role rewrote a ledger row (rolled back here, but nothing stopped it):")
		for _, r := range rewrites {
			fmt.Println("  " + r)
		}
	} else {
		fmt.Println("the ledger refused every rewrite of an existing row")
	}

	if failed {
		os.Exit(1)
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

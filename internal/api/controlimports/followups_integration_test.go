//go:build integration

// #2038 — follow-ups to the import candidates (#1780): the sweeper that fails
// batches stuck pending, keyword retrieval pinned to the batch's SCF release,
// and the composite candidate-to-row FK. Real Postgres with RLS.
package controlimports_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// setSuggestions forces a batch's suggestions status and age, standing in
// for a background run whose process died.
func setSuggestions(t *testing.T, admin *pgxpool.Pool, batchID, status string, age time.Duration) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		UPDATE control_import_batches
		SET suggestions_status = $2, created_at = now() - $3::interval, updated_at = now() - $3::interval
		WHERE id = $1::uuid`, batchID, status, age.String()); err != nil {
		t.Fatalf("set suggestions: %v", err)
	}
}

func batchState(t *testing.T, admin *pgxpool.Pool, batchID string) (string, time.Time) {
	t.Helper()
	var status string
	var updated time.Time
	if err := admin.QueryRow(context.Background(), `
		SELECT suggestions_status, updated_at FROM control_import_batches WHERE id = $1::uuid`,
		batchID).Scan(&status, &updated); err != nil {
		t.Fatalf("read batch: %v", err)
	}
	return status, updated
}

// ----- the sweeper fails stale pending batches, once, in every tenant -----

func TestSweeperFailsStalePendingBatchesOnce(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)

	tenantA, tenantB := freshTenant(t, admin), freshTenant(t, admin)
	staleA := uploadFixture(t, testServer(t, app, tenantA), cat.framework, "csv")
	freshA := uploadFixture(t, testServer(t, app, tenantA), cat.framework, "csv")
	readyA := uploadFixture(t, testServer(t, app, tenantA), cat.framework, "csv")
	staleB := uploadFixture(t, testServer(t, app, tenantB), cat.framework, "csv")
	setSuggestions(t, admin, staleA, controlimport.SuggestionsPending, time.Hour)
	setSuggestions(t, admin, freshA, controlimport.SuggestionsPending, time.Minute)
	setSuggestions(t, admin, readyA, controlimport.SuggestionsReady, time.Hour)
	setSuggestions(t, admin, staleB, controlimport.SuggestionsPending, time.Hour)

	// Two replicas sweep at once.
	sweeper := controlimport.NewSweeper(admin, app, nil, 30*time.Minute)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = sweeper.SweepOnce(context.Background())
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
	}

	for id, want := range map[string]string{
		staleA: controlimport.SuggestionsFailed,
		staleB: controlimport.SuggestionsFailed,
		freshA: controlimport.SuggestionsPending,
		readyA: controlimport.SuggestionsReady,
	} {
		if got, _ := batchState(t, admin, id); got != want {
			t.Errorf("batch %s suggestions = %q, want %q", id, got, want)
		}
	}

	// A later sweep leaves the failed batch alone.
	_, before := batchState(t, admin, staleA)
	if _, err := sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	if _, after := batchState(t, admin, staleA); !after.Equal(before) {
		t.Errorf("second sweep touched the failed batch: updated_at %v -> %v", before, after)
	}

	// Run sweeps once on start, which is what catches batches orphaned by
	// the restart, and returns when its context ends.
	staleAgain := uploadFixture(t, testServer(t, app, tenantA), cat.framework, "csv")
	setSuggestions(t, admin, staleAgain, controlimport.SuggestionsPending, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sweeper.Run(ctx, time.Hour) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, _ := batchState(t, admin, staleAgain); got == controlimport.SuggestionsFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not sweep on start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v after cancel, want nil", err)
	}
}

// ----- failed is final: a late run's ready save is refused -----

func TestLateRunCannotOverwriteASweptBatch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	batchID := uploadFixture(t, testServer(t, app, tenant), cat.framework, "csv")
	setSuggestions(t, admin, batchID, controlimport.SuggestionsPending, time.Hour)

	candidates := func() string {
		t.Helper()
		var s string
		if err := admin.QueryRow(context.Background(), `
			SELECT COALESCE(string_agg(row_id::text || ':' || rank || ':' || scf_anchor_id::text, ',' ORDER BY row_id, rank), '')
			FROM control_import_candidates WHERE batch_id = $1::uuid`, batchID).Scan(&s); err != nil {
			t.Fatalf("read candidates: %v", err)
		}
		return s
	}
	before := candidates()

	if _, err := controlimport.NewSweeper(admin, app, nil, 30*time.Minute).SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	// The late run: one row, its candidates replaced by a single new one.
	var rowID, anchorID string
	if err := admin.QueryRow(context.Background(), `
		SELECT r.id::text, a.id::text
		FROM control_import_rows r, scf_anchors a
		WHERE r.batch_id = $1::uuid
		  AND NOT EXISTS (SELECT 1 FROM control_import_candidates c WHERE c.row_id = r.id AND c.scf_anchor_id = a.id)
		LIMIT 1`, batchID).Scan(&rowID, &anchorID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	tctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	late := []controlimport.RowSuggestions{{
		RowID: rowID,
		Candidates: []controlimport.Candidate{{
			Rank: 1, AnchorID: anchorID, SCFID: "LATE", Title: "Late", Source: controlimport.SourceKeyword,
		}},
	}}
	err = controlimport.NewStore(app).SaveSuggestions(tctx, batchID, late, controlimport.SuggestionsReady)
	if !errors.Is(err, controlimport.ErrSuggestionsNoLongerPending) {
		t.Fatalf("late ready save err = %v, want ErrSuggestionsNoLongerPending", err)
	}
	if got, _ := batchState(t, admin, batchID); got != controlimport.SuggestionsFailed {
		t.Errorf("suggestions = %q after the late save, want failed", got)
	}
	if after := candidates(); after != before {
		t.Errorf("late save replaced candidates:\nbefore %s\nafter  %s", before, after)
	}
}

// ----- keyword retrieval reads the batch's release, not every current one -----

func TestKeywordAnchorsComeFromThePinnedRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	current, _ := currentSCFRelease(t, admin)

	var scfFramework uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_id FROM framework_versions WHERE id = $1`, current).Scan(&scfFramework); err != nil {
		t.Fatalf("read scf framework: %v", err)
	}
	suffix := strings.ToLower(strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	legacy, legacyAnchor, currentAnchor := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM scf_anchors WHERE id = ANY($1)`, []uuid.UUID{legacyAnchor, currentAnchor})
		_, _ = admin.Exec(context.Background(), `DELETE FROM framework_versions WHERE id = $1`, legacy)
	})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status) VALUES ($1, NULL, $2, $3, 'legacy')`,
			[]any{legacy, scfFramework, "pin-" + suffix}},
		{`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description) VALUES ($1, $2, $3, 'PIN', $4, '')`,
			[]any{legacyAnchor, legacy, "PINOLD-" + suffix, "Zebracrypt" + suffix + " legacy"}},
		{`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description) VALUES ($1, $2, $3, 'PIN', $4, '')`,
			[]any{currentAnchor, current, "PINNEW-" + suffix, "Zebracrypt" + suffix + " current"}},
	} {
		if _, err := admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v\nsql: %s", err, q.sql)
		}
	}

	tctx, err := tenancy.WithTenant(ctx, freshTenant(t, admin))
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	store := controlimport.NewStore(app)
	keywords := []string{"zebracrypt" + suffix}
	ids := func(version uuid.UUID) string {
		t.Helper()
		got, err := store.KeywordAnchors(tctx, version.String(), keywords)
		if err != nil {
			t.Fatalf("KeywordAnchors: %v", err)
		}
		out := make([]string, len(got))
		for i, c := range got {
			out[i] = c.AnchorID
		}
		return strings.Join(out, ",")
	}
	if got := ids(legacy); got != legacyAnchor.String() {
		t.Errorf("pinned to the legacy release: anchors %q, want only %s", got, legacyAnchor)
	}
	if got := ids(current); got != currentAnchor.String() {
		t.Errorf("pinned to the current release: anchors %q, want only %s", got, currentAnchor)
	}
}

// ----- a candidate's row must belong to the candidate's batch and tenant -----

func TestCandidateRowMustBelongToItsBatch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)
	batchX := uploadFixture(t, env, cat.framework, "csv")
	batchY := uploadFixture(t, env, cat.framework, "csv")

	// An anchor not already on the row, so only the FK can reject it.
	var rowOfY, anchor string
	if err := admin.QueryRow(context.Background(), `
		SELECT r.id::text, a.id::text
		FROM control_import_rows r, scf_anchors a
		WHERE r.batch_id = $1::uuid
		  AND NOT EXISTS (SELECT 1 FROM control_import_candidates c WHERE c.row_id = r.id AND c.scf_anchor_id = a.id)
		LIMIT 1`, batchY).Scan(&rowOfY, &anchor); err != nil {
		t.Fatalf("read row: %v", err)
	}
	_, err := admin.Exec(context.Background(), `
		INSERT INTO control_import_candidates (tenant_id, batch_id, row_id, rank, scf_anchor_id, scf_id, title, source)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 99, $4::uuid, 'X', 'X', 'keyword')`,
		tenant, batchX, rowOfY, anchor)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("candidate in batch X naming a row of batch Y: err = %v, want a foreign key violation", err)
	}
}

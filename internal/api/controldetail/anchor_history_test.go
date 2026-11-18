package controldetail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// stubAnchorHistory serves fixed rows, or err, and records the page it was
// asked for.
type stubAnchorHistory struct {
	rows []dbx.ListControlAnchorHistoryPagedRow
	err  error
	got  historyPage
}

func (s *stubAnchorHistory) AnchorHistoryForControl(_ context.Context, _ uuid.UUID, p historyPage) ([]dbx.ListControlAnchorHistoryPagedRow, error) {
	s.got = p
	if s.err != nil {
		return nil, s.err
	}
	if int(p.pageRows)+1 < len(s.rows) {
		return s.rows[:p.pageRows+1], nil
	}
	return s.rows, nil
}

const anchorHistoryPattern = "/v1/controls/{id}/anchor-history"

func anchorHistoryPath(q string) string {
	return "/v1/controls/11111111-1111-1111-1111-111111111111/anchor-history" + q
}

func decisionRow(id uuid.UUID, at time.Time, approved bool) dbx.ListControlAnchorHistoryPagedRow {
	row := dbx.ListControlAnchorHistoryPagedRow{
		ID:              pgtype.UUID{Bytes: id, Valid: true},
		Decision:        "rejected",
		LegacyControlID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		LegacyVersion:   1,
		LegacyScfID:     "IAC-01",
		LegacyRelease:   "2024.1",
		DecidedBy:       "system:scf-import",
		DecidedAt:       pgtype.Timestamptz{Time: at, Valid: true},
	}
	if approved {
		v, code, rel := int32(2), "IAC-01.1", "2025.1"
		row.Decision = "approved"
		row.NewControlID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
		row.NewVersion, row.NewScfID, row.NewRelease = &v, &code, &rel
	}
	return row
}

func TestAnchorHistory_Handler_WireShapeAndPaging(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	stub := &stubAnchorHistory{rows: []dbx.ListControlAnchorHistoryPagedRow{
		decisionRow(uuid.New(), now, true),
		decisionRow(uuid.New(), now.Add(-time.Hour), false),
	}}
	h := &Handler{anchors: stub}
	router, req, rec := mountForPath(http.MethodGet, anchorHistoryPath("?limit=1"), h.AnchorHistory, anchorHistoryPattern)
	router.ServeHTTP(rec, withFakeTenant(t, req, uuid.NewString()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if stub.got.pageRows != 1 {
		t.Fatalf("page rows = %d, want 1", stub.got.pageRows)
	}
	var body struct {
		Decisions  []anchorHistoryWire `json:"decisions"`
		Count      int                 `json:"count"`
		NextCursor string              `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Decisions) != 1 {
		t.Fatalf("count = %d, decisions = %d; want 1, 1", body.Count, len(body.Decisions))
	}
	d := body.Decisions[0]
	if d.Decision != "approved" || d.LegacySCFID != "IAC-01" || d.LegacyRelease != "2024.1" ||
		d.NewSCFID == nil || *d.NewSCFID != "IAC-01.1" || d.NewVersion == nil || *d.NewVersion != 2 ||
		d.DecidedBy != "system:scf-import" || d.DecidedAt != "2026-11-01T12:00:00Z" {
		t.Fatalf("decision = %+v", d)
	}
	cur, err := decodeCursor(body.NextCursor)
	if err != nil || !cur.ts.Equal(now) {
		t.Fatalf("next_cursor = %q (%v, %v); want the last row's keyset", body.NextCursor, cur, err)
	}
}

func TestAnchorHistory_Handler_LastPageHasNoCursorAndNullTargets(t *testing.T) {
	t.Parallel()
	stub := &stubAnchorHistory{rows: []dbx.ListControlAnchorHistoryPagedRow{decisionRow(uuid.New(), time.Now(), false)}}
	h := &Handler{anchors: stub}
	router, req, rec := mountForPath(http.MethodGet, anchorHistoryPath(""), h.AnchorHistory, anchorHistoryPattern)
	router.ServeHTTP(rec, withFakeTenant(t, req, uuid.NewString()))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["next_cursor"] != "" {
		t.Fatalf("next_cursor = %v, want empty", body["next_cursor"])
	}
	d := body["decisions"].([]any)[0].(map[string]any)
	for _, k := range []string{"new_control_id", "new_version", "new_scf_id", "new_release", "reason"} {
		if d[k] != nil {
			t.Fatalf("%s = %v, want null on a rejection", k, d[k])
		}
	}
}

func TestAnchorHistory_Handler_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		err  error
		cred *credstore.Credential
		want int
	}{
		{"not a uuid", "/v1/controls/not-a-uuid/anchor-history", nil, nil, http.StatusBadRequest},
		{"bad cursor", anchorHistoryPath("?cursor=@@@"), nil, nil, http.StatusBadRequest},
		{"bad limit", anchorHistoryPath("?limit=999"), nil, nil, http.StatusBadRequest},
		{"other tenant's control", anchorHistoryPath(""), errControlNotFound, nil, http.StatusNotFound},
		{"store failure", anchorHistoryPath(""), errors.New("boom"), nil, http.StatusInternalServerError},
		{"no tenant role", anchorHistoryPath(""), nil, &credstore.Credential{UserID: "test-viewer"}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{anchors: &stubAnchorHistory{err: c.err}}
			router, req, rec := mountForPath(http.MethodGet, c.path, h.AnchorHistory, anchorHistoryPattern)
			if c.cred != nil {
				req = req.WithContext(authctx.WithCredential(req.Context(), *c.cred))
			} else {
				req = withFakeTenant(t, req, uuid.NewString())
			}
			router.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

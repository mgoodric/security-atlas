// pagination.go — opaque keyset-cursor pagination for the two append-only
// ledger reads (evidence, history).
//
// Why keyset, not OFFSET: both evidence_records and control_evaluations are
// append-only. OFFSET pagination drifts when rows are appended between page
// fetches (page 2 skips or repeats rows). A keyset cursor over
// (sort_timestamp, id) is stable under concurrent appends.
//
// The cursor is opaque: a base64url-encoded "RFC3339Nano|uuid" string. The
// wire contract does not leak the internal shape, and the handler treats a
// malformed cursor as a 400. A zero/absent cursor selects the first page via
// a far-future sentinel timestamp + the max UUID, so the keyset predicate
//
//	observed_at < cursor_ts OR (observed_at = cursor_ts AND id < cursor_id)
//
// matches every row on the first call.
package controldetail

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	// defaultWindow is the AC-1 default evidence window: last 30 days.
	defaultWindow = 30 * 24 * time.Hour
)

// errBadCursor / errBadLimit / errBadTime are surfaced by the handler as 400.
var (
	errBadCursor = errors.New("cursor is malformed")
	errBadLimit  = errors.New("limit must be an integer between 1 and 200")
	errBadTime   = errors.New("timestamp must be RFC3339")
)

// keyset is the decoded cursor: the (timestamp, id) coordinate of the last
// row on the previous page. The next page selects rows strictly before it.
type keyset struct {
	ts time.Time
	id uuid.UUID
}

// firstPageCursor is the sentinel keyset that selects the first page: a
// far-future timestamp paired with the max UUID, so every real row sorts
// strictly before it under the keyset predicate.
func firstPageCursor() keyset {
	return keyset{
		ts: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		id: uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
	}
}

// encodeCursor renders a keyset as the opaque base64url wire token. An empty
// keyset (the zero value) encodes to the empty string so the handler can
// omit next_cursor when there is no next page.
func encodeCursor(k keyset) string {
	if k.ts.IsZero() {
		return ""
	}
	raw := k.ts.UTC().Format(time.RFC3339Nano) + "|" + k.id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses the opaque wire token back into a keyset. An empty
// token yields the first-page sentinel. A malformed token is errBadCursor.
func decodeCursor(token string) (keyset, error) {
	if token == "" {
		return firstPageCursor(), nil
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return keyset{}, errBadCursor
	}
	parts := strings.SplitN(string(rawBytes), "|", 2)
	if len(parts) != 2 {
		return keyset{}, errBadCursor
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return keyset{}, errBadCursor
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return keyset{}, errBadCursor
	}
	return keyset{ts: ts.UTC(), id: id}, nil
}

// parseLimit reads ?limit=, defaulting to 50, capping at 200. A non-integer
// or out-of-range value is errBadLimit.
func parseLimit(raw string) (int32, error) {
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		return 0, errBadLimit
	}
	return int32(n), nil
}

// parseRFC3339 reads an optional RFC3339 query param. An empty value yields
// the supplied fallback; a malformed value is errBadTime.
func parseRFC3339(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errBadTime
	}
	return ts.UTC(), nil
}

// evidencePage is the resolved parameter set for one evidence page read.
// pageRows is the caller-requested page size; the Store fetches pageRows+1
// rows so the handler can detect a next page without a COUNT round-trip.
type evidencePage struct {
	since    time.Time
	until    time.Time
	cursor   keyset
	pageRows int32
}

// evidenceListPage is the resolved parameter set for the tenant-wide
// ledger query (slice 106). Carries the same window + cursor + pageRows
// as evidencePage, plus the optional filters. Empty-string filter
// values mean "no filter on this column" and are translated to nil via
// optString at the store boundary.
//
// Slice 234 — `scopeCellID` is the optional `?scope_cell_id=<uuid>`
// filter (the /evidence page's new Scope pill binds to this). The zero
// value (`uuid.Nil`) is the no-filter sentinel; the store wraps it as
// `pgtype.UUID{Valid: false}` and the SQL query treats the invalid
// sentinel as "no filter on this column".
type evidenceListPage struct {
	since           time.Time
	until           time.Time
	kind            string
	result          string
	sourceActorType string
	sourceActorID   string
	scopeCellID     uuid.UUID
	cursor          keyset
	pageRows        int32
}

// historyPage is the resolved parameter set for one history page read.
// pageRows carries the same probe-row contract as evidencePage.
type historyPage struct {
	cursor   keyset
	pageRows int32
}

// optString returns nil for an empty string, &s otherwise. The sqlc
// narg-based filter columns expect *string at the Go boundary; nil
// means "skip this predicate".
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// validResults is the canonical evidence_result enum value set. Matches
// dbx.EvidenceResult* constants in internal/db/dbx/models.go. Used by the
// handler to 400 on a malformed ?result= query param before we even open
// a transaction.
var validResults = map[string]struct{}{
	"pass":         {},
	"fail":         {},
	"na":           {},
	"inconclusive": {},
}

// isValidResult reports whether s is a valid evidence_result enum value.
// Empty string is reported as valid because the absence of a ?result=
// query param is legal (no filter).
func isValidResult(s string) bool {
	if s == "" {
		return true
	}
	_, ok := validResults[s]
	return ok
}

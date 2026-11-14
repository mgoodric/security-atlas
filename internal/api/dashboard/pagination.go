// pagination.go — opaque keyset-cursor pagination for the paginated
// dashboard activity feed. (The upcoming rollup it also served was retired
// by #2130.)
//
// Why keyset, not OFFSET: the read sits over an append-only source. OFFSET pagination drifts when rows are added between page
// fetches (page 2 skips or repeats rows). A keyset cursor over
// (sort_timestamp, id) is stable under concurrent change.
//
// The cursor is opaque: a base64url-encoded "RFC3339Nano|id" string. The
// wire contract does not leak the internal shape, and the handler treats a
// malformed cursor as a 400. The activity feed sorts newest-first, so an
// absent cursor selects the first page via a far-FUTURE sentinel timestamp.
package dashboard

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// errBadCursor / errBadLimit are surfaced by the handlers as 400.
var (
	errBadCursor = errors.New("cursor is malformed")
	errBadLimit  = errors.New("limit must be an integer between 1 and 200")
)

// keyset is the decoded cursor: the (timestamp, id) coordinate of the last
// row on the previous page. The id is a free-form string because the
// activity feed's resource_id is the view's text projection.
type keyset struct {
	ts time.Time
	id string
}

// farFuture is the activity feed's first-page sentinel (newest-first,
// keyset predicate `ts < cursor`): every row sorts strictly before it.
var farFuture = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// firstPageActivity is the sentinel keyset selecting the activity feed's
// first page: a far-future timestamp + the max id string so every real row
// sorts strictly before it under `ts < cursor_ts OR (ts = cursor_ts AND
// resource_id < cursor_id)`.
func firstPageActivity() keyset {
	return keyset{ts: farFuture, id: "\U0010FFFF"}
}

// encodeCursor renders a keyset as the opaque base64url wire token. A zero
// keyset encodes to the empty string so the handler can omit next_cursor
// when there is no next page.
func encodeCursor(k keyset) string {
	if k.ts.IsZero() {
		return ""
	}
	raw := k.ts.UTC().Format(time.RFC3339Nano) + "|" + k.id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses the opaque wire token into a keyset. An empty token
// yields the supplied first-page sentinel. A malformed token is
// errBadCursor.
func decodeCursor(token string, firstPage keyset) (keyset, error) {
	if token == "" {
		return firstPage, nil
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
	return keyset{ts: ts.UTC(), id: parts[1]}, nil
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

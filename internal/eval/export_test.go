package eval

import (
	"context"
	"time"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// SetAppClockForTest replaces e's database clock with now, an app-side clock
// that may disagree with the database's (#3038).
func SetAppClockForTest(e *Engine, now func() time.Time) {
	e.clock = func(context.Context, *dbx.Queries) (time.Time, error) { return now().UTC(), nil }
}

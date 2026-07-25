package backup

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// namePrefix + nameTimeLayout build/parse a backup artifact name. The
// timestamp is embedded so retention can select by recency from the name
// alone (no metadata round-trip). UTC, sortable, filesystem-safe.
const (
	namePrefix     = "atlas-backup-"
	nameTimeLayout = "20060102T150405Z"
	nameSuffix     = ".sql"
)

// ArtifactName returns the canonical backup name for a moment in time.
func ArtifactName(t time.Time) string {
	return namePrefix + t.UTC().Format(nameTimeLayout) + nameSuffix
}

// parseArtifactTime extracts the embedded timestamp from a backup name.
func parseArtifactTime(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, namePrefix) || !strings.HasSuffix(name, nameSuffix) {
		return time.Time{}, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(name, namePrefix), nameSuffix)
	t, err := time.Parse(nameTimeLayout, mid)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// SelectForDeletion implements the retention policy (D5, P0-510-3): given the
// existing backup objects, return the names that fall OUTSIDE the keep window.
//
// The keep window is: the most-recent keepDaily backups (by day), PLUS the
// most-recent backup of each of the last keepWeekly ISO weeks. Everything
// else is selected for deletion — bounding storage growth. A backup that
// qualifies under EITHER rule is retained.
//
// Pure function: no I/O, deterministic, table-tested (slice-353 Q-2 fast loop).
func SelectForDeletion(objs []BackupObject, keepDaily, keepWeekly int) []string {
	if keepDaily < 0 {
		keepDaily = 0
	}
	if keepWeekly < 0 {
		keepWeekly = 0
	}
	// Only consider artifacts whose name parses to a backup timestamp; an
	// unrelated file in the directory is never deleted by rotation.
	type dated struct {
		name string
		ts   time.Time
	}
	dateds := make([]dated, 0, len(objs))
	for _, o := range objs {
		if ts, ok := parseArtifactTime(o.Name); ok {
			dateds = append(dateds, dated{name: o.Name, ts: ts})
		}
	}
	// Newest first.
	sort.Slice(dateds, func(i, j int) bool { return dateds[i].ts.After(dateds[j].ts) })

	keep := make(map[string]struct{}, len(dateds))

	// Daily rule: most-recent backup per UTC day, keep the newest keepDaily
	// distinct days.
	seenDays := map[string]struct{}{}
	dayKept := 0
	for _, d := range dateds {
		day := d.ts.Format("2006-01-02")
		if _, ok := seenDays[day]; ok {
			continue // already kept the day's most-recent
		}
		if dayKept >= keepDaily {
			break
		}
		seenDays[day] = struct{}{}
		keep[d.name] = struct{}{}
		dayKept++
	}

	// Weekly rule: most-recent backup per ISO week, keep the newest
	// keepWeekly distinct weeks.
	seenWeeks := map[string]struct{}{}
	weekKept := 0
	for _, d := range dateds {
		y, w := d.ts.ISOWeek()
		wk := fmt.Sprintf("%04d-W%02d", y, w)
		if _, ok := seenWeeks[wk]; ok {
			continue
		}
		if weekKept >= keepWeekly {
			break
		}
		seenWeeks[wk] = struct{}{}
		keep[d.name] = struct{}{}
		weekKept++
	}

	var del []string
	for _, d := range dateds {
		if _, ok := keep[d.name]; !ok {
			del = append(del, d.name)
		}
	}
	sort.Strings(del)
	return del
}

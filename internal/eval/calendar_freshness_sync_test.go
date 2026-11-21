// Pins the freshness-class CASE blocks in internal/db/queries/calendar.sql to
// freshnessMaxAgeTable (#2868). The calendar replicates the mapping in SQL to
// compute review due dates; if either copy changes alone, the calendar's due
// dates disagree with evaluation freshness. Pure-Go: it parses the SQL file,
// no database.
package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	calendarCaseStart = regexp.MustCompile(`(?i)CASE\s+c\.freshness_class\b`)
	calendarCaseEnd   = regexp.MustCompile(`(?i)\bEND\b`)
	calendarWhen      = regexp.MustCompile(`(?i)\bWHEN\b`)
	calendarWhenArm   = regexp.MustCompile(`(?i)\bWHEN\s+'([a-z_]+)'\s+THEN\s+INTERVAL\s+'(\d+)\s+days?'`)
	calendarElseArm   = regexp.MustCompile(`(?i)\bELSE\s+INTERVAL\s+'(\d+)\s+days?'`)
	sqlLineComment    = regexp.MustCompile(`--[^\n]*`)
)

type freshnessCase struct {
	arms     map[string]time.Duration
	fallback time.Duration
}

// parseFreshnessCases returns every `CASE c.freshness_class ... END` block in
// sql. Each block must consist only of `WHEN '<class>' THEN INTERVAL 'N days'`
// arms plus one `ELSE INTERVAL 'N days'`; any other shape is an error so a
// rewrite of the SQL cannot slip past the comparison unparsed. Line comments
// are stripped first; they are not SQL.
func parseFreshnessCases(sql string) ([]freshnessCase, error) {
	sql = sqlLineComment.ReplaceAllString(sql, "")
	var out []freshnessCase
	for _, loc := range calendarCaseStart.FindAllStringIndex(sql, -1) {
		rest := sql[loc[1]:]
		end := calendarCaseEnd.FindStringIndex(rest)
		if end == nil {
			return nil, fmt.Errorf("CASE at offset %d has no END", loc[0])
		}
		body := rest[:end[0]]
		arms := calendarWhenArm.FindAllStringSubmatch(body, -1)
		if n := len(calendarWhen.FindAllStringIndex(body, -1)); n != len(arms) {
			return nil, fmt.Errorf("CASE at offset %d: %d WHEN arms, only %d in the expected shape", loc[0], n, len(arms))
		}
		elses := calendarElseArm.FindAllStringSubmatch(body, -1)
		if len(elses) != 1 {
			return nil, fmt.Errorf("CASE at offset %d: want exactly one ELSE INTERVAL arm, got %d", loc[0], len(elses))
		}
		c := freshnessCase{arms: map[string]time.Duration{}}
		for _, a := range arms {
			if _, dup := c.arms[a[1]]; dup {
				return nil, fmt.Errorf("CASE at offset %d: class %q appears twice", loc[0], a[1])
			}
			c.arms[a[1]] = days(a[2])
		}
		c.fallback = days(elses[0][1])
		out = append(out, c)
	}
	return out, nil
}

func days(s string) time.Duration {
	n, _ := strconv.Atoi(s) // the regex admits only digits
	return time.Duration(n) * 24 * time.Hour
}

// checkFreshnessCases reports every way blocks disagree with
// freshnessMaxAgeTable and defaultFreshnessMaxAge.
func checkFreshnessCases(blocks []freshnessCase) []string {
	var problems []string
	if len(blocks) == 0 {
		return []string{"no `CASE c.freshness_class` blocks found"}
	}
	for i, b := range blocks {
		for _, class := range FreshnessClasses() {
			want, _ := FreshnessMaxAge(class)
			got, ok := b.arms[class]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("block %d: class %q missing (Go: %v)", i, class, want))
			case got != want:
				problems = append(problems, fmt.Sprintf("block %d: class %q is %v, Go has %v", i, class, got, want))
			}
		}
		for class := range b.arms {
			if _, known := FreshnessMaxAge(class); !known {
				problems = append(problems, fmt.Sprintf("block %d: class %q is not in freshnessMaxAgeTable", i, class))
			}
		}
		if b.fallback != defaultFreshnessMaxAge {
			problems = append(problems, fmt.Sprintf("block %d: ELSE is %v, Go fallback is %v", i, b.fallback, defaultFreshnessMaxAge))
		}
	}
	return problems
}

func TestCalendarSQL_FreshnessCaseMatchesFreshnessMaxAge(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "db", "queries", "calendar.sql"))
	if err != nil {
		t.Fatalf("read calendar.sql: %v", err)
	}
	blocks, err := parseFreshnessCases(string(raw))
	if err != nil {
		t.Fatalf("parse calendar.sql: %v", err)
	}
	for _, p := range checkFreshnessCases(blocks) {
		t.Error(p)
	}
}

// The checker itself must fail on drift, or the test above proves nothing.
func TestCheckFreshnessCases_DetectsDrift(t *testing.T) {
	t.Parallel()
	good := func() string {
		var b strings.Builder
		b.WriteString("CASE c.freshness_class\n")
		for _, class := range FreshnessClasses() {
			d, _ := FreshnessMaxAge(class)
			fmt.Fprintf(&b, "  WHEN '%s' THEN INTERVAL '%d days'\n", class, int(d/(24*time.Hour)))
		}
		fmt.Fprintf(&b, "  ELSE INTERVAL '%d days'\nEND", int(defaultFreshnessMaxAge/(24*time.Hour)))
		return b.String()
	}()

	cases := []struct {
		name     string
		sql      string
		parseErr bool
		drift    bool
	}{
		{"in sync", good, false, false},
		{"window changed", strings.Replace(good, "'400 days'", "'365 days'", 1), false, true},
		{"fallback changed", strings.Replace(good, "ELSE INTERVAL '90 days'", "ELSE INTERVAL '30 days'", 1), false, true},
		{"class missing", strings.Replace(good, "  WHEN 'weekly' THEN INTERVAL '30 days'\n", "", 1), false, true},
		{"class unknown", strings.Replace(good, "'weekly'", "'hourly'", 1), false, true},
		{"arm in another shape", strings.Replace(good, "INTERVAL '7 days'", "INTERVAL '1 week'", 1), true, false},
		{"no blocks", "SELECT 1", false, true},
	}
	for _, c := range cases {
		blocks, err := parseFreshnessCases(c.sql)
		if (err != nil) != c.parseErr {
			t.Errorf("%s: parse err = %v, want err %v", c.name, err, c.parseErr)
			continue
		}
		if err != nil {
			continue
		}
		if got := len(checkFreshnessCases(blocks)) > 0; got != c.drift {
			t.Errorf("%s: drift reported = %v, want %v", c.name, got, c.drift)
		}
	}
}

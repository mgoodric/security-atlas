package eval

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// unfilteredSites counts, per sqlc query, the control_evaluations read
// sites that may skip the replay filter, and why (#3071). Every other site
// names evaluated_as_of before the next site begins. The count must match
// exactly, so a removed site is noticed as well as an added one.
var unfilteredSites = map[string]struct {
	n      int
	reason string
}{
	// The ledger, newest first, with each row's trigger: history, not state.
	"ListControlEvaluationHistoryPaged": {1, "history listing"},
	// Skip scans that enumerate a control's cells. A cell seen only in
	// replay rows has no live row, so the filtered per-cell probe drops it.
	"ListActiveControlsWithState": {2, "cell skip scan"},
	"CountActiveControls":         {2, "cell skip scan"},
	"ListInconclusiveStreaks":     {2, "cell skip scan"},
	// Rows joined to a run a filtered site already picked.
	"FrameworkPosture":              {2, "excepted CTEs join the filtered latest run"},
	"ListFailingEvaluationsForPack": {1, "joins the filtered latest run"},
	// The AR ranks runs by COALESCE(evaluated_as_of, evaluated_at), live
	// first on a tie, and carries evaluated_as_of on what it takes from a
	// replay. The outer read joins the ranked run.
	"ListFailingEvaluationsAsOf":  {1, "joins the AR's ranked run"},
	"ListExceptedEvaluationsAsOf": {1, "joins the AR's ranked run"},
}

var (
	queryName = regexp.MustCompile(`(?m)^-- name: (\w+)`)
	readSite  = regexp.MustCompile(`\b(?:FROM|JOIN)\s+control_evaluations\b`)
	blockCmt  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	rawString = regexp.MustCompile("(?s)`[^`]*`")
)

// #3071: a replay of an earlier instant is not a control's latest state.
// Every read site of control_evaluations in a sqlc query names
// evaluated_as_of (to filter replay rows out, or to rank by the instant a
// row judged) before the next site, or is counted above with its reason. A
// new latest-state read that forgets the filter fails here rather than
// letting a replay surface as current.
func TestQueries_ControlEvaluationReadsHandleReplayRows(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "db", "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob queries: %v (%d files)", err, len(files))
	}
	seen := map[string]bool{}
	var problems []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for name, body := range splitQueries(string(raw)) {
			sql := stripSQLComments(body)
			if !readSite.MatchString(sql) {
				continue
			}
			got := unfilteredSiteCount(sql)
			want := unfilteredSites[name].n
			if _, ok := unfilteredSites[name]; ok {
				seen[name] = true
			}
			if got != want {
				problems = append(problems, filepath.Base(f)+": "+name+": "+strconv.Itoa(got)+" unfiltered read sites, want "+strconv.Itoa(want))
			}
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("control_evaluations read sites without the replay filter (add `evaluated_as_of IS NULL`, or count the site in unfilteredSites with its reason):\n  %s",
			strings.Join(problems, "\n  "))
	}
	for name := range unfilteredSites {
		if !seen[name] {
			t.Errorf("unfilteredSites lists %s, which no longer reads control_evaluations; remove it", name)
		}
	}
}

// #3071: the same rule for raw SQL in Go outside sqlc (the metric
// evaluators, for instance). No exemptions: a Go read that needs one is a
// sqlc query waiting to be written.
func TestGoRawSQL_ControlEvaluationReadsHandleReplayRows(t *testing.T) {
	t.Parallel()
	var problems []string
	walked := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "dbx" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		walked++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, lit := range rawString.FindAllString(string(src), -1) {
			sql := stripSQLComments(lit)
			if n := unfilteredSiteCount(sql); n > 0 {
				problems = append(problems, path+": "+strconv.Itoa(n)+" unfiltered read site(s)")
			}
		}
		return nil
	})
	if err != nil || walked == 0 {
		t.Fatalf("walk internal/: %v (%d files)", err, walked)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("raw SQL reads control_evaluations without the replay filter:\n  %s", strings.Join(problems, "\n  "))
	}
}

// unfilteredSiteCount is how many read sites have no evaluated_as_of
// between them and the next site.
func unfilteredSiteCount(sql string) int {
	locs := readSite.FindAllStringIndex(sql, -1)
	n := 0
	for i, loc := range locs {
		end := len(sql)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if !strings.Contains(sql[loc[1]:end], "evaluated_as_of") {
			n++
		}
	}
	return n
}

// splitQueries maps each `-- name:` to the text up to the next one.
func splitQueries(src string) map[string]string {
	out := map[string]string{}
	locs := queryName.FindAllStringSubmatchIndex(src, -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[src[loc[2]:loc[3]]] = src[loc[1]:end]
	}
	return out
}

// stripSQLComments drops `/* */` and `--` comments so a mention in prose
// does not count.
func stripSQLComments(sql string) string {
	sql = blockCmt.ReplaceAllString(sql, "")
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

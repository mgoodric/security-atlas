package eval

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// replayAwareReads are the sqlc queries that read control_evaluations
// without filtering out replay rows, and why each may (#3071).
var replayAwareReads = map[string]string{
	// The ledger, newest first, with each row's trigger: history, not state.
	"ListControlEvaluationHistoryPaged": "history listing",
	// The AR ranks runs by COALESCE(evaluated_as_of, evaluated_at) and
	// carries evaluated_as_of on the observations it takes from a replay.
	"ListFailingEvaluationsAsOf":  "AR ranks by judged instant",
	"ListExceptedEvaluationsAsOf": "AR ranks by judged instant",
}

var queryName = regexp.MustCompile(`(?m)^-- name: (\w+)`)

// #3071: a replay of an earlier instant is not a control's latest state.
// Every query that reads control_evaluations names evaluated_as_of, either
// to filter replay rows out or to rank by the instant a row judged, or is
// listed above with its reason. A new latest-state read that forgets the
// filter fails here rather than letting a replay surface as current.
func TestQueries_ControlEvaluationReadsHandleReplayRows(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "db", "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob queries: %v (%d files)", err, len(files))
	}
	seen := map[string]bool{}
	var missing []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for name, body := range splitQueries(string(raw)) {
			sql := stripSQLComments(body)
			if !strings.Contains(sql, "control_evaluations") || strings.Contains(sql, "INSERT INTO control_evaluations") {
				continue
			}
			if _, ok := replayAwareReads[name]; ok {
				seen[name] = true
				continue
			}
			if !strings.Contains(sql, "evaluated_as_of") {
				missing = append(missing, filepath.Base(f)+": "+name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("queries read control_evaluations without handling replay rows (add `evaluated_as_of IS NULL`, or list the query in replayAwareReads with its reason):\n  %s",
			strings.Join(missing, "\n  "))
	}
	for name := range replayAwareReads {
		if !seen[name] {
			t.Errorf("replayAwareReads lists %s, which no longer reads control_evaluations; remove it", name)
		}
	}
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

// stripSQLComments drops `--` comments so a mention in prose does not count.
func stripSQLComments(sql string) string {
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

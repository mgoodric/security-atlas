package unifiedlog

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestMachineActorsQueryMirrorsUnifiedLog keeps ListUnifiedAuditMachineActors
// (#2414) reading the same audit-log tables as ListUnifiedAuditLog, so a
// source added to the Activity feed is also searched for its actors.
func TestMachineActorsQueryMirrorsUnifiedLog(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../db/queries/unified_audit_log.sql")
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}
	feed := sourceTables(t, string(raw), "ListUnifiedAuditLog")
	actors := sourceTables(t, string(raw), "ListUnifiedAuditMachineActors")
	if len(feed) == 0 {
		t.Fatal("no source tables found in ListUnifiedAuditLog")
	}
	if !slices.Equal(feed, actors) {
		t.Fatalf("source tables differ:\n  ListUnifiedAuditLog:           %v\n  ListUnifiedAuditMachineActors: %v", feed, actors)
	}
}

var fromTable = regexp.MustCompile(`(?m)^\s*FROM ([a-z_]+_log)\b`)

// sourceTables returns the sorted `FROM <x>_log` tables in the named query.
func sourceTables(t *testing.T, sql, name string) []string {
	t.Helper()
	start := strings.Index(sql, "-- name: "+name+" ")
	if start < 0 {
		t.Fatalf("query %s not found", name)
	}
	body := sql[start+1:]
	if end := strings.Index(body, "-- name: "); end >= 0 {
		body = body[:end]
	}
	var out []string
	for _, m := range fromTable.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return slices.Compact(out)
}

//go:build integration

package streambuf_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
)

// #2435: the credential's scope_predicate rides the NATS header to the
// consumer and is enforced there. A DB-loaded key carries "{}", which must
// push; a restricted key is still enforced after the hop.
func TestScopePredicate_EnforcedAcrossStream(t *testing.T) {
	f := boot(t)
	pub := streambuf.NewJetStreamPublisher(f.conn)
	ctx := context.Background()

	cases := []struct {
		name      string
		predicate string
		want      ingest.Decision
		cred      credstore.Credential
	}{
		{name: "column default", predicate: "{}", want: ingest.DecisionAccepted},
		{name: "matching", predicate: `{"op":"eq","dim":"environment","value":"prod"}`, want: ingest.DecisionAccepted},
		{name: "non-matching", predicate: `{"op":"eq","dim":"environment","value":"staging"}`, want: ingest.DecisionRejectedScopeViolation},
	}
	for i := range cases {
		cases[i].cred = mkCred(f.tenant)
		cases[i].cred.ScopePredicate = cases[i].predicate
		rec := recordFor(t, "sast.scan_result.v1", "scope-"+uuid.NewString()[:8],
			map[string]any{"tool": "semgrep", "findings_count": 0})
		if _, d, err := pub.Publish(ctx, rec, cases[i].cred); err != nil || d != ingest.DecisionAccepted {
			t.Fatalf("publish %s: decision=%s err=%v", cases[i].name, d, err)
		}
	}

	consumer := streambuf.NewConsumer(f.conn, f.svc)
	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = consumer.Start(consumerCtx) }()
	defer consumer.Stop()

	waitFor(t, 10*time.Second, func() bool {
		for _, c := range cases {
			if len(listAuditEntries(t, f.pool, f.tenant, c.cred.ID)) == 0 {
				return false
			}
		}
		return true
	})
	for _, c := range cases {
		rows := listAuditEntries(t, f.pool, f.tenant, c.cred.ID)
		if len(rows) != 1 || rows[0].Decision != c.want.String() {
			t.Fatalf("%s: audit rows = %+v, want one %s", c.name, rows, c.want)
		}
	}
	if n := countEvidence(t, f.pool, f.tenant); n != 2 {
		t.Fatalf("ledger rows = %d, want 2", n)
	}
}

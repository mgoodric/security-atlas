package ingest

import (
	"context"
	"errors"
	"sync"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
)

// ErrNoAuditOutbox is returned by ProcessInTx when ctx carries no
// AuditOutbox: without one the accepted-record sink event would have
// nowhere to wait for the caller's commit (#1933).
var ErrNoAuditOutbox = errors.New("ingest: ProcessInTx needs a context from WithAuditOutbox")

// AuditOutbox holds the external audit-sink events ProcessInTx produced
// inside a caller's transaction. The evidence_audit_log row commits or
// rolls back with that transaction; the sink event must do the same, so
// it leaves the process only when the caller flushes after Commit. A
// rolled-back transaction drops its outbox and the sink never sees an
// "accepted" event for a record that does not exist (#1933).
type AuditOutbox struct {
	mu      sync.Mutex
	entries []unifiedlog.Entry
}

type auditOutboxKey struct{}

// WithAuditOutbox returns ctx carrying a fresh AuditOutbox. A domain store
// opens one for each transaction it hands to ProcessInTx and calls Flush
// once Commit succeeds; on rollback it discards the outbox unflushed.
func WithAuditOutbox(ctx context.Context) (context.Context, *AuditOutbox) {
	o := &AuditOutbox{}
	return context.WithValue(ctx, auditOutboxKey{}, o), o
}

func auditOutboxFrom(ctx context.Context) *AuditOutbox {
	o, _ := ctx.Value(auditOutboxKey{}).(*AuditOutbox)
	return o
}

func (o *AuditOutbox) add(e unifiedlog.Entry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = append(o.entries, e)
}

// Flush emits every held event to the audit sink and empties the outbox.
// Call it only after the transaction the events belong to has committed.
func (o *AuditOutbox) Flush(ctx context.Context) {
	o.mu.Lock()
	entries := o.entries
	o.entries = nil
	o.mu.Unlock()
	for _, e := range entries {
		sink.EmitDefault(ctx, e)
	}
}

// Len reports how many events are waiting for Flush.
func (o *AuditOutbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.entries)
}

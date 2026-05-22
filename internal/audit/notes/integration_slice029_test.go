//go:build integration

// Integration tests for slice 029: Audit Hub threaded comments +
// in-app notifications. Real Postgres only -- RLS cannot be tested
// against a fake DB, and the append-only conversion is meaningless
// against anything but a real role+policy enforcement.
//
// Run with:
//
//	just test-integration
//	# or directly:
//	go test -tags=integration -race ./internal/audit/notes/... ./internal/audit/notifications/...
//
// Required env (same as slice 025):
//
//	DATABASE_URL      migration role DSN (BYPASSRLS); used by the harness
//	                  to seed audit_periods outside the tenant GUC.
//	DATABASE_URL_APP  application role DSN (NOSUPERUSER NOBYPASSRLS); the
//	                  notes.Store + notifications.Store run against this
//	                  so RLS is enforced.

package notes_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ===== AC-1: scope coverage =====

// TestSlice029_CreateOnEachScope_WithVisibility creates one note per
// scope_type × visibility combination. control + sample + walkthrough +
// finding × {auditor_only, shared}. period scope is covered by slice
// 025's TestNotes_RejectsEmptyBody (period scope still allowed).
func TestSlice029_CreateOnEachScope_WithVisibility(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-1 029",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	cases := []struct {
		scopeType  string
		scopeID    string
		visibility string
	}{
		{"control", "ctl-1", notes.VisibilityAuditorOnly},
		{"control", "ctl-1", notes.VisibilityShared},
		{"sample", "smp-1", notes.VisibilityAuditorOnly},
		{"sample", "smp-1", notes.VisibilityShared},
		{"walkthrough", "wlk-1", notes.VisibilityAuditorOnly},
		{"walkthrough", "wlk-1", notes.VisibilityShared},
		{"finding", "fnd-1", notes.VisibilityAuditorOnly},
		{"finding", "fnd-1", notes.VisibilityShared},
	}
	for _, tc := range cases {
		t.Run(tc.scopeType+"_"+tc.visibility, func(t *testing.T) {
			created, err := store.CreateV2(ctx, notes.CreateInput{
				AuditPeriodID: periodID,
				AuthorUserID:  "auditor-A",
				ScopeType:     tc.scopeType,
				ScopeID:       tc.scopeID,
				Body:          "Note body for " + tc.scopeType + " " + tc.visibility,
				Visibility:    tc.visibility,
			})
			if err != nil {
				t.Fatalf("CreateV2(%s, %s): %v", tc.scopeType, tc.visibility, err)
			}
			if created.Note.Visibility != tc.visibility {
				t.Fatalf("expected visibility %q, got %q", tc.visibility, created.Note.Visibility)
			}
			if created.Note.ScopeType != tc.scopeType {
				t.Fatalf("expected scope_type %q, got %q", tc.scopeType, created.Note.ScopeType)
			}
		})
	}
}

// TestSlice029_RejectsInvalidVisibility: the Store rejects unknown
// visibility before hitting the DB CHECK constraint.
func TestSlice029_RejectsInvalidVisibility(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "bad-visibility",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	_, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		Body:          "needs valid visibility",
		Visibility:    "public", // not in the enum
	})
	if !errors.Is(err, notes.ErrInvalidVisibility) {
		t.Fatalf("expected ErrInvalidVisibility, got %v", err)
	}
}

// TestSlice029_WalkthroughScopeAllowed: new scope_type from slice 029.
func TestSlice029_WalkthroughScopeAllowed(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "walkthrough",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	created, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "walkthrough",
		ScopeID:       "wlk-1",
		Body:          "Walkthrough notes",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("CreateV2(walkthrough): %v", err)
	}
	if created.Note.ScopeType != "walkthrough" {
		t.Fatalf("expected scope_type walkthrough, got %q", created.Note.ScopeType)
	}
}

// ===== AC-2: reply threading + recursive thread retrieval =====

// TestSlice029_RepliesThreadToParent: a root note + 2 replies are
// retrievable as a 3-row thread in tree order.
func TestSlice029_RepliesThreadToParent(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-2 thread",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	root, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-CC6.1",
		Body:          "Question on access reviews?",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create root: %v", err)
	}

	reply1, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "grc-user",
		ScopeType:     "control",
		ScopeID:       "ctl-CC6.1",
		Body:          "Attached the quarterly review evidence.",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &root.Note.ID,
	})
	if err != nil {
		t.Fatalf("Create reply1: %v", err)
	}
	if reply1.Note.ParentNoteID == nil || *reply1.Note.ParentNoteID != root.Note.ID {
		t.Fatalf("expected reply1.ParentNoteID = root.ID")
	}

	_, err = store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-CC6.1",
		Body:          "Thanks, this resolves the question.",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &reply1.Note.ID,
	})
	if err != nil {
		t.Fatalf("Create reply2: %v", err)
	}

	thread, err := store.ListThreadForScope(ctx, periodID, "control", "ctl-CC6.1", "auditor-A")
	if err != nil {
		t.Fatalf("ListThreadForScope: %v", err)
	}
	if len(thread) != 3 {
		t.Fatalf("expected 3 notes in thread, got %d", len(thread))
	}
	// Tree order: root (depth 0) -> reply1 (depth 1) -> reply2 (depth 2).
	if thread[0].Depth != 0 || thread[0].ID != root.Note.ID {
		t.Fatalf("expected thread[0] = root @ depth 0; got %v @ depth %d", thread[0].ID, thread[0].Depth)
	}
	if thread[1].Depth != 1 || thread[1].ID != reply1.Note.ID {
		t.Fatalf("expected thread[1] = reply1 @ depth 1; got %v @ depth %d", thread[1].ID, thread[1].Depth)
	}
	if thread[2].Depth != 2 {
		t.Fatalf("expected thread[2] @ depth 2, got %d", thread[2].Depth)
	}
}

// TestSlice029_ReplyToWrongScopeRejected: parent_note_id pointing to
// a note in a different scope_id is rejected at create time.
func TestSlice029_ReplyToWrongScopeRejected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-2 mismatch",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	root, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Note on ctl-1",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create root: %v", err)
	}

	// Reply with a DIFFERENT scope_id -- must reject.
	_, err = store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-2", // mismatch
		Body:          "Reply targeting wrong scope",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &root.Note.ID,
	})
	if !errors.Is(err, notes.ErrParentMismatch) {
		t.Fatalf("expected ErrParentMismatch, got %v", err)
	}
}

// ===== AC-3: in-app notification dispatch =====

// TestSlice029_NotificationDispatchedOnReply: a shared reply triggers
// an in-app notification on the parent author. The dispatch payload
// includes the new note's id + scope.
func TestSlice029_NotificationDispatchedOnReply(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-3 notif",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	notifStore := notifications.NewStore(app)
	ctx := ctxFor(t, tenant)

	root, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "finding",
		ScopeID:       "fnd-1",
		Body:          "Finding to discuss",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create root: %v", err)
	}

	// Auditee replies; CreateV2 should compute "auditor-A" as recipient.
	reply, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "grc-user",
		ScopeType:     "finding",
		ScopeID:       "fnd-1",
		Body:          "Response from auditee",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &root.Note.ID,
	})
	if err != nil {
		t.Fatalf("Create reply: %v", err)
	}

	if len(reply.NotifyRecipients) != 1 || reply.NotifyRecipients[0] != "auditor-A" {
		t.Fatalf("expected recipients = [auditor-A], got %v", reply.NotifyRecipients)
	}

	// Dispatch the notification and verify it lands in the recipient's
	// notifications list.
	dispatched, err := notifStore.DispatchAuditNoteReply(ctx, reply.NotifyRecipients, notifications.AuditNotePayload{
		AuditNoteID:   reply.Note.ID.String(),
		AuditPeriodID: periodID.String(),
		ScopeType:     "finding",
		ScopeID:       "fnd-1",
		AuthorUserID:  "grc-user",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatched notification, got %d", len(dispatched))
	}

	list, unread, err := notifStore.ListForRecipient(ctx, "auditor-A", 50, 0)
	if err != nil {
		t.Fatalf("ListForRecipient: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 notification for auditor-A, got %d", len(list))
	}
	if unread != 1 {
		t.Fatalf("expected 1 unread, got %d", unread)
	}
	if list[0].Type != notifications.TypeAuditNoteReply {
		t.Fatalf("expected type %q, got %q", notifications.TypeAuditNoteReply, list[0].Type)
	}
	if list[0].ReadAt != nil {
		t.Fatalf("expected unread (ReadAt nil), got %v", list[0].ReadAt)
	}

	// Mark-read flips ReadAt to non-nil.
	marked, err := notifStore.MarkRead(ctx, list[0].ID, "auditor-A")
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if marked.ReadAt == nil {
		t.Fatalf("expected ReadAt non-nil after MarkRead")
	}

	// Verify unread count drops to zero.
	_, unread, err = notifStore.ListForRecipient(ctx, "auditor-A", 50, 0)
	if err != nil {
		t.Fatalf("ListForRecipient post-mark: %v", err)
	}
	if unread != 0 {
		t.Fatalf("expected 0 unread after MarkRead, got %d", unread)
	}
}

// TestSlice029_NotificationNotDispatchedForSelfReply: replying to your
// own thread does not trigger a notification on yourself.
func TestSlice029_NotificationNotDispatchedForSelfReply(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-3 self",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	root, _ := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Initial note",
		Visibility:    notes.VisibilityShared,
	})
	reply, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Self-reply",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &root.Note.ID,
	})
	if err != nil {
		t.Fatalf("Self-reply: %v", err)
	}
	if len(reply.NotifyRecipients) != 0 {
		t.Fatalf("expected no recipients on self-reply, got %v", reply.NotifyRecipients)
	}
}

// ===== AC-4: visibility respected (auditor_only invisible to non-authors) =====

// TestSlice029_VisibilityRespected: auditee cannot see auditor_only
// rows in a thread; auditee CAN see shared rows. Auditor sees both.
func TestSlice029_VisibilityRespected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-4 visibility",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Auditor writes one auditor_only note and one shared note in the
	// same scope.
	_, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Private auditor working note.",
		Visibility:    notes.VisibilityAuditorOnly,
	})
	if err != nil {
		t.Fatalf("Create private: %v", err)
	}
	_, err = store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Shared question to the auditee.",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create shared: %v", err)
	}

	// Auditor sees both.
	auditorView, err := store.ListThreadForScope(ctx, periodID, "control", "ctl-1", "auditor-A")
	if err != nil {
		t.Fatalf("ListThreadForScope(auditor): %v", err)
	}
	if len(auditorView) != 2 {
		t.Fatalf("expected auditor sees 2 notes, got %d", len(auditorView))
	}

	// Auditee (grc-user) sees only the shared one.
	auditeeView, err := store.ListThreadForScope(ctx, periodID, "control", "ctl-1", "grc-user")
	if err != nil {
		t.Fatalf("ListThreadForScope(auditee): %v", err)
	}
	if len(auditeeView) != 1 {
		t.Fatalf("expected auditee sees 1 note, got %d", len(auditeeView))
	}
	if auditeeView[0].Visibility != notes.VisibilityShared {
		t.Fatalf("expected auditee sees shared note, got %q", auditeeView[0].Visibility)
	}
}

// TestSlice029_AuditeeCannotReplyToPrivateNote: AC-4 / P0-2 -- if the
// parent is auditor_only and the caller is not its author, the parent
// is "invisible" and CreateV2 rejects the reply with ErrNotFound.
func TestSlice029_AuditeeCannotReplyToPrivateNote(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "P0-2",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	priv, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Auditor private note",
		Visibility:    notes.VisibilityAuditorOnly,
	})
	if err != nil {
		t.Fatalf("Create private: %v", err)
	}

	// Auditee tries to reply to the private note -- must fail.
	_, err = store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "grc-user",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Reply attempt to private note",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &priv.Note.ID,
	})
	if !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("expected ErrNotFound (auditor_only invisible to auditee), got %v", err)
	}
}

// ===== AC-6: immutability / append-only =====

// TestSlice029_AppendOnlyAtDBLayer: a direct UPDATE on audit_notes is
// rejected under RLS because the slice-029 migration dropped the
// tenant_update policy. atlas_app has no UPDATE policy and was
// REVOKED UPDATE privilege; the row stays immutable.
func TestSlice029_AppendOnlyAtDBLayer(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-6 append",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	root, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Original",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Set tenant GUC on the app connection then attempt UPDATE.
	// Use the canonical tenancy.ApplyTenant(ctx, tx) pattern -- SET LOCAL
	// must run inside a transaction or the `is_local` flag is silently
	// inert (see internal/tenancy/apply.go), and bare SET LOCAL does not
	// accept bind parameters (SQLSTATE 42601). The same idiom is used by
	// internal/exception/integration_test.go TestAuditLog_IsAppendOnly.
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	res, err := tx.Exec(ctx, `UPDATE audit_notes SET body = 'Tampered' WHERE id = $1`, root.Note.ID)
	if err == nil {
		// If the error is nil, the only acceptable outcome is zero
		// rows affected (RLS silently filtered out the row). Both
		// outcomes prove the append-only property.
		if rows := res.RowsAffected(); rows != 0 {
			t.Fatalf("expected UPDATE rejected by RLS; got %d rows affected", rows)
		}
		return
	}
	// Postgres may also reject with a privilege error (REVOKE UPDATE)
	// or a "permission denied" message; either is acceptable. The key
	// invariant is that the body did NOT change.
	t.Logf("UPDATE rejected as expected: %v", err)

	// Confirm body unchanged.
	got, err := store.GetForReader(ctx, root.Note.ID, "auditor-A")
	if err != nil {
		t.Fatalf("Get after failed UPDATE: %v", err)
	}
	if got.Body != "Original" {
		t.Fatalf("expected body unchanged 'Original', got %q", got.Body)
	}
}

// TestSlice029_DeleteRejectedAtDBLayer: same append-only property for
// DELETE. atlas_app has no DELETE policy + REVOKEd DELETE privilege.
func TestSlice029_DeleteRejectedAtDBLayer(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-6 delete",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	root, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "finding",
		Body:          "Finding note",
		Visibility:    notes.VisibilityShared,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Canonical tenancy.ApplyTenant(ctx, tx) pattern (see the
	// TestSlice029_AppendOnlyAtDBLayer note above).
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	res, err := tx.Exec(ctx, `DELETE FROM audit_notes WHERE id = $1`, root.Note.ID)
	if err == nil {
		if rows := res.RowsAffected(); rows != 0 {
			t.Fatalf("expected DELETE rejected by RLS; got %d rows affected", rows)
		}
		return
	}
	t.Logf("DELETE rejected as expected: %v", err)

	// Row should still exist.
	if _, err := store.GetForReader(ctx, root.Note.ID, "auditor-A"); err != nil {
		t.Fatalf("Get after failed DELETE: %v (row should still exist)", err)
	}
}

// ===== AC-5: OSCAL data-shape preservation (verified by query existence) =====

// TestSlice029_ListThreadForScope_ProducesOSCALReadShape: a sanity
// check that the slice-030 OSCAL exporter contract holds -- every
// field needed for OSCAL `observation` mapping is exposed on the
// thread row. This test will fail loudly if a future refactor drops
// a field that slice 030 depends on.
func TestSlice029_ListThreadForScope_ProducesOSCALReadShape(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	periodID := seedPeriod(t, admin, tenant, fwID, "AC-5 OSCAL",
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))

	store := notes.NewStore(app)
	ctx := ctxFor(t, tenant)
	root, _ := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "auditor-A",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Root note",
		Visibility:    notes.VisibilityShared,
	})
	_, err := store.CreateV2(ctx, notes.CreateInput{
		AuditPeriodID: periodID,
		AuthorUserID:  "grc-user",
		ScopeType:     "control",
		ScopeID:       "ctl-1",
		Body:          "Reply",
		Visibility:    notes.VisibilityShared,
		ParentNoteID:  &root.Note.ID,
	})
	if err != nil {
		t.Fatalf("Create reply: %v", err)
	}

	thread, err := store.ListThreadForScope(ctx, periodID, "control", "ctl-1", "auditor-A")
	if err != nil {
		t.Fatalf("ListThreadForScope: %v", err)
	}
	if len(thread) != 2 {
		t.Fatalf("expected 2 thread rows, got %d", len(thread))
	}
	for _, n := range thread {
		// observation.uuid
		if n.ID == uuid.Nil {
			t.Fatalf("missing ID")
		}
		// observation.description
		if n.Body == "" {
			t.Fatalf("missing Body")
		}
		// observation.collected
		if n.AuthorUserID == "" {
			t.Fatalf("missing AuthorUserID")
		}
		// observation.subject (object-reference)
		if n.ScopeType == "" {
			t.Fatalf("missing ScopeType")
		}
		// observation.props ns="security-atlas"
		if n.Visibility == "" {
			t.Fatalf("missing Visibility")
		}
		// CreatedAt must be set (observation.collected timestamp).
		if n.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt is zero")
		}
	}
	// observation.related-observations: reply must have parent set.
	if thread[1].ParentNoteID == nil {
		t.Fatalf("reply missing ParentNoteID")
	}
}

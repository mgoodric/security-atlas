package backup

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// NotificationType is the notifications.type value for a backup/verify
// failure alert (AC-6). Stable so a UI filter / the slice-445 digest can
// recognize it.
const NotificationType = "backup.failure"

// NewNotificationAlerter returns an alert function (D9 / AC-6) that writes an
// in-app notification on a backup or restore-verification FAILURE. The
// notification composes with slice 445: the email channel delivers any unread
// notification the recipient has opted in for, so a failure becomes a loud,
// out-of-band alert without this package knowing about SMTP.
//
// A backup failure is a DEPLOYMENT-level event with no natural tenant. The
// alert is written under the operator-configured alert tenant + recipient
// (the deployment's primary admin) so it lands in a real inbox. Wiring runs as
// the migrator pool (BYPASSRLS) but still applies the tenant GUC so the
// tenant-scoped notifications RLS WITH CHECK is satisfied (the row is honestly
// tenant-attributed to the admin's tenant).
//
// If tenantID or recipient is empty, the alerter logs only — a deployment that
// has not configured an alert recipient still gets the failure in the
// backup_runs status row + the server log; it just does not get an in-app
// notification. Best-effort: an alert write failure never fails the backup
// (the artifact is already durable / the failure is already recorded).
func NewNotificationAlerter(pool *pgxpool.Pool, tenantID, recipient string, logger *slog.Logger) func(context.Context, string) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return func(ctx context.Context, summary string) {
		if tenantID == "" || recipient == "" {
			logger.Warn("backup alert recipient unconfigured; skipping in-app notification",
				"summary", summary)
			return
		}
		tctx, err := tenancy.WithTenant(ctx, tenantID)
		if err != nil {
			logger.Error("backup alert: tenant ctx", "err", err.Error())
			return
		}
		tid, err := uuid.Parse(tenantID)
		if err != nil {
			logger.Error("backup alert: parse tenant", "err", err.Error())
			return
		}
		payload, _ := json.Marshal(map[string]string{"summary": boundDetail(summary)})

		tx, err := pool.Begin(tctx)
		if err != nil {
			logger.Error("backup alert: begin tx", "err", err.Error())
			return
		}
		defer func() { _ = tx.Rollback(tctx) }()
		if err := tenancy.ApplyTenant(tctx, tx); err != nil {
			logger.Error("backup alert: apply tenant", "err", err.Error())
			return
		}
		if _, err := dbx.New(tx).CreateNotification(tctx, dbx.CreateNotificationParams{
			ID:              pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:        pgtype.UUID{Bytes: tid, Valid: true},
			RecipientUserID: recipient,
			Type:            NotificationType,
			Payload:         payload,
		}); err != nil {
			logger.Error("backup alert: create notification", "err", err.Error())
			return
		}
		if err := tx.Commit(tctx); err != nil {
			logger.Error("backup alert: commit", "err", err.Error())
			return
		}
		logger.Info("backup failure notification raised", "recipient", recipient, "summary", summary)
	}
}

// AlertConfigFromEnv reads the alert tenant + recipient from the environment.
// ATLAS_BACKUP_ALERT_TENANT defaults to ATLAS_BOOTSTRAP_TENANT so a standard
// self-host deployment alerts the bootstrap tenant's admin without extra
// config; ATLAS_BACKUP_ALERT_RECIPIENT is the slice-029 string user-id of the
// recipient (typically the deployment admin).
func AlertConfigFromEnv(lookup func(string) (string, bool)) (tenantID, recipient string) {
	if v, ok := lookup("ATLAS_BACKUP_ALERT_TENANT"); ok && v != "" {
		tenantID = v
	} else if v, ok := lookup("ATLAS_BOOTSTRAP_TENANT"); ok {
		tenantID = v
	}
	if v, ok := lookup("ATLAS_BACKUP_ALERT_RECIPIENT"); ok {
		recipient = v
	}
	return tenantID, recipient
}

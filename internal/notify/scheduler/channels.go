package scheduler

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/notify/slack"
	"github.com/mgoodric/security-atlas/internal/notify/webhook"
)

// This file binds the three concrete slice-445/543 channels to the driver's
// two seams (OptInLister + DigestDeliverer). The driver itself imports none
// of the channel result types — each adapter flattens the channel's own
// DeliveryResult{Sent,Skipped,Reason} into the shared Delivery{Sent,Skipped}.
// The enumeration query is the channel's ListXOptInUsers sqlc query, run from
// the migrator pool (the cross-tenant read of (tenant, user) keys only).

// EmailChannel adapts the slice-445 email channel.
func EmailChannel(c *email.Channel) Channel {
	return Channel{
		Name:      "email",
		List:      listEmailOptIns,
		Deliverer: emailDeliverer{c},
	}
}

// SlackChannel adapts the slice-543 Slack channel.
func SlackChannel(c *slack.Channel) Channel {
	return Channel{
		Name:      "slack",
		List:      listSlackOptIns,
		Deliverer: slackDeliverer{c},
	}
}

// WebhookChannel adapts the slice-543 webhook channel.
func WebhookChannel(c *webhook.Channel) Channel {
	return Channel{
		Name:      "webhook",
		List:      listWebhookOptIns,
		Deliverer: webhookDeliverer{c},
	}
}

// ---- enumeration adapters (migrator-pool queries -> []OptIn) ----

func listEmailOptIns(ctx context.Context, q *dbx.Queries) ([]OptIn, error) {
	rows, err := q.ListEmailOptInUsers(ctx)
	if err != nil {
		return nil, err
	}
	return listOptInRows(rows, func(r dbx.ListEmailOptInUsersRow) (pgtype.UUID, pgtype.UUID) {
		return r.TenantID, r.UserID
	}), nil
}

func listSlackOptIns(ctx context.Context, q *dbx.Queries) ([]OptIn, error) {
	rows, err := q.ListSlackOptInUsers(ctx)
	if err != nil {
		return nil, err
	}
	return listOptInRows(rows, func(r dbx.ListSlackOptInUsersRow) (pgtype.UUID, pgtype.UUID) {
		return r.TenantID, r.UserID
	}), nil
}

func listWebhookOptIns(ctx context.Context, q *dbx.Queries) ([]OptIn, error) {
	rows, err := q.ListWebhookOptInUsers(ctx)
	if err != nil {
		return nil, err
	}
	return listOptInRows(rows, func(r dbx.ListWebhookOptInUsersRow) (pgtype.UUID, pgtype.UUID) {
		return r.TenantID, r.UserID
	}), nil
}

// ---- delivery adapters (channel DeliveryResult -> Delivery) ----

type emailDeliverer struct{ c *email.Channel }

func (d emailDeliverer) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (Delivery, error) {
	res, err := d.c.DeliverDigest(ctx, userID, recipientUserID)
	return Delivery{Sent: res.Sent, Skipped: res.Skipped}, err
}

type slackDeliverer struct{ c *slack.Channel }

func (d slackDeliverer) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (Delivery, error) {
	res, err := d.c.DeliverDigest(ctx, userID, recipientUserID)
	return Delivery{Sent: res.Sent, Skipped: res.Skipped}, err
}

type webhookDeliverer struct{ c *webhook.Channel }

func (d webhookDeliverer) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (Delivery, error) {
	res, err := d.c.DeliverDigest(ctx, userID, recipientUserID)
	return Delivery{Sent: res.Sent, Skipped: res.Skipped}, err
}

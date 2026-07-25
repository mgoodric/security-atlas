// Package provision implements the OPT-IN, one-shot Event-Grid provisioning the
// Azure connector's steady-state receiver (slice 522) deliberately does NOT do.
//
// Boundary (slice 658, P0-658-1): the long-lived `eventgrid` receiver is
// READ-ONLY and never holds a write scope. Provisioning is a SEPARATE, opt-in,
// one-shot operator action (`atlas-azure provision` / `deprovision`) run with
// the operator's OWN ELEVATED Azure credentials — distinct from the receiver's
// read-only credential. This package is the ONLY write code path in the
// connector.
//
// What it does (AC-1 / AC-3):
//   - Provision: PUT the Event-Grid system topic + event subscription pointed at
//     the receiver's webhook endpoint (delivery key carried per the receiver's
//     --credential-in), and OPTIONALLY the Activity-Log diagnostic setting that
//     routes Activity-Log events to the system topic. ARM PUT is upsert, so a
//     re-run is idempotent.
//   - Deprovision: DELETE the diagnostic setting, event subscription, and system
//     topic the provision step created. DELETE of an absent resource is a no-op,
//     so teardown is also idempotent.
//
// What it does NOT do (P0-658-2): it never talks to the security-atlas platform.
// Provisioning is connector-side Azure ARM management API only; it does not
// widen the platform-side push wire (invariant #3) and creates no evidence kind.
//
// Consistency note: this package mirrors the connector's existing read-side ARM
// access (internal/storage, internal/aks, ...): a thin raw-HTTP ARM client
// behind an injectable interface, NOT the armeventgrid/armmonitor management
// SDK. That keeps the privileged write surface small, auditable line-by-line,
// and free of a heavy dependency the rest of the connector does not use
// (Article VIII — anti-abstraction). See docs/audit-log/658-*.md decision D2.
package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ARMWriteScope is the OAuth2 resource scope for the Azure Resource Manager
// management plane. The SAME scope string the read side uses; the difference is
// the RBAC role behind the operator's elevated credential, NOT the token scope.
const ARMWriteScope = "https://management.azure.com/.default"

// API is the injectable ARM management surface the provisioner drives. The cmd
// layer wires a live raw-HTTP implementation; tests swap in a fake so the
// provision/deprovision flow is exercised without touching live Azure. Every
// method is an ARM management-plane WRITE/DELETE — the receiver never holds a
// reference to this interface (P0-658-1).
type API interface {
	// PutSystemTopic upserts the Event-Grid system topic. ARM PUT is upsert, so
	// a re-run with the same args is a no-op success (idempotent).
	PutSystemTopic(ctx context.Context, t SystemTopic) error
	// PutEventSubscription upserts the event subscription routing the system
	// topic to the receiver webhook (webhook URL + delivery key).
	PutEventSubscription(ctx context.Context, s EventSubscription) error
	// PutDiagnosticSetting upserts the Activity-Log diagnostic setting that
	// routes subscription Activity-Log events to the system topic. Optional.
	PutDiagnosticSetting(ctx context.Context, d DiagnosticSetting) error

	// DeleteEventSubscription removes the event subscription. DELETE of an
	// absent resource is a no-op (idempotent teardown).
	DeleteEventSubscription(ctx context.Context, s EventSubscription) error
	// DeleteSystemTopic removes the system topic.
	DeleteSystemTopic(ctx context.Context, t SystemTopic) error
	// DeleteDiagnosticSetting removes the Activity-Log diagnostic setting.
	DeleteDiagnosticSetting(ctx context.Context, d DiagnosticSetting) error
}

// SystemTopic identifies the Event-Grid system topic to upsert/delete. The
// topic source is the in-scope subscription (Activity-Log events flow through
// the subscription-level system topic).
type SystemTopic struct {
	SubscriptionID string
	ResourceGroup  string
	Name           string
	Location       string
}

// EventSubscription identifies the event subscription routing the system topic
// to the receiver webhook. WebhookURL is the receiver's public HTTPS endpoint +
// its --path. DeliveryKey is the per-delivery credential the receiver verifies;
// it is carried to ARM in the subscription's delivery config and is NEVER
// logged.
type EventSubscription struct {
	SubscriptionID string
	ResourceGroup  string
	SystemTopic    string
	Name           string
	WebhookURL     string
	// DeliveryKeyHeader / DeliveryKeyQueryParam carry where the receiver expects
	// the key (header vs query); exactly one is set, mirroring the receiver's
	// --credential-in.
	DeliveryKeyHeader     string
	DeliveryKeyQueryParam string
	DeliveryKey           string
}

// DiagnosticSetting identifies the Activity-Log diagnostic setting that routes
// subscription Activity-Log events to the system topic's Event-Grid endpoint.
type DiagnosticSetting struct {
	SubscriptionID  string
	Name            string
	SystemTopicID   string // full ARM id of the Event-Grid system topic destination
	ActivityLogCats []string
}

// Plan is the operator's provision request. The cmd layer builds it from flags;
// this package contains no flag parsing (cobra stays in cmd).
type Plan struct {
	Topic        SystemTopic
	Subscription EventSubscription
	// Diagnostic is provisioned only when IncludeDiagnostic is true (AC: the
	// Activity-Log diagnostic setting is opt-in within the opt-in command).
	Diagnostic        DiagnosticSetting
	IncludeDiagnostic bool
}

// Validate rejects an incomplete plan before any ARM call so a half-provisioned
// state is not created on a typo.
func (p Plan) Validate() error {
	var missing []string
	if p.Topic.SubscriptionID == "" {
		missing = append(missing, "subscription-id")
	}
	if p.Topic.ResourceGroup == "" {
		missing = append(missing, "resource-group")
	}
	if p.Topic.Name == "" {
		missing = append(missing, "system-topic name")
	}
	if p.Topic.Location == "" {
		missing = append(missing, "location")
	}
	if p.Subscription.Name == "" {
		missing = append(missing, "event-subscription name")
	}
	if p.Subscription.WebhookURL == "" {
		missing = append(missing, "webhook-url")
	}
	if p.Subscription.DeliveryKey == "" {
		missing = append(missing, "delivery key (env)")
	}
	if !strings.HasPrefix(strings.ToLower(p.Subscription.WebhookURL), "https://") {
		// Event Grid requires an HTTPS webhook; refuse to provision a cleartext
		// endpoint that would carry the delivery key.
		missing = append(missing, "webhook-url must be https://")
	}
	if p.IncludeDiagnostic {
		if p.Diagnostic.Name == "" {
			missing = append(missing, "diagnostic-setting name")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("provision: incomplete plan, missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Provision creates the system topic, then the event subscription, then
// (optionally) the Activity-Log diagnostic setting. Order matters: the
// subscription targets the topic, and the diagnostic setting targets the topic,
// so the topic is created first. Every step is an upsert (PUT) so re-running an
// already-provisioned plan succeeds without error (AC-1 idempotency).
func Provision(ctx context.Context, api API, p Plan) error {
	if api == nil {
		return errors.New("provision: nil API")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := api.PutSystemTopic(ctx, p.Topic); err != nil {
		return fmt.Errorf("provision system topic: %w", err)
	}
	if err := api.PutEventSubscription(ctx, p.Subscription); err != nil {
		return fmt.Errorf("provision event subscription: %w", err)
	}
	if p.IncludeDiagnostic {
		if err := api.PutDiagnosticSetting(ctx, p.Diagnostic); err != nil {
			return fmt.Errorf("provision diagnostic setting: %w", err)
		}
	}
	return nil
}

// Deprovision tears down what Provision created, in reverse dependency order:
// diagnostic setting (if requested), then event subscription, then system
// topic. DELETE of an absent resource is a no-op so a partial teardown can be
// re-run safely (AC-3 idempotency).
func Deprovision(ctx context.Context, api API, p Plan) error {
	if api == nil {
		return errors.New("deprovision: nil API")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if p.IncludeDiagnostic {
		if err := api.DeleteDiagnosticSetting(ctx, p.Diagnostic); err != nil {
			return fmt.Errorf("deprovision diagnostic setting: %w", err)
		}
	}
	if err := api.DeleteEventSubscription(ctx, p.Subscription); err != nil {
		return fmt.Errorf("deprovision event subscription: %w", err)
	}
	if err := api.DeleteSystemTopic(ctx, p.Topic); err != nil {
		return fmt.Errorf("deprovision system topic: %w", err)
	}
	return nil
}

// DocumentedRBACActions returns the EXACT Azure RBAC actions the operator must
// grant the ELEVATED credential they run `provision` / `deprovision` with. These
// are OPERATOR-SUPPLIED for the one-shot privileged action, NOT held by the
// long-lived receiver (P0-658-1). The cmd `provision --print-rbac` flag and the
// README both render this list.
func DocumentedRBACActions() []RBACAction {
	return []RBACAction{
		{
			Action: "Microsoft.EventGrid/systemTopics/write",
			Why:    "create / upsert the Event-Grid system topic (provision)",
		},
		{
			Action: "Microsoft.EventGrid/systemTopics/delete",
			Why:    "tear down the system topic (deprovision)",
		},
		{
			Action: "Microsoft.EventGrid/systemTopics/eventSubscriptions/write",
			Why:    "create / upsert the event subscription routing to the receiver webhook (provision)",
		},
		{
			Action: "Microsoft.EventGrid/systemTopics/eventSubscriptions/delete",
			Why:    "tear down the event subscription (deprovision)",
		},
		{
			Action: "Microsoft.Insights/diagnosticSettings/write",
			Why:    "create / upsert the Activity-Log diagnostic setting (provision, optional --with-diagnostic)",
		},
		{
			Action: "Microsoft.Insights/diagnosticSettings/delete",
			Why:    "tear down the Activity-Log diagnostic setting (deprovision, optional)",
		},
	}
}

// RBACAction is one ARM management action the operator's elevated credential
// needs for provisioning. Note every action is a `write` / `delete` — that is
// precisely why it is OPERATOR-SUPPLIED and one-shot, never granted to the
// steady-state read-only receiver.
type RBACAction struct {
	Action string
	Why    string
}

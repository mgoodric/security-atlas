// Package alertrules pulls Grafana alert-rule + notification-policy inventory
// via the read-only Grafana provisioning API (Viewer role).
//
// The load-bearing guard (P0-488-3 / threat-model I): the client decodes ONLY
// each alert rule's uid, title, condition-type, paused state, folder, and the
// NAME of the contact point it routes to (the receiver). It NEVER materializes
// or emits a contact point's settings blob — which is where the secret webhook
// URL / integration token / recipient email address live. The connector reads
// the contact-point NAMES (from the notification-policy tree + the rule's
// receiver field), never their secret settings.
//
// Two source reads are joined:
//   - alert rules (GET /api/v1/provisioning/alert-rules): title, uid, isPaused,
//     folderUID, the rule's receiver name (if set).
//   - contact points (GET /api/v1/provisioning/contact-points): name + type
//     ONLY — the settings field is discarded at the decode boundary so secrets
//     never enter memory as connector data.
package alertrules

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Grafana provisioning API calls; tests pass a fake.
type API interface {
	// ListAlertRules returns the alert rules (secret-free fields only).
	ListAlertRules(ctx context.Context) ([]RawRule, error)
	// ListContactPoints returns contact-point name->kind, no settings.
	ListContactPoints(ctx context.Context) ([]ContactPoint, error)
}

// RawRule is the narrow, secret-free view the Grafana client returns for one
// alert rule. Tests construct it directly.
type RawRule struct {
	UID       string
	Title     string
	RuleType  string // e.g. "grafana" | "datasource"
	Paused    bool
	FolderUID string
	// ReceiverName is the contact point this rule routes to (may be empty if the
	// rule inherits the default notification policy). NAME only — never settings.
	ReceiverName string
}

// ContactPoint is a notification contact point — NAME + KIND only. There is
// intentionally no settings field (P0-488-3): the Grafana settings blob holds
// the secret webhook URL / token / recipient address and is discarded at decode.
type ContactPoint struct {
	Name string
	Kind string // e.g. "slack" | "pagerduty" | "email" | "webhook"
}

// Collect lists alert rules + contact points and returns secret-free RawRules
// ready for alertcfg.Normalize. Each rule's notification target is the NAME of
// its receiver contact point (classified by the contact-point kind when known).
func Collect(ctx context.Context, api API) ([]alertcfg.RawRule, error) {
	if api == nil {
		return nil, errors.New("alertrules: API is nil")
	}
	rules, err := api.ListAlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list grafana alert rules: %w", err)
	}
	contacts, err := api.ListContactPoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("list grafana contact points: %w", err)
	}
	kindByName := make(map[string]string, len(contacts))
	for _, cp := range contacts {
		name := strings.TrimSpace(cp.Name)
		if name == "" {
			continue
		}
		kind := strings.TrimSpace(cp.Kind)
		if kind == "" {
			kind = "contact_point"
		}
		kindByName[name] = kind
	}

	out := make([]alertcfg.RawRule, 0, len(rules))
	for _, r := range rules {
		uid := strings.TrimSpace(r.UID)
		title := strings.TrimSpace(r.Title)
		if uid == "" || title == "" {
			continue
		}
		out = append(out, alertcfg.RawRule{
			ID:      uid,
			Name:    title,
			Type:    normalizeRuleType(r.RuleType),
			Enabled: !r.Paused,
			Folder:  strings.TrimSpace(r.FolderUID),
			Targets: targetFor(r.ReceiverName, kindByName),
		})
	}
	return out, nil
}

// targetFor returns the single notification target for a rule's receiver name.
// The receiver NAME is emitted (never a secret); the kind is looked up from the
// contact-point inventory, defaulting to "contact_point" when unknown.
func targetFor(receiverName string, kindByName map[string]string) []alertcfg.Target {
	name := strings.TrimSpace(receiverName)
	if name == "" {
		return nil
	}
	kind := kindByName[name]
	if kind == "" {
		kind = "contact_point"
	}
	return []alertcfg.Target{{Kind: kind, Name: name}}
}

func normalizeRuleType(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return "grafana"
	}
	return t
}

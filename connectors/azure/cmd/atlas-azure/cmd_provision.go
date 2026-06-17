package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/eventgrid"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/provision"
)

// ELEVATED provisioning credential env vars — DELIBERATELY DISTINCT from the
// receiver's read-only AZURE_TENANT_ID / AZURE_CLIENT_ID / AZURE_CLIENT_SECRET
// (P0-658-1). The operator supplies a SEPARATE, short-lived credential carrying
// an elevated write role ONLY when running the one-shot provision/deprovision
// command. The steady-state receiver never sees these and never holds a write
// scope.
const (
	provisionTenantEnv = "AZURE_PROVISION_TENANT_ID"
	provisionClientEnv = "AZURE_PROVISION_CLIENT_ID"
	provisionSecretEnv = "AZURE_PROVISION_CLIENT_SECRET"
)

// Provision seams: the subcommands reach through these so tests swap in a fake
// ARM management API + a no-op token acquisition without touching live Azure.
// newProvisionAPI is the ONLY constructor of the write-capable ARM client, and
// it is reachable ONLY from the provision/deprovision subcommands — never from
// the receiver path (P0-658-1).
var (
	provisionAcquireToken = func(ctx context.Context, cred azureauth.Credential, hc *http.Client, scope string) (string, error) {
		return cred.AcquireToken(ctx, hc, scope)
	}
	newProvisionAPI = func(hc *http.Client, baseURL, token string) provision.API {
		return provision.NewClient(hc, baseURL, token)
	}
	// runProvision / runDeprovision are seamed so the cobra RunE wiring is
	// testable end-to-end with a fake API.
	runProvision   = provision.Provision
	runDeprovision = provision.Deprovision
)

type provisionFlags struct {
	subscriptionID   string
	resourceGroup    string
	location         string
	systemTopic      string
	subscriptionName string
	webhookHost      string // operator's public HTTPS host, e.g. https://atlas.example.com
	path             string // receiver --path, default mirrors the receiver default
	credentialIn     string // header | query — mirrors the receiver's --credential-in
	credentialName   string // header / query-param name carrying the delivery key
	withDiagnostic   bool
	diagnosticName   string
	activityLogCats  []string
	teardown         bool
	printRBAC        bool
}

func newProvisionCmd() *cobra.Command {
	var f provisionFlags
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "[PRIVILEGED, opt-in] create the Event-Grid system topic + event subscription routing to the receiver (run with ELEVATED creds)",
		Long: `[PRIVILEGED, OPT-IN, ONE-SHOT] Provision the Azure Event-Grid plumbing the
steady-state 'eventgrid' receiver needs but DELIBERATELY does not create itself.

This is the ONLY write code path in the connector. It is a SEPARATE, one-shot
operator action — NOT part of the long-lived receiver. The steady-state
'eventgrid' receiver stays READ-ONLY and NEVER holds a write scope (slice 658
P0-658-1). Run this once, with your OWN elevated Azure credentials, to wire up
(or tear down with --teardown) the Event-Grid system topic + event subscription
that routes Azure change events to the receiver's webhook, plus optionally the
Activity-Log diagnostic setting.

ELEVATED, SEPARATE credential (NOT the receiver's read-only credential):
  ` + provisionTenantEnv + `
  ` + provisionClientEnv + `
  ` + provisionSecretEnv + `
These are DISTINCT from the receiver's AZURE_TENANT_ID / AZURE_CLIENT_ID /
AZURE_CLIENT_SECRET on purpose. Supply a short-lived credential carrying the
write role for THIS one-shot run, then revoke it. Run 'atlas-azure provision
--print-rbac' to see the exact RBAC actions the elevated credential needs.

Delivery key: set ` + deliveryKeyEnv + ` to the SAME delivery key the receiver
verifies; it is written into the event subscription's delivery config (as a
secret attribute) and is never logged.

What it talks to: Azure's ARM management API ONLY. Provisioning does NOT touch
the security-atlas platform and does NOT widen the platform push wire
(invariant #3 / P0-658-2).

Idempotent: ARM PUT is upsert, so re-running an already-provisioned plan
succeeds. --teardown DELETEs what provision created (DELETE of an absent
resource is a no-op, so teardown is also safe to re-run).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if f.printRBAC {
				printProvisionRBAC(os.Stdout)
				return nil
			}
			return doProvision(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.subscriptionID, "subscription-id", "", "Azure subscription id to provision in [required]")
	cmd.Flags().StringVar(&f.resourceGroup, "resource-group", "", "resource group for the Event-Grid system topic [required]")
	cmd.Flags().StringVar(&f.location, "location", "", "Azure region for the system topic (e.g. eastus) [required]")
	cmd.Flags().StringVar(&f.systemTopic, "system-topic", "atlas-azure-activitylog", "Event-Grid system topic name")
	cmd.Flags().StringVar(&f.subscriptionName, "event-subscription", "atlas-azure-receiver", "Event Grid event-subscription name")
	cmd.Flags().StringVar(&f.webhookHost, "webhook-host", "", "operator's public HTTPS base URL for the receiver, e.g. https://atlas.example.com [required]")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/azure/eventgrid", "receiver URL path (must match the receiver's --path)")
	cmd.Flags().StringVar(&f.credentialIn, "credential-in", "header", "where the receiver expects the delivery key: header | query (must match the receiver)")
	cmd.Flags().StringVar(&f.credentialName, "credential-name", "Authorization", "header / query-param name carrying the delivery key (must match the receiver)")
	cmd.Flags().BoolVar(&f.withDiagnostic, "with-diagnostic", false, "also provision the Activity-Log diagnostic setting routing Activity-Log events to the system topic")
	cmd.Flags().StringVar(&f.diagnosticName, "diagnostic-name", "atlas-azure-activitylog", "Activity-Log diagnostic-setting name (with --with-diagnostic)")
	cmd.Flags().StringSliceVar(&f.activityLogCats, "activity-log-categories", []string{"Administrative", "Security", "Policy"}, "Activity-Log categories to route (with --with-diagnostic)")
	cmd.Flags().BoolVar(&f.teardown, "teardown", false, "tear down (DELETE) the resources provision created instead of creating them")
	cmd.Flags().BoolVar(&f.printRBAC, "print-rbac", false, "print the exact elevated Azure RBAC actions provisioning requires, then exit")
	return cmd
}

// newDeprovisionCmd is a thin alias for `provision --teardown` so operators get
// an explicit, discoverable teardown verb (AC-3).
func newDeprovisionCmd() *cobra.Command {
	var f provisionFlags
	cmd := &cobra.Command{
		Use:           "deprovision",
		Short:         "[PRIVILEGED, opt-in] tear down the Event-Grid system topic + event subscription provision created (run with ELEVATED creds)",
		Long:          "Tear down (DELETE) the Event-Grid system topic + event subscription (and, with --with-diagnostic, the Activity-Log diagnostic setting) that 'provision' created. Run with the SAME elevated, separate credential as 'provision' (" + provisionTenantEnv + " / " + provisionClientEnv + " / " + provisionSecretEnv + "). Idempotent: deleting an absent resource is a no-op. The steady-state receiver is unaffected and stays read-only.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			f.teardown = true
			return doProvision(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.subscriptionID, "subscription-id", "", "Azure subscription id [required]")
	cmd.Flags().StringVar(&f.resourceGroup, "resource-group", "", "resource group of the Event-Grid system topic [required]")
	cmd.Flags().StringVar(&f.location, "location", "", "Azure region of the system topic [required]")
	cmd.Flags().StringVar(&f.systemTopic, "system-topic", "atlas-azure-activitylog", "Event-Grid system topic name")
	cmd.Flags().StringVar(&f.subscriptionName, "event-subscription", "atlas-azure-receiver", "Event Grid event-subscription name")
	cmd.Flags().StringVar(&f.webhookHost, "webhook-host", "", "operator's public HTTPS base URL for the receiver [required for plan validation]")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/azure/eventgrid", "receiver URL path")
	cmd.Flags().StringVar(&f.credentialIn, "credential-in", "header", "where the receiver expects the delivery key: header | query")
	cmd.Flags().StringVar(&f.credentialName, "credential-name", "Authorization", "header / query-param name carrying the delivery key")
	cmd.Flags().BoolVar(&f.withDiagnostic, "with-diagnostic", false, "also tear down the Activity-Log diagnostic setting")
	cmd.Flags().StringVar(&f.diagnosticName, "diagnostic-name", "atlas-azure-activitylog", "Activity-Log diagnostic-setting name (with --with-diagnostic)")
	return cmd
}

// resolveProvisionCredential resolves the ELEVATED provisioning credential from
// its OWN env vars — NEVER the receiver's read-only AZURE_* vars (P0-658-1). The
// separation is real: this maps the AZURE_PROVISION_* vars onto azureauth.Resolve
// EXPLICITLY so a receiver credential present in AZURE_* is never picked up.
func resolveProvisionCredential() (azureauth.Credential, error) {
	tenant := strings.TrimSpace(os.Getenv(provisionTenantEnv))
	client := strings.TrimSpace(os.Getenv(provisionClientEnv))
	secret := strings.TrimSpace(os.Getenv(provisionSecretEnv))
	if tenant == "" || client == "" || secret == "" {
		return azureauth.Credential{}, fmt.Errorf(
			"provision: elevated credential required — set %s / %s / %s (a SEPARATE, short-lived elevated credential, NOT the receiver's read-only AZURE_* vars)",
			provisionTenantEnv, provisionClientEnv, provisionSecretEnv)
	}
	return azureauth.Resolve(azureauth.ResolveOpts{
		Mode:         azureauth.ModeClientCredentials,
		TenantID:     tenant,
		ClientID:     client,
		ClientSecret: secret,
	})
}

func doProvision(ctx context.Context, f provisionFlags) error {
	location, err := parseCredentialLocation(f.credentialIn)
	if err != nil {
		return err
	}
	if f.subscriptionID == "" {
		return errors.New("--subscription-id is required")
	}
	if f.resourceGroup == "" {
		return errors.New("--resource-group is required")
	}
	if f.location == "" {
		return errors.New("--location is required")
	}

	deliveryKey := os.Getenv(deliveryKeyEnv)
	if deliveryKey == "" {
		return fmt.Errorf("%s is required (the delivery key the receiver verifies; provisioning writes it into the event subscription)", deliveryKeyEnv)
	}

	cred, err := resolveProvisionCredential()
	if err != nil {
		return err
	}

	hc := &http.Client{Timeout: 30 * time.Second}
	token, err := provisionAcquireToken(ctx, cred, hc, provision.ARMWriteScope)
	if err != nil {
		return fmt.Errorf("acquire elevated token: %w", err)
	}
	api := newProvisionAPI(hc, "", token)

	plan := buildPlan(f, location, deliveryKey)

	if f.teardown {
		if err := runDeprovision(ctx, api, plan); err != nil {
			return err
		}
		fmt.Printf("azure provision: torn down system topic %q + event subscription %q (diagnostic=%t) in subscription %s\n",
			f.systemTopic, f.subscriptionName, f.withDiagnostic, f.subscriptionID)
		return nil
	}
	if err := runProvision(ctx, api, plan); err != nil {
		return err
	}
	fmt.Printf("azure provision: provisioned system topic %q + event subscription %q (diagnostic=%t) routing to %s in subscription %s\n",
		f.systemTopic, f.subscriptionName, f.withDiagnostic, plan.Subscription.WebhookURL, f.subscriptionID)
	return nil
}

// buildPlan assembles the provision.Plan from flags. The webhook URL is derived
// from the operator-supplied public host + the receiver --path so the
// subscription targets exactly the endpoint the receiver listens on.
func buildPlan(f provisionFlags, loc eventgrid.CredentialLocation, deliveryKey string) provision.Plan {
	webhookURL := strings.TrimRight(f.webhookHost, "/") + "/" + strings.TrimLeft(f.path, "/")
	sub := provision.EventSubscription{
		SubscriptionID: f.subscriptionID,
		ResourceGroup:  f.resourceGroup,
		SystemTopic:    f.systemTopic,
		Name:           f.subscriptionName,
		WebhookURL:     webhookURL,
		DeliveryKey:    deliveryKey,
	}
	if loc == eventgrid.CredentialQuery {
		sub.DeliveryKeyQueryParam = f.credentialName
	} else {
		sub.DeliveryKeyHeader = f.credentialName
	}

	topic := provision.SystemTopic{
		SubscriptionID: f.subscriptionID,
		ResourceGroup:  f.resourceGroup,
		Name:           f.systemTopic,
		Location:       f.location,
	}
	plan := provision.Plan{Topic: topic, Subscription: sub}
	if f.withDiagnostic {
		plan.IncludeDiagnostic = true
		plan.Diagnostic = provision.DiagnosticSetting{
			SubscriptionID:  f.subscriptionID,
			Name:            f.diagnosticName,
			SystemTopicID:   systemTopicARMID(f),
			ActivityLogCats: f.activityLogCats,
		}
	}
	return plan
}

func systemTopicARMID(f provisionFlags) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventGrid/systemTopics/%s",
		f.subscriptionID, f.resourceGroup, f.systemTopic)
}

func printProvisionRBAC(w *os.File) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ELEVATED RBAC ACTION (operator-supplied, one-shot — NOT held by the receiver)\tWHY")
	for _, a := range provision.DocumentedRBACActions() {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", a.Action, a.Why)
	}
	_ = tw.Flush()
}

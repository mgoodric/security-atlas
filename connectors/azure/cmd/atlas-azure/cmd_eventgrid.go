package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/eventgrid"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/firewall"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/keyvault"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/nsg"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// Event-Grid receiver seams: doEventGrid reaches through these so tests can swap in
// fakes for the sdk client constructor and the blocking Serve loop without binding a
// real socket or hitting a real platform.
var (
	newEventGridSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	eventGridServe = eventgrid.Serve
)

// deliveryKeyEnv is the environment variable carrying the operator-configured
// Event-Grid delivery key (D1). Read from the environment, never a flag (it would
// land in shell history), never logged, never placed into a record.
const deliveryKeyEnv = "AZURE_EVENTGRID_DELIVERY_KEY"

type eventGridFlags struct {
	tenantID        string
	subscriptionID  string
	environment     string
	authMode        string
	clientID        string
	listen          string
	path            string
	credentialIn    string // "header" | "query"
	credentialName  string // header name or query-param name
	entraControl    string
	storageControl  string
	aksControl      string
	nsgControl      string
	keyvaultControl string
	firewallControl string
}

func newEventGridCmd() *cobra.Command {
	var f eventGridFlags
	cmd := &cobra.Command{
		Use:   "eventgrid",
		Short: "run the Azure event-driven (subscribe) Event Grid change-event receiver",
		Long: `Run the source-side Azure Event Grid change-event receiver: a long-lived
HTTP server (inside this connector process) that receives Event Grid change events
for in-scope Azure resources, AUTHENTICATES each delivery's configured delivery key
BEFORE doing any work, then — treating the event as a TRIGGER, never the data —
RE-READS the changed resource via the SAME read-only Graph/ARM path the pull profile
uses and pushes the matching record. So a configuration change to an in-scope
storage account / AKS cluster / NSG / Key Vault / firewall policy / Entra role
assignment refreshes its evidence promptly.

Profile: subscribe (event-driven via Azure Event Grid / Activity-Log diagnostic
settings). Event-driven means Event-Grid delivery latency (typically seconds to a
minute) plus the coalescing window — NOT instantaneous, and NOT "continuous
monitoring". The pull profile (the 'run' subcommand) remains the reconciliation
backstop. The platform-side wire is still push (invariant #3): this receiver is part
of the CONNECTOR, not a new inbound platform API.

Event Grid SubscriptionValidation handshake: when Event Grid validates the webhook
it POSTs an event of type Microsoft.EventGrid.SubscriptionValidationEvent carrying a
validationCode; the receiver responds 200 with {"validationResponse":"<code>"} and
builds NO record. This receiver handles that handshake FIRST.

Security (STRIDE Spoofing, DOMINANT): anyone can POST to a public endpoint, so each
delivery's configured delivery key is verified (constant-time) BEFORE any re-read or
record. A delivery with a missing/forged key is rejected 401 and never triggers a
re-read. The event is a TRIGGER: the record's data comes ENTIRELY from the re-read of
real Azure state, so a forged event at worst causes a redundant read of real config,
never a fabricated record. The body is size-bounded (413) so a hostile POST cannot
exhaust memory; an event storm is bounded by a queue + a coalescing window.

No new Azure permission: the re-read uses the SAME read-only Graph + ARM Reader set
the pull profile uses (plus the operator's Event-Grid subscription read, configured
in Azure). NO write path.

Auth (delivery key): set ` + deliveryKeyEnv + ` to the delivery key you configured on
the Event Grid subscription. It is read from the environment, never a flag, never
logged.

Bind: defaults to loopback (127.0.0.1). Event Grid requires an HTTPS endpoint with a
valid certificate — terminate TLS at a reverse proxy in front of this process.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			if _, err := azureauth.ParseMode(f.authMode); err != nil {
				return err
			}
			if _, err := parseCredentialLocation(f.credentialIn); err != nil {
				return err
			}
			if f.subscriptionID == "" {
				return errors.New("--subscription-id is required (the ARM re-read is subscription-scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doEventGrid(signalContext(), f)
		},
	}
	cmd.Flags().StringVar(&f.tenantID, "tenant-id", "", "Entra tenant id (env: AZURE_TENANT_ID)")
	cmd.Flags().StringVar(&f.subscriptionID, "subscription-id", "", "Azure subscription id for the ARM re-read [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.authMode, "auth-mode", "client-credentials", "auth mode: client-credentials | managed-identity")
	cmd.Flags().StringVar(&f.clientID, "client-id", "", "Entra app-registration client id (env: AZURE_CLIENT_ID)")
	cmd.Flags().StringVar(&f.listen, "listen", "127.0.0.1:8485", "address to bind the receiver (loopback default; terminate TLS at a reverse proxy)")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/azure/eventgrid", "URL path the receiver listens on")
	cmd.Flags().StringVar(&f.credentialIn, "credential-in", "header", "where Event Grid carries the delivery key: header | query")
	cmd.Flags().StringVar(&f.credentialName, "credential-name", "Authorization", "header name (credential-in=header) or query-param name (credential-in=query) carrying the delivery key")
	cmd.Flags().StringVar(&f.entraControl, "entra-control", "scf:IAC-21", "control_id to attach to azure.entra_role_assignment.v1 records")
	cmd.Flags().StringVar(&f.storageControl, "storage-control", "scf:CRY-04", "control_id to attach to azure.storage_account_config.v1 records")
	cmd.Flags().StringVar(&f.aksControl, "aks-control", "scf:CFG-02", "control_id to attach to azure.aks_cluster_config.v1 records")
	cmd.Flags().StringVar(&f.nsgControl, "nsg-control", "scf:NET-04", "control_id to attach to azure.nsg_rules.v1 records")
	cmd.Flags().StringVar(&f.keyvaultControl, "keyvault-control", "scf:CRY-09", "control_id to attach to azure.keyvault_access_config.v1 records")
	cmd.Flags().StringVar(&f.firewallControl, "firewall-control", "scf:NET-04", "control_id to attach to azure.firewall_rules.v1 records")
	return cmd
}

// signalContext returns a context cancelled on SIGINT / SIGTERM so the long-lived
// receiver drains gracefully on the operator's stop signal.
func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// parseCredentialLocation maps the --credential-in flag to the eventgrid enum.
func parseCredentialLocation(s string) (eventgrid.CredentialLocation, error) {
	switch s {
	case "header", "":
		return eventgrid.CredentialHeader, nil
	case "query":
		return eventgrid.CredentialQuery, nil
	default:
		return eventgrid.CredentialHeader, fmt.Errorf("--credential-in must be header or query, got %q", s)
	}
}

func doEventGrid(ctx context.Context, f eventGridFlags) error {
	location, err := parseCredentialLocation(f.credentialIn)
	if err != nil {
		return err
	}
	deliveryKey := os.Getenv(deliveryKeyEnv)
	if deliveryKey == "" {
		return fmt.Errorf("%s is required (the per-delivery Event Grid credential)", deliveryKeyEnv)
	}

	mode, err := azureauth.ParseMode(f.authMode)
	if err != nil {
		return err
	}
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		Mode:     mode,
		TenantID: f.tenantID,
		ClientID: f.clientID,
	})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	sdkClient, err := newEventGridSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	reread := newReread(cred, sdkClient, f)

	rec, err := eventgrid.NewReceiver(eventgrid.Config{
		Verifier: eventgrid.NewDeliveryKeyVerifier(location, f.credentialName, deliveryKey),
		Reread:   reread,
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	// The background worker drains the queue + coalesces same-resource events into
	// one re-read.
	go rec.Run(ctx)

	// The validation-handshake adapter wraps the receiver and OWNS the non-record
	// SubscriptionValidation path BEFORE delegating a real delivery to the shared
	// verify-first skeleton (D2).
	srv := eventgrid.NewServer(f.listen, f.path, eventgrid.ValidationHandler{Inner: rec})
	fmt.Printf("azure eventgrid receiver listening (profile=subscribe addr=%s path=%s environment=%s) — NOT continuous monitoring\n",
		f.listen, f.path, f.environment)
	if err := eventGridServe(ctx, srv); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// newReread builds the eventgrid.Rereader: on an event it acquires a read-only
// token for the routed reader's scope, re-runs the EXISTING reader (UNCHANGED),
// filters the result to the changed resource id, builds the matching kind via the
// EXISTING record builder (UNCHANGED), and pushes it. The record's data comes
// ENTIRELY from this re-read — the event payload is never a data source
// (no-fabrication). The slice-486 idem key in each builder collapses a
// subscribe-emitted and a pull-emitted record for the same resource in the same hour
// to one ledger row (D5).
func newReread(cred azureauth.Credential, sdkClient sdkPushClient, f eventGridFlags) eventgrid.Rereader {
	hc := &http.Client{Timeout: 20 * time.Second}
	return func(ctx context.Context, rt eventgrid.ResourceType, resourceID string) (int, error) {
		switch rt {
		case eventgrid.ResourceStorage:
			return rereadKind(ctx, hc, cred, sdkClient, storage.ARMScope, resourceID,
				func(token string) ([]storage.AccountConfig, error) {
					return storageScan(ctx, newStorageAPI(hc, f.subscriptionID, token), f.subscriptionID, nil)
				},
				func(a storage.AccountConfig) string { return a.AccountID },
				func(a storage.AccountConfig) (*evidencev1.EvidenceRecord, error) {
					return buildStorageRecord(a, f.environment, f.storageControl)
				})
		case eventgrid.ResourceAKS:
			return rereadKind(ctx, hc, cred, sdkClient, aks.ARMScope, resourceID,
				func(token string) ([]aks.ClusterConfig, error) {
					return aksScan(ctx, newAKSAPI(hc, f.subscriptionID, token), f.subscriptionID, nil)
				},
				func(c aks.ClusterConfig) string { return c.ClusterID },
				func(c aks.ClusterConfig) (*evidencev1.EvidenceRecord, error) {
					return buildAKSRecord(c, f.environment, f.aksControl)
				})
		case eventgrid.ResourceNSG:
			return rereadKind(ctx, hc, cred, sdkClient, nsg.ARMScope, resourceID,
				func(token string) ([]nsg.GroupConfig, error) {
					return nsgScan(ctx, newNSGAPI(hc, f.subscriptionID, token), f.subscriptionID, nil)
				},
				func(g nsg.GroupConfig) string { return g.NSGID },
				func(g nsg.GroupConfig) (*evidencev1.EvidenceRecord, error) {
					return buildNSGRecord(g, f.environment, f.nsgControl)
				})
		case eventgrid.ResourceKeyVault:
			return rereadKind(ctx, hc, cred, sdkClient, keyvault.ARMScope, resourceID,
				func(token string) ([]keyvault.VaultConfig, error) {
					return keyvaultScan(ctx, newKeyVaultAPI(hc, f.subscriptionID, token), f.subscriptionID, nil)
				},
				func(v keyvault.VaultConfig) string { return v.VaultID },
				func(v keyvault.VaultConfig) (*evidencev1.EvidenceRecord, error) {
					return buildKeyVaultRecord(v, f.environment, f.keyvaultControl)
				})
		case eventgrid.ResourceFirewall:
			return rereadKind(ctx, hc, cred, sdkClient, firewall.ARMScope, resourceID,
				func(token string) ([]firewall.PolicyConfig, error) {
					return firewallScan(ctx, newFirewallAPI(hc, f.subscriptionID, token), f.subscriptionID, nil)
				},
				func(p firewall.PolicyConfig) string { return p.PolicyID },
				func(p firewall.PolicyConfig) (*evidencev1.EvidenceRecord, error) {
					return buildFirewallRecord(p, f.environment, f.firewallControl)
				})
		case eventgrid.ResourceEntra:
			return rereadKind(ctx, hc, cred, sdkClient, entra.GraphScope, resourceID,
				func(token string) ([]entra.Assignment, error) {
					return entraPull(ctx, newEntraAPI(hc, token), cred.TenantID(), nil)
				},
				func(a entra.Assignment) string { return a.AssignmentID },
				func(a entra.Assignment) (*evidencev1.EvidenceRecord, error) {
					return buildEntraRecord(a, f.environment, f.entraControl)
				})
		default:
			// Unmapped types never reach here (the receiver drops them); fail closed.
			return 0, nil
		}
	}
}

// rereadKind is the generic re-read-one-resource pipeline shared by every resource
// type (D4): acquire a read-only token for scope, run the EXISTING reader (scan),
// filter the result to the changed resource id (idOf), build the matching record via
// the EXISTING builder (build), and push it. The record's data comes ENTIRELY from
// the re-read (scan), NEVER the event payload — a resource id matching nothing
// yields zero records (no-fabrication). Collapsing the six near-identical per-kind
// functions into one generic keeps the over-collection guard + builder byte-
// identical with the pull path and the dedup key derivation in one place.
func rereadKind[T any](
	ctx context.Context,
	hc *http.Client,
	cred azureauth.Credential,
	sdkClient sdkPushClient,
	scope, resourceID string,
	scan func(token string) ([]T, error),
	idOf func(T) string,
	build func(T) (*evidencev1.EvidenceRecord, error),
) (int, error) {
	token, err := acquireToken(ctx, cred, hc, scope)
	if err != nil {
		return 0, fmt.Errorf("acquire token: %w", err)
	}
	items, err := scan(token)
	if err != nil {
		return 0, fmt.Errorf("re-read: %w", err)
	}
	pushed := 0
	for _, item := range items {
		if !eventgrid.SameResourceID(idOf(item), resourceID) {
			continue
		}
		rec, err := build(item)
		if err != nil {
			return pushed, fmt.Errorf("build record: %w", err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return pushed, fmt.Errorf("push record: %w", err)
		}
		pushed++
	}
	return pushed, nil
}

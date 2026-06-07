package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the two Azure reads + the sdk client constructor
// without hitting real Azure endpoints or a real platform endpoint. Production
// code paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	entraPull    = entra.Pull
	storageScan  = storage.Inspect
	newSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// acquireToken is seamed so tests never call the live Entra token endpoint.
	acquireToken = func(ctx context.Context, cred azureauth.Credential, hc *http.Client, scope string) (string, error) {
		return cred.AcquireToken(ctx, hc, scope)
	}
	// newEntraAPI / newStorageAPI build the live read-only HTTP clients; seamed
	// so tests inject fakes.
	newEntraAPI = func(hc *http.Client, token string) entra.API {
		return entra.NewClient(hc, "", token)
	}
	newStorageAPI = func(hc *http.Client, subscriptionID, token string) storage.API {
		return storage.NewClient(hc, "", subscriptionID, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	tenantID       string
	subscriptionID string
	environment    string
	authMode       string
	clientID       string
	entraControl   string
	storageControl string
	skipEntra      bool
	skipStorage    bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Entra ID + Azure Storage and push evidence records",
		Long: `Read Microsoft Entra ID role assignments and Azure Storage account
configuration, transform to evidence records, and push to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Least-privilege Azure permissions (read-only):
  - Microsoft Graph: Directory.Read.All  (gates azure.entra_role_assignment.v1)
  - Microsoft Graph: Application.Read.All (gates azure.entra_role_assignment.v1)
  - Azure Resource Manager: Reader role   (gates azure.storage_account_config.v1)

Auth: set AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET (client-
credentials), or pass --auth-mode managed-identity. The secret never appears in
a log line or an evidence record.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			if _, err := azureauth.ParseMode(f.authMode); err != nil {
				return err
			}
			if !f.skipStorage && f.subscriptionID == "" {
				return errors.New("--subscription-id is required for the storage kind (or pass --skip-storage)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.tenantID, "tenant-id", "", "Entra tenant id (env: AZURE_TENANT_ID)")
	cmd.Flags().StringVar(&f.subscriptionID, "subscription-id", "", "Azure subscription id for the storage kind")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.authMode, "auth-mode", "client-credentials", "auth mode: client-credentials | managed-identity")
	cmd.Flags().StringVar(&f.clientID, "client-id", "", "Entra app-registration client id (env: AZURE_CLIENT_ID)")
	cmd.Flags().StringVar(&f.entraControl, "entra-control", "scf:IAC-21", "control_id to attach to azure.entra_role_assignment.v1 records")
	cmd.Flags().StringVar(&f.storageControl, "storage-control", "scf:CRY-04", "control_id to attach to azure.storage_account_config.v1 records")
	cmd.Flags().BoolVar(&f.skipEntra, "skip-entra", false, "skip azure.entra_role_assignment.v1 pull")
	cmd.Flags().BoolVar(&f.skipStorage, "skip-storage", false, "skip azure.storage_account_config.v1 pull")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	mode, err := azureauth.ParseMode(f.authMode)
	if err != nil {
		return err
	}
	cred, err := azureauth.Resolve(azureauth.ResolveOpts{
		Mode:     mode,
		TenantID: f.tenantID,
		ClientID: f.clientID,
		// ClientSecret is read from env only — never a CLI flag (it would
		// land in shell history).
	})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	pushed := 0

	if !f.skipEntra {
		token, err := acquireToken(ctx, cred, httpClient, entra.GraphScope)
		if err != nil {
			return fmt.Errorf("graph token: %w", err)
		}
		assignments, err := entraPull(ctx, newEntraAPI(httpClient, token), cred.TenantID(), nil)
		if err != nil {
			return fmt.Errorf("entra pull: %w", err)
		}
		for _, a := range assignments {
			rec, err := buildEntraRecord(a, f.environment, f.entraControl)
			if err != nil {
				return fmt.Errorf("build entra record %s: %w", a.AssignmentID, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push entra %s: %w", a.AssignmentID, err)
			}
			pushed++
		}
	}

	if !f.skipStorage {
		token, err := acquireToken(ctx, cred, httpClient, storage.ARMScope)
		if err != nil {
			return fmt.Errorf("arm token: %w", err)
		}
		accounts, err := storageScan(ctx, newStorageAPI(httpClient, f.subscriptionID, token), f.subscriptionID, nil)
		if err != nil {
			return fmt.Errorf("storage inspect: %w", err)
		}
		for _, acct := range accounts {
			rec, err := buildStorageRecord(acct, f.environment, f.storageControl)
			if err != nil {
				return fmt.Errorf("build storage record %s: %w", acct.AccountName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push storage %s: %w", acct.AccountName, err)
			}
			pushed++
		}
	}

	fmt.Printf("pushed %d records (tenant=%s subscription=%s environment=%s)\n",
		pushed, cred.TenantID(), f.subscriptionID, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}

func buildEntraRecord(a entra.Assignment, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := a.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"assignment_id":      a.AssignmentID,
		"principal_id":       a.PrincipalID,
		"principal_type":     a.PrincipalType,
		"role_definition_id": a.RoleDefinitionID,
		"is_privileged":      a.IsPrivileged,
	}
	if a.PrincipalDisplayName != "" {
		pm["principal_display_name"] = a.PrincipalDisplayName
	}
	if a.RoleDisplayName != "" {
		pm["role_display_name"] = a.RoleDisplayName
	}
	if a.DirectoryScopeID != "" {
		pm["directory_scope_id"] = a.DirectoryScopeID
	}
	if a.TenantID != "" {
		pm["tenant_id"] = a.TenantID
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.EntraRoleAssignmentKey(a.AssignmentID, now),
		EvidenceKind:   "azure.entra_role_assignment.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"azure:" + a.TenantID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("entra"),
		},
	}, nil
}

func buildStorageRecord(c storage.AccountConfig, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := c.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"account_id":               c.AccountID,
		"account_name":             c.AccountName,
		"subscription_id":          c.SubscriptionID,
		"encryption_enabled":       c.EncryptionEnabled,
		"https_traffic_only":       c.HTTPSTrafficOnly,
		"allow_blob_public_access": c.AllowBlobPublicAccess,
	}
	if c.ResourceGroup != "" {
		pm["resource_group"] = c.ResourceGroup
	}
	if c.Location != "" {
		pm["location"] = c.Location
	}
	if c.EncryptionKeySource != "" {
		pm["encryption_key_source"] = c.EncryptionKeySource
	}
	if c.MinimumTLSVersion != "" {
		pm["minimum_tls_version"] = c.MinimumTLSVersion
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.StorageAccountKey(c.AccountID, now),
		EvidenceKind:   "azure.storage_account_config.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"azure:" + c.SubscriptionID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapStorageResult(c.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("storage"),
		},
	}, nil
}

func mapStorageResult(r storage.ConfigResult) evidencev1.Result {
	switch r {
	case storage.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case storage.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case storage.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

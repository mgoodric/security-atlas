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

	"github.com/mgoodric/security-atlas/connectors/azure/internal/aks"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/entra"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/keyvault"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/nsg"
	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the two Azure reads + the sdk client constructor
// without hitting real Azure endpoints or a real platform endpoint. Production
// code paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	entraPull    = entra.Pull
	storageScan  = storage.Inspect
	aksScan      = aks.Inspect
	nsgScan      = nsg.Inspect
	keyvaultScan = keyvault.Inspect
	newSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// acquireToken is seamed so tests never call the live Entra token endpoint.
	acquireToken = func(ctx context.Context, cred azureauth.Credential, hc *http.Client, scope string) (string, error) {
		return cred.AcquireToken(ctx, hc, scope)
	}
	// newEntraAPI / newStorageAPI / newAKSAPI build the live read-only HTTP
	// clients; seamed so tests inject fakes.
	newEntraAPI = func(hc *http.Client, token string) entra.API {
		return entra.NewClient(hc, "", token)
	}
	newStorageAPI = func(hc *http.Client, subscriptionID, token string) storage.API {
		return storage.NewClient(hc, "", subscriptionID, token)
	}
	newAKSAPI = func(hc *http.Client, subscriptionID, token string) aks.API {
		return aks.NewClient(hc, "", subscriptionID, token)
	}
	newNSGAPI = func(hc *http.Client, subscriptionID, token string) nsg.API {
		return nsg.NewClient(hc, "", subscriptionID, token)
	}
	newKeyVaultAPI = func(hc *http.Client, subscriptionID, token string) keyvault.API {
		return keyvault.NewClient(hc, "", subscriptionID, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	tenantID        string
	subscriptionID  string
	environment     string
	authMode        string
	clientID        string
	entraControl    string
	storageControl  string
	aksControl      string
	nsgControl      string
	keyvaultControl string
	skipEntra       bool
	skipStorage     bool
	skipAKS         bool
	skipNSG         bool
	skipKeyVault    bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Entra ID + Azure Storage + AKS + NSG + Key Vault and push evidence records",
		Long: `Read Microsoft Entra ID role assignments, Azure Storage account
configuration, AKS managed-cluster configuration, NSG firewall-rule posture, and
Key-Vault access-policy / RBAC posture, transform to evidence records, and push
to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Least-privilege Azure permissions (read-only):
  - Microsoft Graph: Directory.Read.All  (gates azure.entra_role_assignment.v1)
  - Microsoft Graph: Application.Read.All (gates azure.entra_role_assignment.v1)
  - Azure Resource Manager: Reader role   (gates azure.storage_account_config.v1
                                           + azure.aks_cluster_config.v1
                                           + azure.nsg_rules.v1
                                           + azure.keyvault_access_config.v1)

The Key-Vault kind reads the ARM management plane ONLY (vault config + access
policies). It NEVER touches the Key-Vault data plane (secret/key/certificate
values) and requires NO Key-Vault data-plane role.

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
			if !f.skipAKS && f.subscriptionID == "" {
				return errors.New("--subscription-id is required for the AKS kind (or pass --skip-aks)")
			}
			if !f.skipNSG && f.subscriptionID == "" {
				return errors.New("--subscription-id is required for the NSG kind (or pass --skip-nsg)")
			}
			if !f.skipKeyVault && f.subscriptionID == "" {
				return errors.New("--subscription-id is required for the Key-Vault kind (or pass --skip-keyvault)")
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
	cmd.Flags().StringVar(&f.aksControl, "aks-control", "scf:CFG-02", "control_id to attach to azure.aks_cluster_config.v1 records")
	cmd.Flags().StringVar(&f.nsgControl, "nsg-control", "scf:NET-04", "control_id to attach to azure.nsg_rules.v1 records")
	cmd.Flags().StringVar(&f.keyvaultControl, "keyvault-control", "scf:CRY-09", "control_id to attach to azure.keyvault_access_config.v1 records")
	cmd.Flags().BoolVar(&f.skipEntra, "skip-entra", false, "skip azure.entra_role_assignment.v1 pull")
	cmd.Flags().BoolVar(&f.skipStorage, "skip-storage", false, "skip azure.storage_account_config.v1 pull")
	cmd.Flags().BoolVar(&f.skipAKS, "skip-aks", false, "skip azure.aks_cluster_config.v1 pull")
	cmd.Flags().BoolVar(&f.skipNSG, "skip-nsg", false, "skip azure.nsg_rules.v1 pull")
	cmd.Flags().BoolVar(&f.skipKeyVault, "skip-keyvault", false, "skip azure.keyvault_access_config.v1 pull")
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

	if !f.skipAKS {
		token, err := acquireToken(ctx, cred, httpClient, aks.ARMScope)
		if err != nil {
			return fmt.Errorf("arm token: %w", err)
		}
		clusters, err := aksScan(ctx, newAKSAPI(httpClient, f.subscriptionID, token), f.subscriptionID, nil)
		if err != nil {
			return fmt.Errorf("aks inspect: %w", err)
		}
		for _, c := range clusters {
			rec, err := buildAKSRecord(c, f.environment, f.aksControl)
			if err != nil {
				return fmt.Errorf("build aks record %s: %w", c.ClusterName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push aks %s: %w", c.ClusterName, err)
			}
			pushed++
		}
	}

	if !f.skipNSG {
		token, err := acquireToken(ctx, cred, httpClient, nsg.ARMScope)
		if err != nil {
			return fmt.Errorf("arm token: %w", err)
		}
		groups, err := nsgScan(ctx, newNSGAPI(httpClient, f.subscriptionID, token), f.subscriptionID, nil)
		if err != nil {
			return fmt.Errorf("nsg inspect: %w", err)
		}
		for _, g := range groups {
			rec, err := buildNSGRecord(g, f.environment, f.nsgControl)
			if err != nil {
				return fmt.Errorf("build nsg record %s: %w", g.NSGName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push nsg %s: %w", g.NSGName, err)
			}
			pushed++
		}
	}

	if !f.skipKeyVault {
		token, err := acquireToken(ctx, cred, httpClient, keyvault.ARMScope)
		if err != nil {
			return fmt.Errorf("arm token: %w", err)
		}
		vaults, err := keyvaultScan(ctx, newKeyVaultAPI(httpClient, f.subscriptionID, token), f.subscriptionID, nil)
		if err != nil {
			return fmt.Errorf("keyvault inspect: %w", err)
		}
		for _, v := range vaults {
			rec, err := buildKeyVaultRecord(v, f.environment, f.keyvaultControl)
			if err != nil {
				return fmt.Errorf("build keyvault record %s: %w", v.VaultName, err)
			}
			if err := pushOne(ctx, sdkClient, rec); err != nil {
				return fmt.Errorf("push keyvault %s: %w", v.VaultName, err)
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

// buildAKSRecord maps one AKS managed-cluster config into an evidence record.
// The payload carries management-plane CONFIGURATION flags ONLY — never admin
// kubeconfig, secrets, or workload manifests (the aks.ClusterConfig struct has
// no field for such data; this builder cannot emit what it cannot read).
func buildAKSRecord(c aks.ClusterConfig, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := c.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"cluster_id":           c.ClusterID,
		"cluster_name":         c.ClusterName,
		"subscription_id":      c.SubscriptionID,
		"rbac_enabled":         c.RBACEnabled,
		"private_cluster":      c.PrivateCluster,
		"authorized_ip_ranges": c.AuthorizedIPRanges,
	}
	if c.ResourceGroup != "" {
		pm["resource_group"] = c.ResourceGroup
	}
	if c.Location != "" {
		pm["location"] = c.Location
	}
	if c.KubernetesVersion != "" {
		pm["kubernetes_version"] = c.KubernetesVersion
	}
	if c.NetworkPolicy != "" {
		pm["network_policy"] = c.NetworkPolicy
	}
	// Booleans always carry their value (false is signal, not absence).
	pm["managed_identity"] = c.ManagedIdentity
	pm["local_accounts_disabled"] = c.LocalAccountsDisabled
	pm["oidc_issuer_enabled"] = c.OIDCIssuerEnabled
	pm["node_pool_count"] = c.NodePoolCount
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.AKSClusterConfigKey(c.ClusterID, now),
		EvidenceKind:   "azure.aks_cluster_config.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"azure:" + c.SubscriptionID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapAKSResult(c.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("aks"),
		},
	}, nil
}

func mapAKSResult(r aks.ConfigResult) evidencev1.Result {
	switch r {
	case aks.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case aks.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case aks.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

// buildNSGRecord maps one NSG's firewall-rule posture into an evidence record.
// The payload carries RULE metadata ONLY — never flow logs, packet captures, or
// traffic contents (the nsg.GroupConfig / nsg.SecurityRule structs have no field
// for such data; this builder cannot emit what it cannot read).
func buildNSGRecord(g nsg.GroupConfig, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := g.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"nsg_id":             g.NSGID,
		"nsg_name":           g.NSGName,
		"subscription_id":    g.SubscriptionID,
		"associated_subnets": g.AssociatedSubnets,
		"associated_nics":    g.AssociatedNICs,
		"rules":              nsgRulePayload(g.Rules),
	}
	if g.ResourceGroup != "" {
		pm["resource_group"] = g.ResourceGroup
	}
	if g.Location != "" {
		pm["location"] = g.Location
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.NSGRulesKey(g.NSGID, now),
		EvidenceKind:   "azure.nsg_rules.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"azure:" + g.SubscriptionID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapNSGResult(g.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("nsg"),
		},
	}, nil
}

// nsgRulePayload renders the security-rule list into a structpb-compatible
// []any. RULE metadata only — empty optional fields are omitted from each item.
func nsgRulePayload(rules []nsg.SecurityRule) []any {
	out := make([]any, 0, len(rules))
	for _, r := range rules {
		item := map[string]any{
			"name":      r.Name,
			"direction": r.Direction,
			"access":    r.Access,
		}
		if r.Protocol != "" {
			item["protocol"] = r.Protocol
		}
		item["priority"] = r.Priority
		if r.SourceAddressPrefix != "" {
			item["source_address_prefix"] = r.SourceAddressPrefix
		}
		if r.DestinationAddressPrefix != "" {
			item["destination_address_prefix"] = r.DestinationAddressPrefix
		}
		if r.SourcePortRange != "" {
			item["source_port_range"] = r.SourcePortRange
		}
		if r.DestinationPortRange != "" {
			item["destination_port_range"] = r.DestinationPortRange
		}
		out = append(out, item)
	}
	return out
}

func mapNSGResult(r nsg.ConfigResult) evidencev1.Result {
	switch r {
	case nsg.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case nsg.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case nsg.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

// buildKeyVaultRecord maps one Key-Vault's access posture into an evidence
// record. The payload carries management-plane CONFIGURATION + access-policy /
// role-assignment METADATA ONLY — never a secret, key, or certificate value
// (the keyvault.VaultConfig / keyvault.AccessEntry structs have no field for
// such data; this builder cannot emit what it cannot read, and the connector
// never touches the Key-Vault data plane).
func buildKeyVaultRecord(v keyvault.VaultConfig, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := v.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"vault_id":           v.VaultID,
		"vault_name":         v.VaultName,
		"subscription_id":    v.SubscriptionID,
		"rbac_authorization": v.RBACAuthorization,
	}
	// Booleans always carry their value (false is signal, not absence).
	pm["purge_protection"] = v.PurgeProtection
	pm["soft_delete_enabled"] = v.SoftDeleteEnabled
	if v.ResourceGroup != "" {
		pm["resource_group"] = v.ResourceGroup
	}
	if v.Location != "" {
		pm["location"] = v.Location
	}
	if v.PublicNetworkAccess != "" {
		pm["public_network_access"] = v.PublicNetworkAccess
	}
	if v.NetworkACLDefault != "" {
		pm["network_acl_default"] = v.NetworkACLDefault
	}
	if len(v.AccessEntries) > 0 {
		pm["access_entries"] = keyVaultAccessPayload(v.AccessEntries)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.KeyVaultAccessKey(v.VaultID, now),
		EvidenceKind:   "azure.keyvault_access_config.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"azure:" + v.SubscriptionID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapKeyVaultResult(v.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("keyvault"),
		},
	}, nil
}

// keyVaultAccessPayload renders the access-entry list into a structpb-compatible
// []any. Access METADATA only (principal id/type + permission verbs or role
// name) — never a secret value; empty optional fields are omitted from each
// item.
func keyVaultAccessPayload(entries []keyvault.AccessEntry) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		item := map[string]any{
			"principal_id":   e.PrincipalID,
			"principal_type": e.PrincipalType,
		}
		if len(e.Permissions) > 0 {
			perms := make([]any, 0, len(e.Permissions))
			for _, p := range e.Permissions {
				perms = append(perms, p)
			}
			item["permissions"] = perms
		}
		if e.RoleName != "" {
			item["role_name"] = e.RoleName
		}
		out = append(out, item)
	}
	return out
}

func mapKeyVaultResult(r keyvault.ConfigResult) evidencev1.Result {
	switch r {
	case keyvault.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case keyvault.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case keyvault.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

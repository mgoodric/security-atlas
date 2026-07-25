// Package keyvault inspects Azure Key-Vault access posture — the load-bearing
// signal for the Azure connector's Key-Vault evidence kind (slice 521).
//
// Source: read-only Azure Resource Manager (ARM Reader role) — the SAME role
// the storage, AKS and NSG kinds use; no new Azure scope (P0-521-3). The
// connector reads vault management-plane CONFIGURATION + ACCESS-POLICY /
// ROLE-ASSIGNMENT METADATA only — NEVER a secret, key, or certificate VALUE
// (the Key-Vault DATA plane, vault.azure.net) (P0-521-2), and is NEVER granted
// any data-plane permission (P0-521-1). The per-vault access model (legacy
// access policies vs Azure RBAC), purge-protection / soft-delete state,
// public-network-access posture, and the principals entitled to the vault are
// the minimum that demonstrates the secrets-management / least-privilege
// control (e.g. "the vault enforces purge protection and grants no over-broad
// secret access").
package keyvault

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConfigResult enumerates the per-vault verdict. Maps 1:1 onto the gRPC Result
// enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// AccessEntry is one principal's entitlement to a vault. It is EITHER a legacy
// access-policy entry (Permissions populated, granted permission verbs for
// keys/secrets/certificates) OR an Azure RBAC role assignment (RoleName
// populated). Field names map 1:1 to the azure.keyvault_access_config.v1
// schema's access-entry items.
//
// Over-collection guard (P0-521-2, structural): this struct carries access
// METADATA ONLY — a principal id/type and the permission VERBS or role NAME
// it was granted. There is deliberately NO field for any secret, key, or
// certificate VALUE (no place for such data to land even if a future ARM field
// exposed it). The MetadataOnly test pins this.
type AccessEntry struct {
	PrincipalID   string   // object id of the entitled principal
	PrincipalType string   // "access_policy" | "rbac_role_assignment"
	Permissions   []string // access-policy permission verbs (e.g. "keys:get", "secrets:list"); empty for an RBAC entry
	RoleName      string   // RBAC role definition name (e.g. "Key Vault Reader"); empty for an access-policy entry
}

// VaultConfig is the per-vault payload the connector emits. Field names map 1:1
// to the azure.keyvault_access_config.v1 schema.
//
// Over-collection guard (P0-521-2, structural): management-plane CONFIGURATION
// + access-policy / role-assignment METADATA ONLY. No secret/key/certificate
// VALUE field exists. The MetadataOnly test pins this.
type VaultConfig struct {
	VaultID             string
	VaultName           string
	SubscriptionID      string
	ResourceGroup       string
	Location            string
	RBACAuthorization   bool // true: Azure RBAC mode; false: legacy access-policy mode
	PurgeProtection     bool
	SoftDeleteEnabled   bool
	PublicNetworkAccess string // "Enabled" | "Disabled" | "" (unset)
	NetworkACLDefault   string // network ACL default action: "Allow" | "Deny" | "" (no network ACLs configured)
	AccessEntries       []AccessEntry
	Result              ConfigResult
	Reason              string // human-readable inconclusive / fail reason
	ObservedAt          time.Time
}

// RawVault is the narrow view the API surface returns for one vault. The
// concrete ARM client maps the SDK response into this shape; tests construct it
// directly.
//
// Over-collection guard: CONFIGURATION + access METADATA only — no secret, key,
// or certificate value.
type RawVault struct {
	ID                  string
	Name                string
	ResourceGroup       string
	Location            string
	RBACAuthorization   bool
	PurgeProtection     bool
	SoftDeleteEnabled   bool
	PublicNetworkAccess string
	NetworkACLDefault   string
	AccessEntries       []AccessEntry
	// ReadError, when non-empty, marks the vault as INCONCLUSIVE (a per-vault
	// ARM read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// wraps the read-only Azure Resource Manager vaults list (and, for an
// RBAC-authorization vault, a SECOND scoped read against
// Microsoft.Authorization/roleAssignments — slice 615 — merged into the vault's
// AccessEntries before it is returned). Tests pass a fake. v0 lists the first
// bounded page for one subscription; cursor pagination + multi-subscription
// enumeration are documented follow-ons (threat-model D, shared with slice 486
// R3).
type API interface {
	ListVaults(ctx context.Context) ([]RawVault, error)
}

// Inspect returns the access posture for every visible Key Vault in the
// subscription. subscriptionID scopes every record. now is injectable for
// deterministic tests (nil -> time.Now UTC).
func Inspect(ctx context.Context, api API, subscriptionID string, now func() time.Time) ([]VaultConfig, error) {
	if api == nil {
		return nil, errors.New("keyvault: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListVaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("list key vaults: %w", err)
	}
	observedAt := now()
	out := make([]VaultConfig, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.Name == "" {
			continue
		}
		cfg := VaultConfig{
			VaultID:             r.ID,
			VaultName:           r.Name,
			SubscriptionID:      subscriptionID,
			ResourceGroup:       r.ResourceGroup,
			Location:            r.Location,
			RBACAuthorization:   r.RBACAuthorization,
			PurgeProtection:     r.PurgeProtection,
			SoftDeleteEnabled:   r.SoftDeleteEnabled,
			PublicNetworkAccess: r.PublicNetworkAccess,
			NetworkACLDefault:   r.NetworkACLDefault,
			AccessEntries:       r.AccessEntries,
			ObservedAt:          observedAt,
		}
		cfg.Result, cfg.Reason = verdict(r)
		out = append(out, cfg)
	}
	return out, nil
}

// verdict deterministically scores the vault's secrets-management posture. FAIL
// when purge protection is off, soft-delete is off, or the public network is
// reachable without a default-Deny network ACL; INCONCLUSIVE when the per-vault
// read errored; PASS otherwise. The platform evaluator owns the final pass/fail
// per (control, vault) — this is a descriptive default.
func verdict(r RawVault) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read vault config: " + r.ReadError
	}
	switch {
	case !r.SoftDeleteEnabled:
		return ResultFail, "soft-delete not enabled"
	case !r.PurgeProtection:
		return ResultFail, "purge protection not enabled"
	case publicReachable(r):
		return ResultFail, "vault reachable from the public network with no default-Deny network ACL"
	default:
		return ResultPass, ""
	}
}

// publicReachable reports whether the vault is open to the public network. It
// is reachable when public network access is explicitly Enabled AND the network
// ACL default action is not Deny. An empty PublicNetworkAccess is treated as
// not-explicitly-open (the ARM default varies by API version; the connector
// does not infer a FAIL from an absent field).
func publicReachable(r RawVault) bool {
	if r.PublicNetworkAccess != "Enabled" {
		return false
	}
	return r.NetworkACLDefault != "Deny"
}

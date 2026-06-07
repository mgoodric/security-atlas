// Package storage inspects Azure Storage account hardening posture — the
// load-bearing signal for the Azure connector's storage evidence kind.
//
// Source: read-only Azure Resource Manager (ARM Reader role). The connector
// reads account CONFIGURATION only — NEVER blob/object contents, access keys, or
// SAS tokens.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ConfigResult enumerates what the connector reports per account. Maps 1:1 onto
// the gRPC Result enum.
type ConfigResult string

const (
	ResultPass         ConfigResult = "pass"
	ResultFail         ConfigResult = "fail"
	ResultInconclusive ConfigResult = "inconclusive"
)

// AccountConfig is the per-account payload the connector emits. Field names map
// 1:1 to azure.storage_account_config.v1 schema.
type AccountConfig struct {
	AccountID             string
	AccountName           string
	SubscriptionID        string
	ResourceGroup         string
	Location              string
	EncryptionEnabled     bool
	EncryptionKeySource   string
	HTTPSTrafficOnly      bool
	MinimumTLSVersion     string
	AllowBlobPublicAccess bool
	Result                ConfigResult
	Reason                string // human-readable inconclusive reason
	ObservedAt            time.Time
}

// RawAccount is the narrow view the API surface returns for one storage account.
// The concrete ARM client maps the SDK response into this shape; tests construct
// it directly. Config only — no keys, no SAS, no blob contents.
type RawAccount struct {
	ID                    string
	Name                  string
	ResourceGroup         string
	Location              string
	EncryptionEnabled     bool
	EncryptionKeySource   string
	HTTPSTrafficOnly      bool
	MinimumTLSVersion     string
	AllowBlobPublicAccess bool
	// ReadError, when non-empty, marks the account as INCONCLUSIVE (a per-
	// account ARM read errored) rather than dropping it.
	ReadError string
}

// API is the narrow surface Inspect depends on. The concrete implementation
// wraps the Azure Resource Manager SDK; tests pass a fake. v0 lists the first
// bounded page for one subscription; cursor pagination + multi-subscription
// enumeration are documented follow-ons (threat-model D).
type API interface {
	ListStorageAccounts(ctx context.Context) ([]RawAccount, error)
}

// Inspect returns the hardening posture for every visible storage account in the
// subscription. subscriptionID scopes every record. now is injectable for
// deterministic tests (nil → time.Now UTC).
func Inspect(ctx context.Context, api API, subscriptionID string, now func() time.Time) ([]AccountConfig, error) {
	if api == nil {
		return nil, errors.New("storage: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListStorageAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list storage accounts: %w", err)
	}
	observedAt := now()
	out := make([]AccountConfig, 0, len(raw))
	for _, r := range raw {
		if r.ID == "" || r.Name == "" {
			continue
		}
		cfg := AccountConfig{
			AccountID:             r.ID,
			AccountName:           r.Name,
			SubscriptionID:        subscriptionID,
			ResourceGroup:         r.ResourceGroup,
			Location:              r.Location,
			EncryptionEnabled:     r.EncryptionEnabled,
			EncryptionKeySource:   r.EncryptionKeySource,
			HTTPSTrafficOnly:      r.HTTPSTrafficOnly,
			MinimumTLSVersion:     r.MinimumTLSVersion,
			AllowBlobPublicAccess: r.AllowBlobPublicAccess,
			ObservedAt:            observedAt,
		}
		cfg.Result, cfg.Reason = verdict(r)
		out = append(out, cfg)
	}
	return out, nil
}

// verdict deterministically scores the account's hardening posture. PASS only
// when all three hardening flags are set; FAIL when any is off; INCONCLUSIVE
// when the per-account read errored.
func verdict(r RawAccount) (ConfigResult, string) {
	if r.ReadError != "" {
		return ResultInconclusive, "read account config: " + r.ReadError
	}
	switch {
	case !r.EncryptionEnabled:
		return ResultFail, "encryption at rest not enabled"
	case !r.HTTPSTrafficOnly:
		return ResultFail, "secure-transfer (HTTPS-only) not required"
	case r.AllowBlobPublicAccess:
		return ResultFail, "anonymous public blob access permitted"
	default:
		return ResultPass, ""
	}
}

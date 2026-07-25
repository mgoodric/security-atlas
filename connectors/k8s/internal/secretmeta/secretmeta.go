// Package secretmeta inventories Kubernetes Secret objects as METADATA ONLY —
// per-Secret type, namespace, name, age (creation timestamp), and the NAMES of
// the keys under .data (the map keys, NEVER their values). It is the
// load-bearing exception to the slice-487 connector family's no-Secrets rule:
// the base connector's ClusterRole deliberately excludes `secrets`, and this
// collector adds exactly one `secrets` get/list grant (k8sauth.SecretsRule).
//
// The auditable question is "how many TLS secrets / service-account tokens
// exist, where, and how old" (rotation + sprawl signals), NEVER the secret
// contents.
//
// Information disclosure is the PRIMARY threat (slice 525 threat model). The
// guard is STRUCTURAL, not procedural:
//
//   - The Inventory record struct has NO field that can hold a Secret value.
//     A reflection guard (secretmeta_test.go) fails the build if a field whose
//     name hints at a value / data / content / payload surface is ever added.
//   - The client decode target models .data as a map of key -> json.RawMessage
//     and reads ONLY the map KEYS into KeyNames; the RawMessage value (the
//     base64 blob) is never copied into any record-bound field and is dropped
//     when the decode target leaves scope. .stringData is NOT modeled at all,
//     so Go's json decoder discards it.
//
// Source: read-only Kubernetes API (get/list on core secrets). The collector
// reads ONLY metadata.name / metadata.namespace / metadata.creationTimestamp /
// type / the KEY NAMES of data — never a value, never .stringData, never an
// annotation, never any other field.
package secretmeta

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// RawSecret is the narrow view the API surface returns for one Secret object —
// METADATA ONLY. The concrete client maps the Kubernetes API response into this
// shape; tests construct it directly. There is deliberately NO field that can
// carry a Secret value (raw or base64): KeyNames holds only the KEY NAMES of
// .data. A structural reflection guard (secretmeta_test.go) fails the build if a
// value-bearing field is added.
type RawSecret struct {
	Namespace string
	Name      string
	// Type is the Secret's type string (Opaque / kubernetes.io/tls /
	// kubernetes.io/service-account-token / ...). Non-secret classification.
	Type string
	// CreatedAt is metadata.creationTimestamp. Drives the age signal.
	CreatedAt time.Time
	// KeyNames is the sorted set of KEY NAMES under .data — the MAP KEYS ONLY
	// (e.g. "tls.crt", "tls.key", "token"). NEVER the values behind them.
	KeyNames []string
}

// Inventory is the per-Secret metadata record the connector emits. Field names
// map 1:1 to the k8s.secret_inventory.v1 schema. METADATA ONLY — there is
// deliberately NO field for the Secret's .data / .stringData values (raw or
// base64). KeyNames carries the KEY NAMES only. A structural over-collection
// guard (secretmeta_test.go) fails the build if a value-bearing field is added.
type Inventory struct {
	Namespace string
	Name      string
	Type      string
	// AgeDays is whole days since CreatedAt at observation time — the rotation /
	// sprawl signal. Negative ages (clock skew / future timestamps) clamp to 0.
	AgeDays int
	// CreatedAt is the Secret's creation timestamp (UTC).
	CreatedAt time.Time
	// KeyNames is the sorted KEY NAMES under .data (map keys only, never values).
	KeyNames []string
	// ObservedAt is when the inventory record was produced.
	ObservedAt time.Time
}

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Kubernetes API calls; tests pass a fake. The list call
// follows the metadata.continue cursor to completion via the shared k8slist
// reader (slice 621).
type API interface {
	// ListSecretMeta returns one RawSecret per visible Secret, carrying ONLY
	// that Secret's metadata + the KEY NAMES of its .data — never a value.
	ListSecretMeta(ctx context.Context) ([]RawSecret, error)
}

// maxSecrets bounds the per-run Secret count the inventory materializes, so a
// pathological cluster (or a hostile API response) cannot blow up memory. The
// client already bounds the page read; this is the inventory-side cap. Mirrors
// pss.maxNamespaces.
const maxSecrets = 20000

// Collect returns the metadata inventory for every visible Secret. now is
// injectable for deterministic tests (nil -> time.Now UTC). The list is bounded
// by maxSecrets.
func Collect(ctx context.Context, api API, now func() time.Time) ([]Inventory, error) {
	if api == nil {
		return nil, errors.New("secretmeta: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	raw, err := api.ListSecretMeta(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secret metadata: %w", err)
	}
	observedAt := now().UTC()
	out := make([]Inventory, 0, len(raw))
	for _, s := range raw {
		if s.Name == "" || s.Namespace == "" {
			continue
		}
		if len(out) >= maxSecrets {
			break
		}
		out = append(out, inventorize(s, observedAt))
	}
	return out, nil
}

// inventorize derives one Secret's metadata record. It copies ONLY the
// metadata + the KEY NAMES; there is no code path that could copy a value
// because RawSecret carries none.
func inventorize(s RawSecret, observedAt time.Time) Inventory {
	keys := make([]string, len(s.KeyNames))
	copy(keys, s.KeyNames)
	sort.Strings(keys)
	return Inventory{
		Namespace:  s.Namespace,
		Name:       s.Name,
		Type:       normalizeType(s.Type),
		AgeDays:    ageDays(s.CreatedAt, observedAt),
		CreatedAt:  s.CreatedAt.UTC(),
		KeyNames:   keys,
		ObservedAt: observedAt,
	}
}

// normalizeType keeps the Secret type string as the cluster reports it, with the
// empty value normalized to "Opaque" — Kubernetes treats a Secret with no
// explicit type as Opaque, so recording it honestly avoids an empty type column.
func normalizeType(t string) string {
	if t == "" {
		return "Opaque"
	}
	return t
}

// ageDays returns whole days between createdAt and observedAt, clamped at 0 so
// a future / skewed creation timestamp never produces a negative age.
func ageDays(createdAt, observedAt time.Time) int {
	if createdAt.IsZero() {
		return 0
	}
	d := observedAt.Sub(createdAt)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

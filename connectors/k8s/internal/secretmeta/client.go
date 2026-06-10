package secretmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8slist"
)

// Client is a thin read-only HTTP client for the ONE Kubernetes endpoint the
// secret-inventory collector reads: core secrets (get/list — the one grant the
// base connector intentionally withheld, added by k8sauth.SecretsRule for THIS
// mode only). It delegates HTTP + pagination to the shared k8slist.Reader: the
// secrets list call follows the Kubernetes metadata.continue cursor to
// completion (slice 621), so a cluster with more than one page of Secrets is not
// silently truncated. It holds a short-lived bearer token (never logged) and
// issues only GET requests.
//
// CRITICAL (AC-5 — the load-bearing metadata-only guard): a Secret object's
// most sensitive fields are .data (a map of key -> base64 value) and .stringData
// (a map of key -> plaintext value). This client:
//
//   - does NOT model .stringData at all, so Go's json decoder discards it
//     before it ever reaches Go memory; and
//   - models .data as a map of key -> json.RawMessage and reads ONLY the map
//     KEYS into RawSecret.KeyNames. The RawMessage values (the base64 blobs) are
//     never decoded into a string, never base64-decoded, and never copied into a
//     record-bound field — they are dropped when the decode target leaves scope.
//
// reduce() is the single chokepoint; a test (TestReduce_DropsSecretValues) feeds
// a Secret with real .data (base64) + .stringData and proves only
// type/namespace/name/age/key-names survive.
type Client struct {
	r *k8slist.Reader
}

// NewClient builds a secret-inventory client. token is a read-only
// ServiceAccount bearer token (from k8sauth.Credential.Token). baseURL is the
// API server URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{r: k8slist.NewReader(httpClient, baseURL, token)}
}

// APIError is re-exported from the shared reader so callers and tests keep
// referring to secretmeta.APIError.
type APIError = k8slist.APIError

// --- minimal Kubernetes API JSON shapes (Secret METADATA ONLY) ---
//
// We model ONLY metadata (name / namespace / creationTimestamp), the type
// string, and the KEYS of .data. We deliberately do NOT model .stringData, and
// we decode .data values as opaque json.RawMessage we never read — so no Secret
// value (raw or base64) is ever materialized into a record-bound field.

type apiMeta struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	CreationTimestamp time.Time `json:"creationTimestamp"`
}

// apiSecret models the metadata + type + the KEYS of data only. data is decoded
// as a map of key -> RawMessage: we take the map's KEYS and never touch a value.
// stringData is intentionally absent (discarded by the decoder).
type apiSecret struct {
	Metadata apiMeta `json:"metadata"`
	Type     string  `json:"type"`
	// Data: key -> base64 value. We read ONLY the keys. The json.RawMessage
	// values are never decoded / base64-decoded / copied anywhere.
	Data map[string]json.RawMessage `json:"data"`
}

// ListSecretMeta reads every Secret (the list call follows the continue cursor
// to completion — read-only) and reduces each to its METADATA. Read-only: only a
// GET against core secrets.
func (c *Client) ListSecretMeta(ctx context.Context) ([]RawSecret, error) {
	secrets, err := k8slist.ListAll[apiSecret](ctx, c.r, "/api/v1/secrets")
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	out := make([]RawSecret, 0, len(secrets))
	for _, s := range secrets {
		if s.Metadata.Name == "" || s.Metadata.Namespace == "" {
			continue
		}
		out = append(out, reduce(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// reduce collapses one Secret object into the METADATA-ONLY RawSecret the
// evidence kind carries. This is THE over-collection chokepoint: it reads the
// KEY NAMES of .data (the map keys) and NEVER a value. It never touches
// .stringData (not even decoded). The base64 values behind the keys are dropped
// on the floor here — they exist only as opaque json.RawMessage in the transient
// decode target and are never copied into RawSecret.
func reduce(s apiSecret) RawSecret {
	keys := make([]string, 0, len(s.Data))
	for k := range s.Data { // map KEYS only — the values (RawMessage) are never read
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return RawSecret{
		Namespace: s.Metadata.Namespace,
		Name:      s.Metadata.Name,
		Type:      s.Type,
		CreatedAt: s.Metadata.CreationTimestamp,
		KeyNames:  keys,
	}
}

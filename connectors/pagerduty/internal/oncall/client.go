package oncall

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdhttp"
)

// Client is the read-only PagerDuty escalation-policy client. It issues only a
// GET against /escalation_policies (requires a read-only token) and decodes
// ONLY the id/name, the escalation rules (tiers), and each rule target's
// id/type/summary. Responder personal-contact fields (phone, personal email)
// live on the /users resource and are NEVER fetched or decoded here
// (P0-489-3).
type Client struct {
	t *pdhttp.Transport
}

// NewClient builds an escalation-policy client. token is the read-only
// PagerDuty token; baseURL is the REST API base.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	return &Client{t: pdhttp.New(httpClient, baseURL, token)}
}

const pageLimit = 100

// apiEscalationPolicies is the minimal PagerDuty escalation-policy list shape —
// PII-free fields only. Every personal-contact field (phone, email) on a
// referenced user is intentionally absent: json.Decode discards JSON keys with
// no matching struct field, so they never enter memory as connector data.
type apiEscalationPolicies struct {
	EscalationPolicies []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		EscalationRules []struct {
			Targets []struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Summary string `json:"summary"`
			} `json:"targets"`
		} `json:"escalation_rules"`
	} `json:"escalation_policies"`
}

// ListEscalationPolicies reads the first bounded page of escalation policies.
// Read-only: a single GET against /escalation_policies.
func (c *Client) ListEscalationPolicies(ctx context.Context) ([]RawPolicy, error) {
	var resp apiEscalationPolicies
	if err := c.t.GetJSON(ctx, "/escalation_policies?limit="+strconv.Itoa(pageLimit)+"&offset=0", &resp); err != nil {
		return nil, err
	}
	out := make([]RawPolicy, 0, len(resp.EscalationPolicies))
	for _, p := range resp.EscalationPolicies {
		if strings.TrimSpace(p.ID) == "" {
			continue
		}
		tiers := make([]RawTier, 0, len(p.EscalationRules))
		for i, r := range p.EscalationRules {
			targets := make([]RawTarget, 0, len(r.Targets))
			for _, tg := range r.Targets {
				targets = append(targets, RawTarget{
					Kind: tg.Type,
					ID:   tg.ID,
					Name: tg.Summary,
				})
			}
			tiers = append(tiers, RawTier{Level: i + 1, Targets: targets})
		}
		out = append(out, RawPolicy{ID: p.ID, Name: p.Name, Tiers: tiers})
	}
	return out, nil
}

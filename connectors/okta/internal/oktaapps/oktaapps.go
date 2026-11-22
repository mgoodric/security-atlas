// Package oktaapps pulls Okta applications and their group assignments,
// producing one Assignment per application.
//
// Endpoints:
//   - GET /api/v1/apps                            (list applications)
//   - GET /api/v1/apps/{id}/groups                (groups assigned to app)
//   - GET /api/v1/policies/{policyId}/rules       (rules of the app sign-in
//     policy the app's _links.accessPolicy names, #2828)
//
// Output is descriptive: the evaluator (slice 015) interprets which app-
// assignment pattern passes/fails per (control, scope). The connector
// emits Result_INCONCLUSIVE so we don't bake policy into the pipe.
//
// #2828: each app also carries whether its app sign-in policy requires MFA
// (signOnMFA) and, when the operator classified apps, its classification
// (Classify). The shapes are Okta's management OpenAPI description
// (okta/okta-management-openapi-spec, dist/current/management-minimal.yaml,
// read 2026-11-21): ApplicationLinks.accessPolicy, AccessPolicyRule,
// AccessPolicyRuleApplicationSignOn{,Access}, VerificationMethod,
// AssuranceMethod and AssuranceMethodFactorMode.
package oktaapps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
)

// Assignment is one record the cmd layer turns into an evidence record.
// Field names map 1:1 to okta.app_assignment.v1 schema.
type Assignment struct {
	AppID              string
	AppName            string
	SignOnMode         string
	Status             string
	AssignedGroupIDs   []string
	AssignedGroupCount int
	// SignOnPolicyID is the id of the app sign-in policy (ACCESS_POLICY)
	// the app's _links.accessPolicy names; empty when the app has none.
	SignOnPolicyID string
	// SignOnMFARequired is whether that policy requires MFA (see
	// signOnMFA); nil when it could not be decided, and Reason says why.
	SignOnMFARequired *bool
	// Classification is admin, infrastructure or standard, set by
	// Classify; empty when the operator classified no app.
	Classification string
	// Reason says why SignOnMFARequired is nil.
	Reason     string
	ObservedAt time.Time
}

// Classifications an app may carry.
const (
	ClassAdmin          = "admin"
	ClassInfrastructure = "infrastructure"
	ClassStandard       = "standard"
)

// Classify sets each assignment's Classification from the operator's
// lists of app ids. With both lists empty it leaves every app
// unclassified, so an unconfigured connector is not read as "no admin
// apps". Otherwise an app on neither list is standard, and an id on both
// lists is admin. It returns the listed ids that no assignment carried,
// sorted, so the caller can warn about them.
func Classify(as []Assignment, admin, infrastructure []string) []string {
	if len(admin) == 0 && len(infrastructure) == 0 {
		return nil
	}
	class := map[string]string{}
	for _, id := range infrastructure {
		class[id] = ClassInfrastructure
	}
	for _, id := range admin {
		class[id] = ClassAdmin
	}
	seen := map[string]bool{}
	for i := range as {
		c, ok := class[as[i].AppID]
		if !ok {
			c = ClassStandard
		}
		as[i].Classification = c
		seen[as[i].AppID] = true
	}
	var missing []string
	for id := range class {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

// API is the narrow surface Pull depends on.
type API interface {
	ListApps(ctx context.Context) ([]rawApp, error)
	ListAppGroups(ctx context.Context, appID string) ([]rawGroup, error)
	ListPolicyRules(ctx context.Context, policyID string) ([]rawRule, error)
}

// Pull lists every app and reconciles its group assignments. Errors on a
// single app's group listing are recorded as the app having an empty
// assignment set with no fatal abort — partial inventory beats no
// inventory.
func Pull(ctx context.Context, api API, now func() time.Time) ([]Assignment, error) {
	if api == nil {
		return nil, errors.New("oktaapps: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	apps, err := api.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}
	out := make([]Assignment, 0, len(apps))
	// Many apps share one policy (a new app gets the default one), so each
	// policy's rules are read once.
	verdicts := map[string]mfaVerdict{}
	for _, a := range apps {
		if a.ID == "" {
			continue
		}
		name := firstNonEmpty(a.Label, a.Name)
		if name == "" {
			// Schema requires app_name; skip rather than emit invalid records.
			continue
		}
		groups, err := api.ListAppGroups(ctx, a.ID)
		if err != nil {
			// Recorded as zero-group assignment so the evaluator can decide.
			groups = nil
		}
		ids := make([]string, 0, len(groups))
		for _, g := range groups {
			if g.ID != "" {
				ids = append(ids, g.ID)
			}
		}
		asg := Assignment{
			AppID:              a.ID,
			AppName:            name,
			SignOnMode:         a.SignOnMode,
			Status:             normalizeStatus(a.Status),
			AssignedGroupIDs:   ids,
			AssignedGroupCount: len(ids),
			ObservedAt:         now(),
		}
		href := a.Links.AccessPolicy.Href
		policyID, ok := policyIDFromHref(href)
		switch {
		case href == "":
			asg.Reason = "app has no app sign-in policy (_links.accessPolicy absent)"
		case !ok:
			asg.Reason = "unrecognised _links.accessPolicy href"
		default:
			asg.SignOnPolicyID = policyID
			v, cached := verdicts[policyID]
			if !cached {
				rules, err := api.ListPolicyRules(ctx, policyID)
				if err != nil {
					v = mfaVerdict{reason: fmt.Sprintf("sign-in policy rules: %v", err)}
				} else {
					v = signOnMFA(rules)
				}
				verdicts[policyID] = v
			}
			asg.SignOnMFARequired, asg.Reason = v.required, v.reason
		}
		out = append(out, asg)
	}
	return out, nil
}

// policyIDFromHref takes the policy id from an accessPolicy href of the
// form <base>/api/v1/policies/{policyId}.
func policyIDFromHref(href string) (string, bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	dir, id := path.Split(u.Path)
	if dir != "/api/v1/policies/" || id == "" {
		return "", false
	}
	return id, true
}

type mfaVerdict struct {
	required *bool
	reason   string
}

// signOnMFA decides whether an app sign-in policy requires MFA. Every
// active rule is a path some sign-in can take, whatever its conditions, so
// the policy requires MFA only when each active rule denies access or
// allows it through an ASSURANCE method whose factorMode is 2FA. One
// active rule allowing 1FA decides false. A rule whose method reads as
// neither (AUTH_METHOD_CHAIN, ID_PROOFING, no factorMode, no appSignOn)
// leaves the policy undecided, unless another rule decides false.
func signOnMFA(rules []rawRule) mfaVerdict {
	no, yes := false, true
	active, undecided := 0, ""
	for _, r := range rules {
		if r.Status == "INACTIVE" {
			continue
		}
		active++
		so := r.Actions.AppSignOn
		switch {
		case so == nil:
			undecided = fmt.Sprintf("rule %s has no appSignOn action", r.ID)
		case so.Access == "DENY":
		case so.Access != "ALLOW":
			undecided = fmt.Sprintf("rule %s has access %q", r.ID, so.Access)
		case so.VerificationMethod == nil || so.VerificationMethod.Type != "ASSURANCE":
			undecided = fmt.Sprintf("rule %s verification method is not ASSURANCE", r.ID)
		case so.VerificationMethod.FactorMode == "1FA":
			return mfaVerdict{required: &no}
		case so.VerificationMethod.FactorMode != "2FA":
			undecided = fmt.Sprintf("rule %s has factorMode %q", r.ID, so.VerificationMethod.FactorMode)
		}
	}
	if undecided != "" {
		return mfaVerdict{reason: undecided}
	}
	if active == 0 {
		return mfaVerdict{reason: "sign-in policy has no active rule"}
	}
	return mfaVerdict{required: &yes}
}

// normalizeStatus collapses Okta's known status strings to the
// schema-enum subset. Unknown statuses pass through verbatim; the
// schema's enum will reject them at ingest validation time, surfacing the
// drift loudly.
func normalizeStatus(s string) string {
	up := strings.ToUpper(strings.TrimSpace(s))
	if up == "" {
		return "INACTIVE"
	}
	return up
}

// ---- REST client ----

type rawApp struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	SignOnMode string `json:"signOnMode"`
	Links      struct {
		AccessPolicy struct {
			Href string `json:"href"`
		} `json:"accessPolicy"`
	} `json:"_links"`
}

// rawRule is the part of an ACCESS_POLICY rule that signOnMFA reads.
type rawRule struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Actions struct {
		AppSignOn *struct {
			Access             string `json:"access"`
			VerificationMethod *struct {
				Type       string `json:"type"`
				FactorMode string `json:"factorMode"`
			} `json:"verificationMethod"`
		} `json:"appSignOn"`
	} `json:"actions"`
}

type rawGroup struct {
	ID string `json:"id"`
}

// Client is a thin HTTP client for the apps endpoint family.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Creds   oktaauth.Credential
}

// NewClient builds a Client.
func NewClient(httpClient *http.Client, baseURL string, creds oktaauth.Credential) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListApps fetches the first page of apps. v1 pulls only the first 200;
// pagination lands when org sizes demand it.
func (c *Client) ListApps(ctx context.Context) ([]rawApp, error) {
	u := c.BaseURL + "/api/v1/apps?limit=200"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawApp
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode apps: %w", err)
	}
	return out, nil
}

// ListAppGroups fetches the groups assigned to one app.
func (c *Client) ListAppGroups(ctx context.Context, appID string) ([]rawGroup, error) {
	if appID == "" {
		return nil, errors.New("oktaapps: appID required")
	}
	u := fmt.Sprintf("%s/api/v1/apps/%s/groups", c.BaseURL, url.PathEscape(appID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawGroup
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode app groups: %w", err)
	}
	return out, nil
}

// ListPolicyRules fetches the rules of one policy.
func (c *Client) ListPolicyRules(ctx context.Context, policyID string) ([]rawRule, error) {
	if policyID == "" {
		return nil, errors.New("oktaapps: policyID required")
	}
	u := fmt.Sprintf("%s/api/v1/policies/%s/rules", c.BaseURL, url.PathEscape(policyID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var out []rawRule
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode policy rules: %w", err)
	}
	return out, nil
}

// APIError carries Okta REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("okta: HTTP %d", e.Status)
	}
	return fmt.Sprintf("okta: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

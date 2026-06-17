package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// armWriteAPIVersion pins the Event-Grid Resource Provider API version used for
// the system-topic + event-subscription writes.
const armWriteAPIVersion = "2022-06-15"

// insightsAPIVersion pins the Microsoft.Insights API version used for the
// Activity-Log diagnostic-setting write.
const insightsAPIVersion = "2021-05-01-preview"

// Client is the live raw-HTTP ARM management client backing the provisioner. It
// holds a short-lived ELEVATED bearer token (never logged) and issues PUT /
// DELETE management-plane calls. It mirrors internal/storage's read-side Client
// shape; the only difference is the verbs (write/delete vs get) and that the
// token behind it carries an operator-supplied write role.
//
// This type is constructed ONLY by the provision/deprovision subcommands, never
// by the receiver (P0-658-1).
type Client struct {
	HTTP    *http.Client
	BaseURL string // default https://management.azure.com
	token   string
}

// NewClient builds the ARM write client. token is an ELEVATED bearer access
// token (acquired from the operator-supplied provisioning credential, NOT the
// receiver's read-only credential). baseURL empty defaults to the public ARM
// endpoint; tests pass an httptest URL.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://management.azure.com"
	}
	return &Client{
		HTTP:    httpClient,
		BaseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

func systemTopicID(base string, t SystemTopic) string {
	return fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventGrid/systemTopics/%s",
		base, url.PathEscape(t.SubscriptionID), url.PathEscape(t.ResourceGroup), url.PathEscape(t.Name))
}

// PutSystemTopic upserts the subscription-scoped system topic (source =
// Microsoft.Resources.Subscriptions, the Activity-Log source type).
func (c *Client) PutSystemTopic(ctx context.Context, t SystemTopic) error {
	u := systemTopicID(c.BaseURL, t) + "?api-version=" + armWriteAPIVersion
	body := map[string]any{
		"location": t.Location,
		"properties": map[string]any{
			"source":    fmt.Sprintf("/subscriptions/%s", t.SubscriptionID),
			"topicType": "Microsoft.Resources.Subscriptions",
		},
	}
	return c.do(ctx, http.MethodPut, u, body)
}

// PutEventSubscription upserts the event subscription routing the system topic
// to the receiver webhook. The delivery key is carried in the webhook
// destination's deliveryAttributeMappings as a static SECRET attribute so Event
// Grid presents it on each delivery; it is never logged here.
func (c *Client) PutEventSubscription(ctx context.Context, s EventSubscription) error {
	u := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventGrid/systemTopics/%s/eventSubscriptions/%s?api-version=%s",
		c.BaseURL, url.PathEscape(s.SubscriptionID), url.PathEscape(s.ResourceGroup),
		url.PathEscape(s.SystemTopic), url.PathEscape(s.Name), armWriteAPIVersion)

	attrName := s.DeliveryKeyHeader
	if attrName == "" {
		attrName = s.DeliveryKeyQueryParam
	}
	deliveryAttrs := []map[string]any{
		{
			"name": attrName,
			"type": "Static",
			"properties": map[string]any{
				"value":    s.DeliveryKey,
				"isSecret": true,
			},
		},
	}
	body := map[string]any{
		"properties": map[string]any{
			"destination": map[string]any{
				"endpointType": "WebHook",
				"properties": map[string]any{
					"endpointUrl":                   s.WebhookURL,
					"deliveryAttributeMappings":     deliveryAttrs,
					"maxEventsPerBatch":             1,
					"preferredBatchSizeInKilobytes": 64,
				},
			},
			"eventDeliverySchema": "EventGridSchema",
		},
	}
	return c.do(ctx, http.MethodPut, u, body)
}

// PutDiagnosticSetting upserts the subscription-level Activity-Log diagnostic
// setting that routes the named Activity-Log categories to the Event-Grid system
// topic destination.
func (c *Client) PutDiagnosticSetting(ctx context.Context, d DiagnosticSetting) error {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Insights/diagnosticSettings/%s?api-version=%s",
		c.BaseURL, url.PathEscape(d.SubscriptionID), url.PathEscape(d.Name), insightsAPIVersion)

	logs := make([]map[string]any, 0, len(d.ActivityLogCats))
	for _, cat := range d.ActivityLogCats {
		logs = append(logs, map[string]any{"category": cat, "enabled": true})
	}
	body := map[string]any{
		"properties": map[string]any{
			"eventHubAuthorizationRuleId": nil,
			"logs":                        logs,
			// Route to the Event-Grid system topic via the marketplace partner /
			// system-topic destination id.
			"systemTopicDestinationId": d.SystemTopicID,
		},
	}
	return c.do(ctx, http.MethodPut, u, body)
}

// DeleteEventSubscription removes the event subscription (idempotent: a 404 is
// treated as already-absent).
func (c *Client) DeleteEventSubscription(ctx context.Context, s EventSubscription) error {
	u := fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.EventGrid/systemTopics/%s/eventSubscriptions/%s?api-version=%s",
		c.BaseURL, url.PathEscape(s.SubscriptionID), url.PathEscape(s.ResourceGroup),
		url.PathEscape(s.SystemTopic), url.PathEscape(s.Name), armWriteAPIVersion)
	return c.do(ctx, http.MethodDelete, u, nil)
}

// DeleteSystemTopic removes the system topic (idempotent).
func (c *Client) DeleteSystemTopic(ctx context.Context, t SystemTopic) error {
	u := systemTopicID(c.BaseURL, t) + "?api-version=" + armWriteAPIVersion
	return c.do(ctx, http.MethodDelete, u, nil)
}

// DeleteDiagnosticSetting removes the Activity-Log diagnostic setting
// (idempotent).
func (c *Client) DeleteDiagnosticSetting(ctx context.Context, d DiagnosticSetting) error {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Insights/diagnosticSettings/%s?api-version=%s",
		c.BaseURL, url.PathEscape(d.SubscriptionID), url.PathEscape(d.Name), insightsAPIVersion)
	return c.do(ctx, http.MethodDelete, u, nil)
}

// do issues an ARM management request. For PUT the body is JSON-encoded; for
// DELETE a 404 / 204 is success (idempotent teardown). The request body is
// NEVER logged (it may carry the delivery key); only the response status + a
// bounded response body appear in errors.
func (c *Client) do(ctx context.Context, method, u string, body any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case method == http.MethodDelete && res.StatusCode == http.StatusNotFound:
		// Already absent — idempotent teardown.
		return nil
	default:
		return fmt.Errorf("arm %s %d: %s", method, res.StatusCode, drain(res.Body))
	}
}

// drain reads a bounded amount of an error response body for diagnostics.
func drain(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(b))
}

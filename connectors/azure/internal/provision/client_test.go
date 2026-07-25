// Client tests exercise the raw-HTTP ARM management client against an httptest
// server — no live Azure. Neutral test tokens only (no real Azure creds).
package provision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capturedReq struct {
	method string
	path   string
	query  string
	body   map[string]any
	auth   string
}

func newCapturingServer(t *testing.T, status int, captured *[]capturedReq) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cr := capturedReq{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				_ = json.Unmarshal(b, &cr.body)
			}
		}
		*captured = append(*captured, cr)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
}

func TestClient_PutSystemTopic(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusOK, &got)
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-elevated-token")
	err := c.PutSystemTopic(context.Background(), SystemTopic{
		SubscriptionID: "00000000-0000-0000-0000-000000000000",
		ResourceGroup:  "rg",
		Name:           "topic",
		Location:       "eastus",
	})
	if err != nil {
		t.Fatalf("PutSystemTopic: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 request; got %d", len(got))
	}
	r := got[0]
	if r.method != http.MethodPut {
		t.Errorf("method = %s; want PUT", r.method)
	}
	if !strings.Contains(r.path, "/providers/Microsoft.EventGrid/systemTopics/topic") {
		t.Errorf("path = %q", r.path)
	}
	if !strings.Contains(r.query, "api-version=") {
		t.Errorf("query missing api-version: %q", r.query)
	}
	if r.auth != "Bearer test-elevated-token" {
		t.Errorf("auth = %q; want Bearer token", r.auth)
	}
	if r.body["location"] != "eastus" {
		t.Errorf("body location = %v", r.body["location"])
	}
}

func TestClient_PutEventSubscription_CarriesDeliveryKey(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusCreated, &got)
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-elevated-token")
	err := c.PutEventSubscription(context.Background(), EventSubscription{
		SubscriptionID:    "sub",
		ResourceGroup:     "rg",
		SystemTopic:       "topic",
		Name:              "recv",
		WebhookURL:        "https://atlas.example.com/webhooks/azure/eventgrid",
		DeliveryKeyHeader: "Authorization",
		DeliveryKey:       "test-delivery-key",
	})
	if err != nil {
		t.Fatalf("PutEventSubscription: %v", err)
	}
	r := got[0]
	if !strings.Contains(r.path, "/systemTopics/topic/eventSubscriptions/recv") {
		t.Errorf("path = %q", r.path)
	}
	props, _ := r.body["properties"].(map[string]any)
	dest, _ := props["destination"].(map[string]any)
	destProps, _ := dest["properties"].(map[string]any)
	if destProps["endpointUrl"] != "https://atlas.example.com/webhooks/azure/eventgrid" {
		t.Errorf("endpointUrl = %v", destProps["endpointUrl"])
	}
	attrs, _ := destProps["deliveryAttributeMappings"].([]any)
	if len(attrs) != 1 {
		t.Fatalf("want 1 delivery attribute mapping; got %d", len(attrs))
	}
	a0, _ := attrs[0].(map[string]any)
	if a0["name"] != "Authorization" {
		t.Errorf("delivery attr name = %v; want Authorization", a0["name"])
	}
	ap, _ := a0["properties"].(map[string]any)
	if ap["value"] != "test-delivery-key" || ap["isSecret"] != true {
		t.Errorf("delivery key not carried as secret static attr: %v", ap)
	}
}

func TestClient_PutEventSubscription_QueryParamName(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusOK, &got)
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "tok")
	err := c.PutEventSubscription(context.Background(), EventSubscription{
		SubscriptionID: "s", ResourceGroup: "rg", SystemTopic: "t", Name: "n",
		WebhookURL: "https://x/y", DeliveryKeyQueryParam: "code", DeliveryKey: "k",
	})
	if err != nil {
		t.Fatalf("PutEventSubscription: %v", err)
	}
	props := got[0].body["properties"].(map[string]any)
	dest := props["destination"].(map[string]any)
	dp := dest["properties"].(map[string]any)
	attrs := dp["deliveryAttributeMappings"].([]any)
	if attrs[0].(map[string]any)["name"] != "code" {
		t.Errorf("query-param delivery attr name = %v; want code", attrs[0])
	}
}

func TestClient_PutDiagnosticSetting(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusOK, &got)
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "tok")
	err := c.PutDiagnosticSetting(context.Background(), DiagnosticSetting{
		SubscriptionID:  "sub",
		Name:            "diag",
		SystemTopicID:   "/subscriptions/sub/.../systemTopics/topic",
		ActivityLogCats: []string{"Administrative", "Security"},
	})
	if err != nil {
		t.Fatalf("PutDiagnosticSetting: %v", err)
	}
	r := got[0]
	if !strings.Contains(r.path, "/providers/Microsoft.Insights/diagnosticSettings/diag") {
		t.Errorf("path = %q", r.path)
	}
	props := r.body["properties"].(map[string]any)
	logs := props["logs"].([]any)
	if len(logs) != 2 {
		t.Errorf("want 2 log categories; got %d", len(logs))
	}
	if props["systemTopicDestinationId"] != "/subscriptions/sub/.../systemTopics/topic" {
		t.Errorf("destination id = %v", props["systemTopicDestinationId"])
	}
}

func TestClient_Delete_404IsIdempotent(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusNotFound, &got)
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "tok")
	// All three deletes must treat 404 as success.
	if err := c.DeleteEventSubscription(context.Background(), EventSubscription{SubscriptionID: "s", ResourceGroup: "rg", SystemTopic: "t", Name: "n"}); err != nil {
		t.Errorf("DeleteEventSubscription 404: %v", err)
	}
	if err := c.DeleteSystemTopic(context.Background(), SystemTopic{SubscriptionID: "s", ResourceGroup: "rg", Name: "t"}); err != nil {
		t.Errorf("DeleteSystemTopic 404: %v", err)
	}
	if err := c.DeleteDiagnosticSetting(context.Background(), DiagnosticSetting{SubscriptionID: "s", Name: "d"}); err != nil {
		t.Errorf("DeleteDiagnosticSetting 404: %v", err)
	}
	for _, r := range got {
		if r.method != http.MethodDelete {
			t.Errorf("want DELETE; got %s", r.method)
		}
	}
}

func TestClient_PutError_ReturnsStatus(t *testing.T) {
	var got []capturedReq
	srv := newCapturingServer(t, http.StatusForbidden, &got)
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "tok")
	err := c.PutSystemTopic(context.Background(), SystemTopic{SubscriptionID: "s", ResourceGroup: "rg", Name: "t", Location: "eastus"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error; got %v", err)
	}
}

func TestClient_PutError_DoesNotLeakRequestBody(t *testing.T) {
	// A failing PUT must surface the response status + bounded RESPONSE body, but
	// must NOT echo the request body (which carries the delivery key).
	const secret = "super-secret-delivery-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), srv.URL, "tok")
	err := c.PutEventSubscription(context.Background(), EventSubscription{
		SubscriptionID: "s", ResourceGroup: "rg", SystemTopic: "t", Name: "n",
		WebhookURL: "https://x/y", DeliveryKeyHeader: "Authorization", DeliveryKey: secret,
	})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("delivery key leaked into error: %v", err)
	}
}

func TestNewClient_Defaults(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, "", "tok")
	if c.HTTP == nil {
		t.Error("nil HTTP not defaulted")
	}
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("BaseURL = %q; want public ARM endpoint", c.BaseURL)
	}
}

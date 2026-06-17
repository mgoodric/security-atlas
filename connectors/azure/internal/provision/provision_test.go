package provision

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeAPI records the calls Provision / Deprovision make so tests assert order,
// args, and idempotency without touching live Azure.
type fakeAPI struct {
	putTopic   []SystemTopic
	putSub     []EventSubscription
	putDiag    []DiagnosticSetting
	delTopic   []SystemTopic
	delSub     []EventSubscription
	delDiag    []DiagnosticSetting
	order      []string
	failPutSub error
	failDelTop error
}

func (f *fakeAPI) PutSystemTopic(_ context.Context, t SystemTopic) error {
	f.putTopic = append(f.putTopic, t)
	f.order = append(f.order, "put-topic")
	return nil
}
func (f *fakeAPI) PutEventSubscription(_ context.Context, s EventSubscription) error {
	if f.failPutSub != nil {
		return f.failPutSub
	}
	f.putSub = append(f.putSub, s)
	f.order = append(f.order, "put-sub")
	return nil
}
func (f *fakeAPI) PutDiagnosticSetting(_ context.Context, d DiagnosticSetting) error {
	f.putDiag = append(f.putDiag, d)
	f.order = append(f.order, "put-diag")
	return nil
}
func (f *fakeAPI) DeleteEventSubscription(_ context.Context, s EventSubscription) error {
	f.delSub = append(f.delSub, s)
	f.order = append(f.order, "del-sub")
	return nil
}
func (f *fakeAPI) DeleteSystemTopic(_ context.Context, t SystemTopic) error {
	if f.failDelTop != nil {
		return f.failDelTop
	}
	f.delTopic = append(f.delTopic, t)
	f.order = append(f.order, "del-topic")
	return nil
}
func (f *fakeAPI) DeleteDiagnosticSetting(_ context.Context, d DiagnosticSetting) error {
	f.delDiag = append(f.delDiag, d)
	f.order = append(f.order, "del-diag")
	return nil
}

func goodPlan() Plan {
	return Plan{
		Topic: SystemTopic{
			SubscriptionID: "00000000-0000-0000-0000-000000000000",
			ResourceGroup:  "rg-atlas",
			Name:           "atlas-azure-activitylog",
			Location:       "eastus",
		},
		Subscription: EventSubscription{
			SubscriptionID:    "00000000-0000-0000-0000-000000000000",
			ResourceGroup:     "rg-atlas",
			SystemTopic:       "atlas-azure-activitylog",
			Name:              "atlas-azure-receiver",
			WebhookURL:        "https://atlas.example.com/webhooks/azure/eventgrid",
			DeliveryKeyHeader: "Authorization",
			DeliveryKey:       "test-delivery-key",
		},
	}
}

func TestProvision_CreatesTopicThenSubscription(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if err := Provision(context.Background(), api, goodPlan()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(api.putTopic) != 1 || len(api.putSub) != 1 {
		t.Fatalf("want 1 topic + 1 sub; got %d/%d", len(api.putTopic), len(api.putSub))
	}
	if len(api.putDiag) != 0 {
		t.Errorf("diagnostic should not be provisioned when IncludeDiagnostic is false")
	}
	if got := strings.Join(api.order, ","); got != "put-topic,put-sub" {
		t.Errorf("order = %q; want topic-before-subscription", got)
	}
	if api.putSub[0].DeliveryKey != "test-delivery-key" {
		t.Errorf("delivery key not threaded to subscription")
	}
	if api.putSub[0].WebhookURL != "https://atlas.example.com/webhooks/azure/eventgrid" {
		t.Errorf("webhook url = %q", api.putSub[0].WebhookURL)
	}
}

func TestProvision_WithDiagnostic(t *testing.T) {
	t.Parallel()
	p := goodPlan()
	p.IncludeDiagnostic = true
	p.Diagnostic = DiagnosticSetting{
		SubscriptionID:  p.Topic.SubscriptionID,
		Name:            "atlas-azure-activitylog",
		SystemTopicID:   "/subscriptions/x/resourceGroups/rg-atlas/providers/Microsoft.EventGrid/systemTopics/atlas-azure-activitylog",
		ActivityLogCats: []string{"Administrative"},
	}
	api := &fakeAPI{}
	if err := Provision(context.Background(), api, p); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := strings.Join(api.order, ","); got != "put-topic,put-sub,put-diag" {
		t.Errorf("order = %q; want topic,sub,diag", got)
	}
}

func TestProvision_Idempotent_ReRunSucceeds(t *testing.T) {
	t.Parallel()
	// The fake's PutSystemTopic / PutEventSubscription are upserts (no error on
	// re-call) — mirroring ARM PUT semantics. A second Provision must succeed.
	api := &fakeAPI{}
	for i := 0; i < 2; i++ {
		if err := Provision(context.Background(), api, goodPlan()); err != nil {
			t.Fatalf("Provision run %d: %v", i, err)
		}
	}
	if len(api.putTopic) != 2 || len(api.putSub) != 2 {
		t.Fatalf("re-run should re-issue upserts; got %d/%d", len(api.putTopic), len(api.putSub))
	}
}

func TestDeprovision_ReverseOrder(t *testing.T) {
	t.Parallel()
	p := goodPlan()
	p.IncludeDiagnostic = true
	p.Diagnostic = DiagnosticSetting{SubscriptionID: p.Topic.SubscriptionID, Name: "d", SystemTopicID: "/x"}
	api := &fakeAPI{}
	if err := Deprovision(context.Background(), api, p); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	if got := strings.Join(api.order, ","); got != "del-diag,del-sub,del-topic" {
		t.Errorf("teardown order = %q; want diag,sub,topic (reverse of provision)", got)
	}
}

func TestDeprovision_NoDiagnostic(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	if err := Deprovision(context.Background(), api, goodPlan()); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}
	if len(api.delDiag) != 0 {
		t.Errorf("diagnostic delete should not run when IncludeDiagnostic is false")
	}
	if got := strings.Join(api.order, ","); got != "del-sub,del-topic" {
		t.Errorf("order = %q", got)
	}
}

func TestProvision_NilAPI(t *testing.T) {
	t.Parallel()
	if err := Provision(context.Background(), nil, goodPlan()); err == nil {
		t.Fatal("want error on nil API")
	}
	if err := Deprovision(context.Background(), nil, goodPlan()); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestProvision_SubscriptionErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("arm 403")
	api := &fakeAPI{failPutSub: sentinel}
	err := Provision(context.Background(), api, goodPlan())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "event subscription") {
		t.Fatalf("want wrapped subscription error; got %v", err)
	}
}

func TestDeprovision_TopicErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("arm 500")
	api := &fakeAPI{failDelTop: sentinel}
	err := Deprovision(context.Background(), api, goodPlan())
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "system topic") {
		t.Fatalf("want wrapped topic delete error; got %v", err)
	}
}

func TestPlanValidate(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*Plan){
		"missing subscription":   func(p *Plan) { p.Topic.SubscriptionID = "" },
		"missing resource group": func(p *Plan) { p.Topic.ResourceGroup = "" },
		"missing topic name":     func(p *Plan) { p.Topic.Name = "" },
		"missing location":       func(p *Plan) { p.Topic.Location = "" },
		"missing sub name":       func(p *Plan) { p.Subscription.Name = "" },
		"missing webhook":        func(p *Plan) { p.Subscription.WebhookURL = "" },
		"missing delivery key":   func(p *Plan) { p.Subscription.DeliveryKey = "" },
		"non-https webhook":      func(p *Plan) { p.Subscription.WebhookURL = "http://insecure.example.com/x" },
	}
	for name, mutate := range cases {
		p := goodPlan()
		mutate(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	// Happy path validates.
	if err := goodPlan().Validate(); err != nil {
		t.Errorf("good plan should validate: %v", err)
	}
}

func TestPlanValidate_DiagnosticNameRequired(t *testing.T) {
	t.Parallel()
	p := goodPlan()
	p.IncludeDiagnostic = true
	// Diagnostic.Name left empty
	if err := p.Validate(); err == nil {
		t.Fatal("expected error: diagnostic name required when IncludeDiagnostic")
	}
}

func TestProvision_ValidatesBeforeAnyCall(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	p := goodPlan()
	p.Subscription.WebhookURL = "" // invalid
	if err := Provision(context.Background(), api, p); err == nil {
		t.Fatal("expected validation error")
	}
	if len(api.putTopic) != 0 {
		t.Error("no ARM call should be issued on an invalid plan (no half-provisioned state)")
	}
}

func TestDocumentedRBACActions_AllWriteOrDelete(t *testing.T) {
	t.Parallel()
	actions := DocumentedRBACActions()
	if len(actions) == 0 {
		t.Fatal("empty RBAC action list")
	}
	for _, a := range actions {
		low := strings.ToLower(a.Action)
		if !strings.HasSuffix(low, "/write") && !strings.HasSuffix(low, "/delete") {
			t.Errorf("action %q is neither write nor delete — provisioning actions must be explicitly privileged", a.Action)
		}
		if a.Why == "" {
			t.Errorf("action %q missing rationale", a.Action)
		}
	}
	// The two required surfaces must be present.
	joined := strings.ToLower(strings.Join(actionNames(actions), " "))
	if !strings.Contains(joined, "microsoft.eventgrid") {
		t.Error("missing Microsoft.EventGrid actions")
	}
	if !strings.Contains(joined, "microsoft.insights/diagnosticsettings") {
		t.Error("missing Microsoft.Insights/diagnosticSettings actions")
	}
}

func actionNames(as []RBACAction) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Action)
	}
	return out
}

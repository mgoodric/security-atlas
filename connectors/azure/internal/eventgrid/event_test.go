package eventgrid

import "testing"

func TestParseBatch_RoutesByResourceProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		id       string
		wantType ResourceType
	}{
		{"storage", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct1", ResourceStorage},
		{"aks", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/c1", ResourceAKS},
		{"nsg", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg1", ResourceNSG},
		{"keyvault", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv1", ResourceKeyVault},
		{"firewall", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Network/firewallPolicies/fp1", ResourceFirewall},
		{"entra", "/providers/Microsoft.Authorization/roleAssignments/ra1", ResourceEntra},
		{"case-insensitive", "/subscriptions/s1/providers/MICROSOFT.STORAGE/STORAGEACCOUNTS/acct1", ResourceStorage},
		{"unmapped-vm", "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1", ResourceUnknown},
		{"no-provider", "/subscriptions/s1/resourceGroups/rg", ResourceUnknown},
		{"empty", "", ResourceUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := routeResourceID(tc.id); got != tc.wantType {
				t.Fatalf("routeResourceID(%q) = %q, want %q", tc.id, got, tc.wantType)
			}
		})
	}
}

func TestParseBatch_ArrayDelivery(t *testing.T) {
	t.Parallel()
	body := []byte(`[
		{"eventType":"Microsoft.Resources.ResourceWriteSuccess","subject":"/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct1"},
		{"eventType":"Microsoft.Resources.ResourceWriteSuccess","subject":"/subscriptions/s1/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv1"}
	]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2", len(events))
	}
	if events[0].ResourceType != ResourceStorage || events[1].ResourceType != ResourceKeyVault {
		t.Fatalf("types = %q,%q", events[0].ResourceType, events[1].ResourceType)
	}
}

func TestParseBatch_SingleObjectDelivery(t *testing.T) {
	t.Parallel()
	body := []byte(`{"eventType":"Microsoft.EventGrid.SubscriptionValidationEvent","data":{"validationCode":"test-validation-code-001"}}`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(events) != 1 || !events[0].IsValidation() {
		t.Fatalf("want one validation event, got %+v", events)
	}
	if events[0].ValidationCode != "test-validation-code-001" {
		t.Fatalf("code = %q", events[0].ValidationCode)
	}
}

func TestParseBatch_ResourceURIFallback(t *testing.T) {
	t.Parallel()
	// No subject; data.resourceUri carries the changed id (Activity-Log shape).
	body := []byte(`[{"eventType":"Microsoft.Resources.ResourceWriteSuccess","data":{"resourceUri":"/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg1"}}]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if events[0].ResourceType != ResourceNSG {
		t.Fatalf("type = %q, want nsg", events[0].ResourceType)
	}
	if events[0].ResourceID == "" {
		t.Fatal("resource id not extracted from data.resourceUri")
	}
}

func TestParseBatch_Malformed(t *testing.T) {
	t.Parallel()
	if _, err := ParseBatch([]byte(`{not json`)); err == nil {
		t.Fatal("want error on malformed body")
	}
}

func TestSameResourceID_CaseInsensitive(t *testing.T) {
	t.Parallel()
	a := "/subscriptions/S1/providers/Microsoft.Storage/storageAccounts/Acct1"
	b := "/subscriptions/s1/providers/microsoft.storage/storageaccounts/acct1"
	if !SameResourceID(a, b) {
		t.Fatal("want case-insensitive match")
	}
	if SameResourceID(a, "/other") {
		t.Fatal("unexpected match")
	}
}

// TestParseBatch_NoFabrication asserts the parser takes ONLY the resource id +
// type from the event — a payload-only event carrying fabricated record-shaped
// fields contributes nothing but its (unmapped) id.
func TestParseBatch_NoFabrication_PayloadDiscarded(t *testing.T) {
	t.Parallel()
	body := []byte(`[{"eventType":"Microsoft.Resources.ResourceWriteSuccess","subject":"/subscriptions/s1/providers/Microsoft.Compute/virtualMachines/vm1","data":{"encryption_enabled":true,"https_traffic_only":true,"account_name":"forged"}}]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	// Unmapped provider → dropped; even if it were mapped, ParsedEvent has no field
	// to hold the forged account_name/encryption_enabled — they vanish at decode.
	if events[0].ResourceType != ResourceUnknown {
		t.Fatalf("type = %q, want unknown (vm is unmapped)", events[0].ResourceType)
	}
}

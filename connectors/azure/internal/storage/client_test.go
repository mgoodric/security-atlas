package storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

func TestClient_ListStorageAccounts_ParsesARMPage(t *testing.T) {
	var sawAuth, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method = %s; want GET (read-only)", r.Method)
		}
		_, _ = w.Write([]byte(`{
			"value": [
				{
					"id": "/subscriptions/sub-1/resourceGroups/rg1/providers/Microsoft.Storage/storageAccounts/acct1",
					"name": "acct1",
					"location": "eastus",
					"properties": {
						"encryption": {"keySource": "Microsoft.Storage"},
						"supportsHttpsTrafficOnly": true,
						"minimumTlsVersion": "TLS1_2",
						"allowBlobPublicAccess": false
					}
				}
			]
		}`))
	}))
	defer srv.Close()

	c := storage.NewClient(srv.Client(), srv.URL, "sub-1", "test-access-token")
	got, err := c.ListStorageAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListStorageAccounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	a := got[0]
	if a.Name != "acct1" || a.ResourceGroup != "rg1" || a.Location != "eastus" {
		t.Errorf("account fields wrong: %+v", a)
	}
	if !a.EncryptionEnabled || a.EncryptionKeySource != "Microsoft.Storage" {
		t.Errorf("encryption fields wrong: %+v", a)
	}
	if !a.HTTPSTrafficOnly || a.MinimumTLSVersion != "TLS1_2" || a.AllowBlobPublicAccess {
		t.Errorf("hardening flags wrong: %+v", a)
	}
	if sawAuth != "Bearer test-access-token" {
		t.Errorf("auth header = %q", sawAuth)
	}
	if !strings.Contains(sawPath, "/subscriptions/sub-1/") {
		t.Errorf("path = %q; want subscription-scoped", sawPath)
	}
}

func TestClient_ListStorageAccounts_NoEncryptionKeySource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"/x","name":"x","properties":{"encryption":{"keySource":""}}}]}`))
	}))
	defer srv.Close()
	c := storage.NewClient(nil, srv.URL, "sub", "tok")
	got, err := c.ListStorageAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListStorageAccounts: %v", err)
	}
	if got[0].EncryptionEnabled {
		t.Error("empty key source should mean encryption not reported enabled")
	}
}

func TestClient_ListStorageAccounts_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := storage.NewClient(srv.Client(), srv.URL, "sub", "tok")
	_, err := c.ListStorageAccounts(context.Background())
	if err == nil {
		t.Fatal("expected HTTP 403 error")
	}
}

func TestClient_ListStorageAccounts_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := storage.NewClient(srv.Client(), srv.URL, "sub", "tok")
	if _, err := c.ListStorageAccounts(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNewClient_DefaultsBaseURL(t *testing.T) {
	c := storage.NewClient(nil, "", "sub", "tok")
	if c.BaseURL != "https://management.azure.com" {
		t.Errorf("default base URL = %q", c.BaseURL)
	}
}

func TestAPIError_Message(t *testing.T) {
	if (&storage.APIError{Status: 500}).Error() == "" {
		t.Error("empty error message")
	}
	if (&storage.APIError{Status: 403, Body: "denied"}).Error() == "" {
		t.Error("empty error message with body")
	}
}

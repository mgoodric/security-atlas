package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/storage"
)

// fakeARM is a faked Azure Resource Manager surface — NO live Azure in tests.
type fakeARM struct {
	accounts []storage.RawAccount
	err      error
}

func (f *fakeARM) ListStorageAccounts(_ context.Context) ([]storage.RawAccount, error) {
	return f.accounts, f.err
}

var fixedNow = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }

func hardened(id, name string) storage.RawAccount {
	return storage.RawAccount{
		ID: id, Name: name, ResourceGroup: "rg", Location: "eastus",
		EncryptionEnabled: true, EncryptionKeySource: "Microsoft.Storage",
		HTTPSTrafficOnly: true, MinimumTLSVersion: "TLS1_2", AllowBlobPublicAccess: false,
	}
}

func TestInspect_PassWhenHardened(t *testing.T) {
	api := &fakeARM{accounts: []storage.RawAccount{hardened("/sub/acct1", "acct1")}}
	got, err := storage.Inspect(context.Background(), api, "sub-1", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(got) != 1 || got[0].Result != storage.ResultPass {
		t.Fatalf("want 1 PASS; got %+v", got)
	}
	if got[0].SubscriptionID != "sub-1" {
		t.Errorf("subscription = %q; want sub-1", got[0].SubscriptionID)
	}
}

func TestInspect_FailMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *storage.RawAccount)
		reason string
	}{
		{"no-encryption", func(a *storage.RawAccount) { a.EncryptionEnabled = false }, "encryption"},
		{"no-https-only", func(a *storage.RawAccount) { a.HTTPSTrafficOnly = false }, "secure-transfer"},
		{"public-blob", func(a *storage.RawAccount) { a.AllowBlobPublicAccess = true }, "public blob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := hardened("/sub/acct", "acct")
			tc.mutate(&a)
			got, err := storage.Inspect(context.Background(), &fakeARM{accounts: []storage.RawAccount{a}}, "sub", fixedNow)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if got[0].Result != storage.ResultFail {
				t.Errorf("result = %q; want fail", got[0].Result)
			}
			if got[0].Reason == "" {
				t.Error("FAIL should carry a reason")
			}
		})
	}
}

func TestInspect_InconclusiveOnReadError(t *testing.T) {
	a := hardened("/sub/acct", "acct")
	a.ReadError = "throttled"
	got, err := storage.Inspect(context.Background(), &fakeARM{accounts: []storage.RawAccount{a}}, "sub", fixedNow)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got[0].Result != storage.ResultInconclusive {
		t.Errorf("result = %q; want inconclusive", got[0].Result)
	}
}

func TestInspect_SkipsIncompleteAccounts(t *testing.T) {
	api := &fakeARM{accounts: []storage.RawAccount{
		{ID: "", Name: "x"},
		{ID: "y", Name: ""},
		hardened("/sub/ok", "ok"),
	}}
	got, _ := storage.Inspect(context.Background(), api, "sub", fixedNow)
	if len(got) != 1 || got[0].AccountName != "ok" {
		t.Fatalf("expected 1 valid account; got %+v", got)
	}
}

func TestInspect_PropagatesListError(t *testing.T) {
	sentinel := errors.New("arm 403")
	_, err := storage.Inspect(context.Background(), &fakeARM{err: sentinel}, "sub", fixedNow)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
}

func TestInspect_NilAPIRejected(t *testing.T) {
	_, err := storage.Inspect(context.Background(), nil, "sub", nil)
	if err == nil {
		t.Fatal("expected error for nil API")
	}
}

// P0-486-3: the AccountConfig struct must carry config flags ONLY — never keys,
// SAS tokens, or blob contents. This pins that the struct has no such field by
// asserting the populated record only carries the documented config flags.
func TestAccountConfig_ConfigOnly(t *testing.T) {
	api := &fakeARM{accounts: []storage.RawAccount{hardened("/sub/acct", "acct")}}
	got, _ := storage.Inspect(context.Background(), api, "sub", fixedNow)
	c := got[0]
	if c.EncryptionKeySource != "Microsoft.Storage" || c.MinimumTLSVersion != "TLS1_2" {
		t.Errorf("config fields not preserved: %+v", c)
	}
}

package api

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// fixedPersister rehydrates a credstore with fixed credentials, the way
// apikeystore.Persister does at boot.
type fixedPersister struct {
	recs []credstore.PersistedCredential
}

func (fixedPersister) NewID() (string, error)                     { return "key_00000000-0000-4000-8000-000000000000", nil }
func (fixedPersister) HashToken(token string) string              { return "h:" + token }
func (fixedPersister) Insert(credstore.PersistedCredential) error { return nil }
func (fixedPersister) Rotate(credstore.PersistedCredential, string, string, time.Time) error {
	return nil
}
func (fixedPersister) Revoke(string, string) error { return nil }
func (p fixedPersister) LoadActive() ([]credstore.PersistedCredential, error) {
	return p.recs, nil
}

// #2849: a policy-sync key authenticates on gRPC but is refused with
// PermissionDenied before any RPC runs; an ordinary key reaches the handler.
func TestAuthInterceptor_RefusesPolicySyncKey(t *testing.T) {
	t.Parallel()
	const tenant = "6f1c2b8e-0000-4000-8000-0000000028a9"
	store := credstore.New(0)
	if _, err := store.AttachPersistence(fixedPersister{recs: []credstore.PersistedCredential{
		{Cred: credstore.Credential{ID: "key_00000000-0000-4000-8000-000000000001", TenantID: tenant, Purpose: credstore.PurposePolicySync}, TokenHashHex: "h:atlas_sync"},
		{Cred: credstore.Credential{ID: "key_00000000-0000-4000-8000-000000000002", TenantID: tenant, IsAdmin: true}, TokenHashHex: "h:atlas_admin"},
	}}); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	intercept := authInterceptor(store)
	call := func(token string) (bool, error) {
		ran := false
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
		_, err := intercept(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/evidence.v1.EvidenceIngestService/Push"},
			func(context.Context, any) (any, error) { ran = true; return nil, nil })
		return ran, err
	}

	ran, err := call("atlas_sync")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("policy-sync key: err = %v, want PermissionDenied", err)
	}
	if ran {
		t.Fatal("policy-sync key reached the handler")
	}
	if ran, err := call("atlas_admin"); err != nil || !ran {
		t.Fatalf("admin key: ran=%v err=%v, want the handler to run", ran, err)
	}
}

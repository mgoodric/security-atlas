package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/mgoodric/security-atlas/internal/api"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

// TestBuildRecord_PushesWithScopeAndProvenance asserts that a record built
// from a (BucketState, Identity) pair pushes successfully through the
// platform's Push API and arrives with scope + source_attribution as the
// ACs require.
func TestBuildRecord_PushesWithScopeAndProvenance(t *testing.T) {
	srv := api.New(api.Config{RotationGrace: time.Hour})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = lis.Close()
	})

	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}

	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := sdk.NewClientFromConn(conn, bearer)

	state := awss3.BucketState{
		BucketARN:    "arn:aws:s3:::prod-vault",
		BucketName:   "prod-vault",
		BucketRegion: "us-east-1",
		Result:       awss3.ResultPass,
		Algorithm:    "AES256",
	}
	identity := awsauth.Identity{AccountID: "111122223333", Environment: "prod", Source: "flag"}

	rec, err := buildRecord(state, identity, "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildRecord: %v", err)
	}

	receipt, err := client.Push(context.Background(), rec)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if receipt.GetHash() == "" {
		t.Fatal("receipt.hash empty")
	}

	if rec.GetSourceAttribution().GetActorType() != "connector" {
		t.Errorf("actor_type = %q; want connector", rec.GetSourceAttribution().GetActorType())
	}
	if rec.GetSourceAttribution().GetActorId() == "" || rec.GetSourceAttribution().GetActorId() == "connector:aws:s3@" {
		t.Errorf("actor_id = %q; want connector:aws:s3@<version>", rec.GetSourceAttribution().GetActorId())
	}

	found := map[string]string{}
	for _, d := range rec.GetScope() {
		if len(d.GetValues()) > 0 {
			found[d.GetKey()] = d.GetValues()[0]
		}
	}
	if got := found["cloud_account"]; got != "aws:111122223333" {
		t.Errorf("cloud_account = %q; want aws:111122223333", got)
	}
	if got := found["environment"]; got != "prod" {
		t.Errorf("environment = %q; want prod", got)
	}
}

// TestBuildRecord_DedupesWithinHour verifies AC-6: two records built from
// the same bucket within the same hour share an idempotency_key, so the
// platform's dedup returns the same receipt.
func TestBuildRecord_DedupesWithinHour(t *testing.T) {
	srv := api.New(api.Config{RotationGrace: time.Hour})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(func() { srv.GRPC.GracefulStop(); _ = lis.Close() })

	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := sdk.NewClientFromConn(conn, bearer)

	state := awss3.BucketState{
		BucketARN: "arn:aws:s3:::prod-vault", BucketName: "prod-vault", BucketRegion: "us-east-1",
		Result: awss3.ResultPass, Algorithm: "AES256",
	}
	identity := awsauth.Identity{AccountID: "111122223333", Environment: "prod", Source: "flag"}

	first, err := buildRecord(state, identity, "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildRecord first: %v", err)
	}
	second, err := buildRecord(state, identity, "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildRecord second: %v", err)
	}

	r1, err := client.Push(context.Background(), first)
	if err != nil {
		t.Fatalf("first Push: %v", err)
	}
	r2, err := client.Push(context.Background(), second)
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if r1.GetRecordId() != r2.GetRecordId() {
		t.Fatalf("dedup failed: %q vs %q", r1.GetRecordId(), r2.GetRecordId())
	}
}

# sdk-go

The security-atlas Go push SDK. A small, stable wrapper around the generated
`EvidenceIngestService` gRPC client. This is the SDK the platform itself
dogfoods and the one every Go connector pushes through.

The canonical inbound wire surface is a single RPC: `Push(record) → Receipt`.
Connectors retrieve data from their source however they like (pull /
subscribe / push profile), but the platform-side surface is always `Push`.

## Install

```sh
go get github.com/mgoodric/security-atlas/pkg/sdk-go
```

## Push-record quickstart

Construct a client, build an evidence record, call `Push`, handle the
`Receipt`:

```go
package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

func main() {
	// 1. Construct a client. The bearer is the token issued by the
	//    platform's AdminCredentials.Issue; it is sent on every RPC.
	//    TLS (system roots) is the default; pass placeholders, never a
	//    real endpoint or token, into source control.
	client, err := sdk.NewClient(
		"platform.example.com:443",
		"<SECURITY_ATLAS_TOKEN>",
	)
	if err != nil {
		log.Fatalf("new client: %v", err)
	}
	defer client.Close()

	// 2. Build an evidence record.
	observed := time.Now().UTC().Truncate(time.Hour)
	payload, err := structpb.NewStruct(map[string]any{
		"bucket_name": "example-bucket",
		"algorithm":   "aws:kms",
	})
	if err != nil {
		log.Fatalf("payload: %v", err)
	}

	record := &evidencev1.EvidenceRecord{
		IdempotencyKey: "example-key-2026-06-03T00:00:00Z",
		EvidenceKind:   "aws.s3.bucket_encryption_state.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:CRY-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"aws:<ACCOUNT_ID>"}},
			{Key: "environment", Values: []string{"prod"}},
		},
		ObservedAt: timestamppb.New(observed),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   "connector:example:demo@dev",
		},
	}

	// 3. Push. The 10s timeout bounds the RPC.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	receipt, err := client.Push(ctx, record)
	if err != nil {
		log.Fatalf("push: %v", err)
	}

	// 4. Handle the Receipt.
	log.Printf("pushed record_id=%s", receipt.GetRecordId())
}
```

## API surface

| Symbol                                                                    | Purpose                                                                                    |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `sdk.NewClient(endpoint, bearer string, opts ...Option) (*Client, error)` | Dial `endpoint` and prepare a push client. Returns an error on an empty bearer.            |
| `(*Client).Push(ctx, *EvidenceRecord) (*EvidenceReceipt, error)`          | Send one evidence record. Wraps gRPC errors so callers can `errors.As` a status.           |
| `(*Client).Close() error`                                                 | Release the underlying gRPC connection when the client owns it.                            |
| `sdk.WithTLSConfig(*tls.Config) Option`                                   | Override the default TLS configuration (default: system roots, TLS 1.2 floor).             |
| `sdk.WithInsecure() Option`                                               | Disable TLS — accepted **only** for loopback endpoints; refuses non-loopback.              |
| `sdk.NewClientFromConn(*grpc.ClientConn, bearer string) *Client`          | Build a client around an existing conn (typical in `bufconn` tests). `Close()` is a no-op. |
| `sdk.MetadataAuthorization`, `sdk.BearerPrefix`                           | The gRPC metadata key and bearer prefix the client appends to every RPC.                   |

## Security notes

- The bearer token is required (`NewClient` rejects an empty bearer) and is
  sent as gRPC `authorization: Bearer <token>` metadata on every call.
- `WithInsecure` is loopback-only by design: it refuses any non-loopback
  endpoint to prevent accidental plaintext on the wire.
- Use placeholder endpoints and tokens in any committed sample — never a real
  platform URL or bearer.

## Tests

```sh
go test ./pkg/sdk-go/...
```

The branch-level tests exercise option validation (empty bearer, the
loopback / non-loopback `WithInsecure` decision, the default-TLS and
`WithTLSConfig` paths) without dialing the network — `grpc.NewClient` is a
lazy constructor in grpc-go v1.59+, so the option branches run without a live
server.

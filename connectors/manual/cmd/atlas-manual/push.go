package main

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// schemaVersion is the manual.upload.v1 schema every path emits. 1.1.0
// declares the per-path source fields (#2940); 1.0.0 rejects them.
const schemaVersion = "1.1.0"

// recordPusher is the one sdk.Client method the push loops use.
type recordPusher interface {
	Push(ctx context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
}

// pushRecord pushes rec and reports whether the platform already held a
// record under its idempotency key.
//
// Every key this connector builds names one version of one source item
// (a CSV row's content, an S3 ETag, an SFTP mtime) and nothing about the
// run. The ledger's content hash covers observed_at, which is the run
// hour, so re-pushing an unchanged item in a later hour reuses the key
// with a different hash and ingest answers AlreadyExists. For these keys
// that answer means "this version of the item is already in the ledger",
// so the run skips it and the first push's observed_at stands (#2940).
// Before #2940 the S3 and SFTP runs aborted on it instead.
func pushRecord(ctx context.Context, client recordPusher, rec *evidencev1.EvidenceRecord) (alreadyIngested bool, err error) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = client.Push(pctx, rec)
	if status.Code(err) == codes.AlreadyExists {
		return true, nil
	}
	return false, err
}

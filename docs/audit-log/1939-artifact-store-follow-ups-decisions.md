# security-atlas#1939 — artifact store follow-ups — decisions log

Follow-ups from the #1938 review (#1928): Helm public endpoint and
path-style, streaming uploads, walkthrough orphan blobs.

- detection_tier_actual: manual_review
- detection_tier_target: integration

## D1 — two passes over a seekable body, not one hashing reader into PutObject

`Store.PutReader` reads the multipart part twice: once through sha256
behind a `MaxUploadBytes+1` limit, then again into `PutObject`. A single
pass, with a hashing reader as the `PutObject` body, was rejected:

- The SDK signs a seekable body by reading and rewinding it. A
  non-seekable wrapper forces it to buffer or to unsigned trailing
  checksums, which plain-HTTP MinIO (`http://minio:9000`) refuses.
- The hash would only be known after the S3 write, so dedup would turn
  into write-then-delete and the cap would be enforced after the bytes
  had landed in the bucket.

The part is already seekable: `ParseMultipartForm` keeps up to
`MaxMultipartMemory` (8 MiB) in memory and spools the rest to a temp
file, so `multipart.File` is an `io.ReadSeeker`. The per-request memory
bound is that 8 MiB plus a copy buffer, not the 100 MiB upload. The cap
is enforced while streaming: the first pass stops after cap+1 bytes.

`Put([]byte)` stays for callers that hold the bytes already, and shares
the dedup / S3 / insert tail with `PutReader`.

## D2 — validate the walkthrough first, rather than delete the blob on failure

The ticket offered either. Deleting is unsafe: `Put` dedups by content
hash, so the returned artifact may be an existing row that other records
reference, and deleting its blob would break them. Deleting the
`artifacts` row too would need a new store method and still race with
another uploader of the same bytes.

`walkthrough.Store.CheckAttachable` runs the same not-found / finalized /
frozen-period checks `AddAttachment` runs, through a shared
`attachableRow` helper, and the handler calls it before storing the blob.
`AddAttachment` repeats the checks in its own transaction. A walkthrough
finalized between the two calls still leaves an artifact nothing
attaches. That artifact is valid, tenant-scoped and audit-logged, not a
dangling reference, and the window is one request.

## D3 — Helm `forcePathStyle` is tri-state

`artifacts.forcePathStyle` defaults to empty, which renders no
`ARTIFACTS_S3_FORCE_PATH_STYLE` and leaves the server's default
(path-style whenever an endpoint is set) in charge. `true` / `false`
render verbatim, and any other value fails the render.
`values-production.yaml` sets `false`, because its example endpoint is
AWS S3, which uses virtual-hosted addressing.

`artifacts.s3PublicEndpoint` defaults to empty, because the chart cannot
know the browser-facing MinIO address. NOTES.txt warns when bundled
MinIO is on and the value is unset.

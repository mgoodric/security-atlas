# Decisions log — #1906: recovery key follow-ups

- detection_tier_actual: manual_review
- detection_tier_target: unit

Ticket: security-atlas#1906, from the #1893 and #1917 reviews.

## D1 — `mark-first-signin` requires an admin credential

Any authenticated bearer could call `POST /v1/install/mark-first-signin`.
After a reset, a low-privilege key calling it first would flip the marker
and revoke the recovery key, leaving the operator with no way back in. The
handler now refuses with 403 when the caller is not admin, and with 401
when there is no credential. Both happen before the marker is written.

The ticket allowed "an admin credential, or the recovery key itself". The
recovery key is minted admin, and a super_admin JWT bridges to `IsAdmin`, so
`cred.IsAdmin` covers both without a separate purpose check.

A non-admin first sign-in no longer flips `first_signin_at`. The login page
keeps showing the fresh-install hint until an admin signs in. That is the
intended effect: the marker gates a recovery path, so only the operator
should close it. The web `signIn` action already ignores the call's result,
so the sign-in itself is unaffected.

The bootstrap-token file is deleted on that same first flip. After a
non-admin first sign-in, the file, which holds an admin bearer, stays on
disk until an admin signs in. That is an accepted trade-off: the file
was already on disk for the whole fresh-install period, under the
data-dir permissions, and deleting it on a non-admin call would need the
marker flip this change refuses.

## D2 — gRPC `admin.Issue` refuses a negative TTL

`durationpb.AsDuration` passes a negative duration through. The RPC now
returns `InvalidArgument` for `ttl < 0`, matching the HTTP path's
`ttl_seconds >= 0`. An absent or zero TTL still means no expiry. No upper
bound: `AsDuration` saturates instead of wrapping (#1914 D3).

## D3 — No automatic change for keys minted between #1886 and #1893

The runbook [`docs/runbooks/pre-1893-recovery-keys.md`](../runbooks/pre-1893-recovery-keys.md)
lists them. No CLI step marks or revokes them, for three reasons:

- The columns cannot separate them from the `ATLAS_BOOTSTRAP_TENANT` boot
  key, which is also admin, unattributed and non-expiring. Only timing
  against the platform log separates them, and that needs a human.
- Marking one `bootstrap-recovery` would not bound it. Its TTL stays 0, so
  it would only become revocable by the next reset or first sign-in. That
  sign-in may never come on an install that already signed in.
- The window was about two hours on `main`. Once the operator has picked
  the rows, `atlas-cli credentials revoke --id <id>` already revokes them.

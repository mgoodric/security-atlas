# Slice 385 — decisions log

## Decisions made

- **D1: Strip the prefix in `resolveActor` rather than changing the JWT subject
  format.** The `Subject = "user:<uuid>"` convention is established across the
  auth substrate (LocalLogin, PKCE, device-code grants) and consumed by jwtmw.
  Changing the subject format would ripple across the whole auth surface and
  risk token-compat regressions. The demo handler was simply the one consumer
  that parsed the subject naively. Confidence: high.
- **D2: Apply the strip to the credstore fallback too.** jwtmw sets
  `cred.UserID = claims.Subject`, so the fallback carries the same prefix; a
  genuine legacy bare-UUID credential survives `TrimPrefix` unchanged.
  Confidence: high.
- **D3: Local helper `subjectUserID`, not a shared util.** Only this handler
  parses the subject into a bare UUID today. Promoting to a shared package can
  happen if a second consumer appears. Confidence: medium.
- **D4: Did not change the masked-500 behavior in this PR.** `resolveActor`
  failure is a genuine internal error (500 is correct). The separately-requested
  "map demoseed business-rule refusals to 409" improvement is orthogonal to this
  bug (no demo tenant existed) and is left for a follow-up so this hotfix stays
  surgical. Confidence: high.

## Revisit once in use

- After deploy to `atlas-edge`, confirm the demo seed completes (it may surface
  a downstream issue the actor-resolution 500 was masking; the demo writers do
  not touch the drifted metrics objects, so this is not expected).

## Confidence

High that this resolves the reported 500. The fix matches the exact subject
shape observed in the live reproduction log.

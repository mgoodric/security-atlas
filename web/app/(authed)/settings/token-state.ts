// Slice 103 -- personal-API-token plaintext-once state machine.
//
// Encodes the P0-A2 invariant: the bearer plaintext returned by POST
// /v1/admin/credentials (slice 034) is shown to the user EXACTLY ONCE
// in a callout, then never re-displayed by the UI.
//
// The reducer holds the plaintext for the duration of the `issued`
// state and discards it on DISMISS. A second ISSUED replaces the prior
// bearer wholesale -- no history, no undo, no leakage between issuance
// events.
//
// This is the slice 062/063 admin-creds pattern carried verbatim. See
// `web/app/admin/api-keys/page.tsx` for the equivalent component-local
// version that this module abstracts so unit tests can lock it down.

export type FreshTokenState =
  | { kind: "none" }
  | {
      kind: "issued";
      bearer: string;
      last4: string;
      issued_at: string;
    };

export type TokenAction =
  | {
      kind: "ISSUED";
      bearer: string;
      last4: string;
      issued_at: string;
    }
  | { kind: "DISMISS" };

export const initialState: FreshTokenState = { kind: "none" };

export function reduce(
  state: FreshTokenState,
  action: TokenAction,
): FreshTokenState {
  switch (action.kind) {
    case "ISSUED":
      return {
        kind: "issued",
        bearer: action.bearer,
        last4: action.last4,
        issued_at: action.issued_at,
      };
    case "DISMISS":
      return { kind: "none" };
    default:
      // Defensive: an unrecognized action returns the prior state
      // unchanged. The TokenAction union is closed, so this branch is
      // unreachable under correct typing -- guards against version
      // skew where a future action type is sent to an older bundle.
      return state;
  }
}

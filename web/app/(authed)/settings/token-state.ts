// Slice 103 -- personal-API-token plaintext-once state machine.
// Slice 163 -- extended with a ROTATED transition wired against the
// slice 062 POST /v1/admin/credentials/:id/rotate endpoint.
//
// Encodes the P0-A2 / P0-163-1 invariant: the bearer plaintext returned
// by POST /v1/admin/credentials (slice 034) and POST .../:id/rotate
// (slice 062) is shown to the user EXACTLY ONCE in a callout, then
// never re-displayed by the UI.
//
// The reducer holds the plaintext for the duration of an `issued` or
// `rotated` state and discards it on DISMISS. Any subsequent
// ISSUED/ROTATED replaces the prior bearer wholesale -- no history, no
// undo, no leakage between issuance events, regardless of which path
// produced the prior bearer (P0-163-1 says rotate's plaintext-once
// applies symmetrically with issue's).
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
    }
  | {
      kind: "rotated";
      bearer: string;
      last4: string;
      // The predecessor's last 4 -- copied from the AdminCredential row
      // that triggered the rotate so the callout can render the
      // "rotated from ...XXXX" context.
      predecessor_last4: string;
      // The deadline at which the predecessor stops accepting
      // authenticated calls (slice 062 returns this from Rotate as the
      // backend-set rotationGrace window).
      predecessor_expires_at: string;
    };

export type TokenAction =
  | {
      kind: "ISSUED";
      bearer: string;
      last4: string;
      issued_at: string;
    }
  | {
      kind: "ROTATED";
      bearer: string;
      last4: string;
      predecessor_last4: string;
      predecessor_expires_at: string;
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
    case "ROTATED":
      return {
        kind: "rotated",
        bearer: action.bearer,
        last4: action.last4,
        predecessor_last4: action.predecessor_last4,
        predecessor_expires_at: action.predecessor_expires_at,
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

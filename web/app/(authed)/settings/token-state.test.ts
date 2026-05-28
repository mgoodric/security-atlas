// Slice 103 -- vitest unit coverage for the personal-API-token
// plaintext-once state machine.
//
// Slice 163 -- extended to cover the ROTATED transition. The reducer
// holds a single bearer plaintext at a time across both ISSUED and
// ROTATED paths; the plaintext-once invariant (P0-163-1) applies
// symmetrically: DISMISS clears, and any new ISSUED or ROTATED
// overwrites the prior bearer regardless of which path produced it.
//
// The state machine encodes P0-A2: the bearer plaintext returned by
// POST /v1/admin/credentials is shown to the user EXACTLY ONCE in a
// callout, then never re-displayed by the UI. Component state holds
// the bearer; if the user dismisses the callout or navigates away, the
// state transitions back to `none` and the bearer is gone.
//
// This is a pure reducer test -- no React, no DOM, no fetch.

import { describe, expect, test } from "vitest";

import {
  TEST_BEARER_A,
  TEST_BEARER_B,
} from "../../../lib/test-utils/test-tokens";
import {
  initialState,
  reduce,
  type FreshTokenState,
  type TokenAction,
} from "./token-state";

describe("FreshTokenState reducer", () => {
  test("initial state is none", () => {
    expect(initialState).toEqual<FreshTokenState>({ kind: "none" });
  });

  test("ISSUED action carries the bearer plaintext into the issued state", () => {
    const out = reduce(initialState, {
      kind: "ISSUED",
      bearer: "test-plaintext-bearer-value",
      last4: "abcd",
      issued_at: "2026-05-16T00:00:00Z",
    });
    expect(out).toEqual<FreshTokenState>({
      kind: "issued",
      bearer: "test-plaintext-bearer-value",
      last4: "abcd",
      issued_at: "2026-05-16T00:00:00Z",
    });
  });

  test("DISMISS clears the bearer from state", () => {
    const issued: FreshTokenState = {
      kind: "issued",
      bearer: "test-plaintext-bearer-value",
      last4: "abcd",
      issued_at: "2026-05-16T00:00:00Z",
    };
    const out = reduce(issued, { kind: "DISMISS" });
    expect(out).toEqual<FreshTokenState>({ kind: "none" });
    // Belt-and-suspenders: the result MUST NOT contain the plaintext
    // anywhere, even as a leftover field. JSON.stringify catches any
    // surviving reference.
    expect(JSON.stringify(out)).not.toContain("test-plaintext-bearer-value");
  });

  test("DISMISS from none stays none (idempotent)", () => {
    const out = reduce(initialState, { kind: "DISMISS" });
    expect(out).toEqual<FreshTokenState>({ kind: "none" });
  });

  test("a second ISSUED replaces the prior bearer (rotation case)", () => {
    const first = reduce(initialState, {
      kind: "ISSUED",
      bearer: "test-old-bearer",
      last4: "old4",
      issued_at: "2026-05-15T00:00:00Z",
    });
    const second = reduce(first, {
      kind: "ISSUED",
      bearer: "test-new-bearer",
      last4: "new4",
      issued_at: "2026-05-16T00:00:00Z",
    });
    // The new state holds the new bearer; the old bearer MUST NOT
    // appear anywhere in the new state. If the reducer ever stashed
    // the prior value (e.g. for "undo"), this would catch it.
    expect(second).toEqual<FreshTokenState>({
      kind: "issued",
      bearer: "test-new-bearer",
      last4: "new4",
      issued_at: "2026-05-16T00:00:00Z",
    });
    expect(JSON.stringify(second)).not.toContain("test-old-bearer");
  });

  test("reducer is exhaustive: an unknown action type is a no-op", () => {
    // The reducer is typed to forbid unknown actions, but defensively
    // it returns the prior state if one slips through (e.g. from a
    // version skew between client and server).
    const out = reduce(initialState, {
      kind: "UNKNOWN" as TokenAction["kind"],
    } as unknown as TokenAction);
    expect(out).toBe(initialState);
  });
});

describe("plaintext-once invariant under realistic flow", () => {
  test("Issue -> render -> Dismiss leaves no plaintext in state", () => {
    let state = initialState;
    state = reduce(state, {
      kind: "ISSUED",
      bearer: "test-very-sensitive-plaintext",
      last4: "ef99",
      issued_at: "2026-05-16T00:00:00Z",
    });
    // While in `issued` state the UI renders the plaintext. The
    // contract is: the plaintext exists in state for as long as the
    // callout is open. The instant the user dismisses, state == none.
    expect(state.kind).toBe("issued");
    state = reduce(state, { kind: "DISMISS" });
    expect(state.kind).toBe("none");
    expect(JSON.stringify(state)).not.toContain(
      "test-very-sensitive-plaintext",
    );
  });

  test("Issue -> Issue (rotation without dismiss) overwrites bearer", () => {
    let state = initialState;
    state = reduce(state, {
      kind: "ISSUED",
      bearer: TEST_BEARER_A,
      last4: "aaaa",
      issued_at: "2026-05-15T00:00:00Z",
    });
    state = reduce(state, {
      kind: "ISSUED",
      bearer: TEST_BEARER_B,
      last4: "bbbb",
      issued_at: "2026-05-16T00:00:00Z",
    });
    // Only the most-recent plaintext survives in state. The prior
    // one is GC'd.
    expect(JSON.stringify(state)).not.toContain(TEST_BEARER_A);
    expect(JSON.stringify(state)).toContain(TEST_BEARER_B);
  });
});

// Slice 163 -- ROTATED transition coverage.
//
// The rotate path (POST /v1/admin/credentials/:id/rotate, slice 062)
// returns a successor bearer plaintext + the predecessor's retirement
// deadline. The reducer holds these in a kind="rotated" variant which
// the FreshTokenCallout discriminates on to render rotate-flavour copy
// (predecessor retiring at {predecessor_expires_at}, successor last 4
// is {last4}). P0-163-1 says the plaintext-once invariant for ROTATED
// matches ISSUED: bearer cleared on DISMISS, and any second
// ROTATED/ISSUED replaces the prior bearer wholesale.
describe("FreshTokenState reducer -- ROTATED transition", () => {
  test("ROTATED from none carries the successor bearer + predecessor metadata", () => {
    const out = reduce(initialState, {
      kind: "ROTATED",
      bearer: "test-rotated-successor-bearer",
      last4: "succ",
      predecessor_last4: "pred",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    });
    expect(out).toEqual<FreshTokenState>({
      kind: "rotated",
      bearer: "test-rotated-successor-bearer",
      last4: "succ",
      predecessor_last4: "pred",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    });
  });

  test("ROTATED -> DISMISS clears bearer (plaintext-once invariant)", () => {
    const rotated: FreshTokenState = {
      kind: "rotated",
      bearer: "test-rotated-successor-bearer",
      last4: "succ",
      predecessor_last4: "pred",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    };
    const out = reduce(rotated, { kind: "DISMISS" });
    expect(out).toEqual<FreshTokenState>({ kind: "none" });
    // Belt-and-suspenders: the result MUST NOT contain the plaintext
    // anywhere, even as a leftover field.
    expect(JSON.stringify(out)).not.toContain("test-rotated-successor-bearer");
  });

  test("ROTATED -> ROTATED (rotate twice) replaces prior bearer", () => {
    let state = initialState;
    state = reduce(state, {
      kind: "ROTATED",
      bearer: "test-rotated-bearer-first",
      last4: "rot1",
      predecessor_last4: "pre1",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    });
    state = reduce(state, {
      kind: "ROTATED",
      bearer: "test-rotated-bearer-second",
      last4: "rot2",
      predecessor_last4: "pre2",
      predecessor_expires_at: "2026-05-24T00:00:00Z",
    });
    expect(state).toEqual<FreshTokenState>({
      kind: "rotated",
      bearer: "test-rotated-bearer-second",
      last4: "rot2",
      predecessor_last4: "pre2",
      predecessor_expires_at: "2026-05-24T00:00:00Z",
    });
    expect(JSON.stringify(state)).not.toContain("test-rotated-bearer-first");
  });

  test("ISSUED -> ROTATED clears the prior ISSUED bearer", () => {
    let state = initialState;
    state = reduce(state, {
      kind: "ISSUED",
      bearer: "test-issued-bearer-before-rotate",
      last4: "iss1",
      issued_at: "2026-05-15T00:00:00Z",
    });
    state = reduce(state, {
      kind: "ROTATED",
      bearer: "test-rotated-bearer-after-issue",
      last4: "rot1",
      predecessor_last4: "iss1",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    });
    expect(state.kind).toBe("rotated");
    // The ISSUED bearer is GONE; only the rotated bearer survives.
    expect(JSON.stringify(state)).not.toContain(
      "test-issued-bearer-before-rotate",
    );
    expect(JSON.stringify(state)).toContain("test-rotated-bearer-after-issue");
  });

  test("ROTATED -> ISSUED clears the prior ROTATED bearer", () => {
    let state = initialState;
    state = reduce(state, {
      kind: "ROTATED",
      bearer: "test-rotated-bearer-before-issue",
      last4: "rot1",
      predecessor_last4: "pre1",
      predecessor_expires_at: "2026-05-23T00:00:00Z",
    });
    state = reduce(state, {
      kind: "ISSUED",
      bearer: "test-issued-bearer-after-rotate",
      last4: "iss1",
      issued_at: "2026-05-16T00:00:00Z",
    });
    expect(state.kind).toBe("issued");
    expect(JSON.stringify(state)).not.toContain(
      "test-rotated-bearer-before-issue",
    );
    expect(JSON.stringify(state)).toContain("test-issued-bearer-after-rotate");
  });
});

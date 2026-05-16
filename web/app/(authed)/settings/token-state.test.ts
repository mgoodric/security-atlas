// Slice 103 -- vitest unit coverage for the personal-API-token
// plaintext-once state machine.
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
      bearer: "test-bearer-a",
      last4: "aaaa",
      issued_at: "2026-05-15T00:00:00Z",
    });
    state = reduce(state, {
      kind: "ISSUED",
      bearer: "test-bearer-b",
      last4: "bbbb",
      issued_at: "2026-05-16T00:00:00Z",
    });
    // Only the most-recent plaintext survives in state. The prior
    // one is GC'd.
    expect(JSON.stringify(state)).not.toContain("test-bearer-a");
    expect(JSON.stringify(state)).toContain("test-bearer-b");
  });
});

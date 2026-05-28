// web/lib/test-utils/test-tokens.ts
//
// Slice 348 V-4 — Centralized vitest test-bearer literals.
//
// Test-bearer placeholders were scattered across ~30 vitest files
// (`test-bearer-094`, `test-bearer-263`, `test-bearer-fixture`,
// `test-bearer-value`, `test-bearer-a`, `test-bearer-b`). Slice 334
// V-4 (Low) called the centralization out as a single edit point so:
//
//   1. The slice 069 P0-A9 / GitGuardian neutral-token policy has ONE
//      place to enforce (no vendor-prefixed token strings — `ghp_*`,
//      `sk_*`, `gho_*`, `eyJ*`, `AKIA*` — even as test placeholders;
//      GitGuardian scans test files too).
//   2. A future migration (slice 197 / 201 was painful — the static
//      bearer literal cascaded across many files) has one canonical
//      location.
//
// Convention: ALL test bearers in this module use the
// `test-bearer-NNNN` shape. The NNNN portion is informational only —
// the historical slice-numbered shapes (-094, -263, etc.) carry no
// runtime meaning but help track which slice first introduced a
// particular test fixture.
//
// Adding a new bearer here:
//   1. Use the `test-bearer-NNNN` shape.
//   2. Do NOT use vendor token prefixes. GitGuardian scans test files.
//   3. Prefer reusing one of the existing constants below before
//      adding a new one — fewer literals == easier future migrations.

/**
 * Default test bearer for new tests. Use this unless you specifically
 * need to distinguish bearers across multiple call sites in the same
 * test (e.g. a token-rotation test that asserts two different bearer
 * values do not collide).
 */
export const TEST_BEARER_DEFAULT = "test-bearer-default";

/**
 * Original slice-094 test bearer — used in calendar BFF tests.
 * Migrating these is mechanical; reach for `TEST_BEARER_DEFAULT` in
 * any new test.
 */
export const TEST_BEARER_094 = "test-bearer-094";

/**
 * Slice-263 test bearer — used across the questionnaires BFF tests.
 * Same migration note as above.
 */
export const TEST_BEARER_263 = "test-bearer-263";

/**
 * Generic fixture bearer — used by the proxy test for cookie-shaped
 * fixtures where the bearer's slice provenance does not matter.
 */
export const TEST_BEARER_FIXTURE = "test-bearer-fixture";

/**
 * Install / first-signin route tests use a "value"-suffixed shape
 * because the test name pattern asserts a literal value rather than a
 * slice-numbered one.
 */
export const TEST_BEARER_VALUE = "test-bearer-value";

/**
 * Token-state tests use two distinct bearers (A and B) to assert
 * isolation between sequential calls.
 */
export const TEST_BEARER_A = "test-bearer-a";
export const TEST_BEARER_B = "test-bearer-b";

/**
 * Generic BFF forwarder test bearer — used by `lib/api/bff.test.ts`
 * to assert the cookie-to-Bearer-header pass-through. The literal
 * value is asserted in the test body (the test reads back the
 * `Authorization: Bearer ...` header and confirms the bearer
 * propagated verbatim), so the constant exists as the SINGLE source
 * of truth for both the request setup and the assertion.
 */
export const TEST_BEARER_TOKEN = "test-bearer-token";

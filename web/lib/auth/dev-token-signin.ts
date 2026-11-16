// #2062 — when /login offers the developer (paste-a-bearer-token) sign-in.
//
// The token card used to render for everyone. It is now offered only when
//
//   (a) the web server runs with ATLAS_WEB_DEV_TOKEN_SIGNIN=true, or
//   (b) the platform reports first_install=true (first_signin_at IS NULL).
//
// (b) keeps the two flows that need a pasted token working with the flag
// off: first sign-in with the bootstrap token, and sign-in with the
// recovery key minted by `atlas-cli credentials issue --reset-bootstrap`
// (which clears first_signin_at, putting the install back into (b)).
//
// The same decision gates the signIn server action on every call, not only
// the page render, so a stale /login tab cannot post a token after the
// first sign-in. An unreadable install-state counts as "not first install"
// (fail closed): the card is hidden and the action refused unless the flag
// is on.

import type { InstallState } from "./login-tenant";

export const DEV_TOKEN_SIGNIN_ENV = "ATLAS_WEB_DEV_TOKEN_SIGNIN";

// devTokenFlagEnabled reads the deployment setting. Only the exact
// lowercase string "true" opts in, matching ATLAS_ENABLE_DEMO_SEED and
// ATLAS_ALLOW_MULTI_TENANT: "1", "yes" and "TRUE" are off.
export function devTokenFlagEnabled(
  env: Record<string, string | undefined>,
): boolean {
  return env[DEV_TOKEN_SIGNIN_ENV] === "true";
}

export function devTokenSignInAllowed(
  flagEnabled: boolean,
  state: InstallState | null,
): boolean {
  return flagEnabled || state?.first_install === true;
}

// signedInRedirectTarget is where a signed-in visitor to /login goes: the
// already-sanitized `from` target, unless it points back at /login (which
// would loop), in which case /dashboard.
export function signedInRedirectTarget(safeTarget: string): string {
  return safeTarget === "/login" ||
    safeTarget.startsWith("/login?") ||
    safeTarget.startsWith("/login/")
    ? "/dashboard"
    : safeTarget;
}

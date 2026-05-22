// security-atlas TypeScript SDK public surface.
//
// Slice 191 ships the OAuth client_credentials helper as the
// migration target for slice 003's api-key-based authentication.
// Future slices will graduate the high-level evidence push surface
// to this entry point.

export {
  OAuthClient,
  OAuthError,
  InvalidConfigError,
  DEFAULT_HTTP_TIMEOUT_MS,
  DEFAULT_REFRESH_LEEWAY_MS,
} from "./oauth.js";

export type { OAuthClientOptions } from "./oauth.js";

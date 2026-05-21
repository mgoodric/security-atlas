// security-atlas TypeScript SDK — OAuth 2.0 client_credentials helper.
//
// Slice 191 ships this as the migration target for slice 003's
// api-key-based authentication. SDK consumers move from
// constructing a bearer-string directly to constructing an
// OAuthClient that handles token acquisition, caching, and
// refresh.
//
// USAGE:
//
//   import { OAuthClient } from "@security-atlas/sdk";
//
//   const oc = new OAuthClient({
//     clientId: "...",
//     clientSecret: "...",
//     issuerUrl: "https://atlas.example.com",
//   });
//   const token = await oc.getToken();
//   // Use token as the bearer for any /v1/* call:
//   //   Authorization: Bearer <token>
//
// CONCURRENCY:
//
// Node is single-threaded but I/O-concurrent. The SDK's refresh
// path serializes concurrent getToken() callers through a single
// in-flight Promise — the first caller that misses the cache
// kicks off the refresh; all others await the same Promise. There
// is no thundering-herd to the issuer.
//
// REFRESH POLICY:
//
// getToken() returns the cached JWT until 60 seconds before
// expiry, then refreshes synchronously. Tokens have a 1-hour
// lifetime per slice 188; the 60-second early refresh handles
// clock skew + slow requests.
//
// SCOPE DISCIPLINE:
//
// This module does NOT implement:
//   - Refresh-token grant (v3 deferred per slice 188).
//   - DPoP (v3 deferred per slice 191 P0-191-7).
//   - Token introspection (the SDK is the resource client, not
//     the resource server).

/** DEFAULT_REFRESH_LEEWAY_MS is the window before expiry inside
 * which getToken() refreshes proactively. 60s in milliseconds. */
export const DEFAULT_REFRESH_LEEWAY_MS = 60_000;

/** DEFAULT_HTTP_TIMEOUT_MS bounds the issuer request. */
export const DEFAULT_HTTP_TIMEOUT_MS = 30_000;

/** OAuthClientOptions are the constructor parameters for OAuthClient. */
export interface OAuthClientOptions {
  /** The public OAuth client identifier registered with the atlas issuer. */
  clientId: string;
  /** The plaintext OAuth client secret presented to /oauth/token. */
  clientSecret: string;
  /** The atlas issuer URL (the iss claim of every minted JWT). */
  issuerUrl: string;
  /** Optional RFC 8693 audience form param. */
  audience?: string;
  /** Optional time-before-expiry window inside which getToken refreshes. */
  refreshLeewayMs?: number;
  /** Optional per-request issuer-call timeout (milliseconds). */
  httpTimeoutMs?: number;
  /** Optional clock injection point for tests. Returns Date.now() millis. */
  now?: () => number;
  /** Optional fetch implementation override for tests. */
  fetchImpl?: typeof fetch;
}

/** OAuthError is the base class for SDK errors. */
export class OAuthError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "OAuthError";
  }
}

/** InvalidConfigError is thrown by the constructor on missing fields. */
export class InvalidConfigError extends OAuthError {
  constructor(message: string) {
    super(message);
    this.name = "InvalidConfigError";
  }
}

interface TokenResponse {
  access_token?: string;
  token_type?: string;
  expires_in?: number;
}

/**
 * OAuthClient is a thread-safe (in the JS single-thread sense)
 * OAuth client_credentials bearer-token acquirer with synchronous
 * refresh-before-expiry.
 */
export class OAuthClient {
  private readonly clientId: string;
  private readonly clientSecret: string;
  private readonly issuerUrl: string;
  private readonly audience?: string;
  private readonly refreshLeewayMs: number;
  private readonly httpTimeoutMs: number;
  private readonly now: () => number;
  private readonly fetchImpl: typeof fetch;
  private readonly tokenUrl: string;

  private cached: string | null = null;
  private expiresAtMs = 0;
  private inflight: Promise<string> | null = null;

  constructor(opts: OAuthClientOptions) {
    if (!opts.clientId) {
      throw new InvalidConfigError("clientId is required");
    }
    if (!opts.clientSecret) {
      throw new InvalidConfigError("clientSecret is required");
    }
    if (!opts.issuerUrl) {
      throw new InvalidConfigError("issuerUrl is required");
    }
    this.clientId = opts.clientId;
    this.clientSecret = opts.clientSecret;
    this.issuerUrl = opts.issuerUrl;
    this.audience = opts.audience;
    this.refreshLeewayMs = opts.refreshLeewayMs ?? DEFAULT_REFRESH_LEEWAY_MS;
    this.httpTimeoutMs = opts.httpTimeoutMs ?? DEFAULT_HTTP_TIMEOUT_MS;
    this.now = opts.now ?? (() => Date.now());
    this.fetchImpl = opts.fetchImpl ?? fetch;
    this.tokenUrl = opts.issuerUrl.replace(/\/+$/, "") + "/oauth/token";
  }

  /**
   * Return a valid bearer token. The cached token is returned
   * when it's still at least refreshLeewayMs away from expiry;
   * otherwise getToken acquires a fresh one. Concurrent callers
   * await a single in-flight refresh Promise.
   */
  async getToken(): Promise<string> {
    const nowMs = this.now();
    if (this.cached && nowMs + this.refreshLeewayMs < this.expiresAtMs) {
      return this.cached;
    }
    if (this.inflight) {
      return this.inflight;
    }
    this.inflight = this.acquire(nowMs)
      .then((tok) => {
        this.cached = tok.accessToken;
        this.expiresAtMs = tok.expiresAtMs;
        return tok.accessToken;
      })
      .finally(() => {
        this.inflight = null;
      });
    return this.inflight;
  }

  private async acquire(
    nowMs: number,
  ): Promise<{ accessToken: string; expiresAtMs: number }> {
    const params = new URLSearchParams();
    params.set("grant_type", "client_credentials");
    params.set("client_id", this.clientId);
    params.set("client_secret", this.clientSecret);
    if (this.audience) {
      params.set("audience", this.audience);
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.httpTimeoutMs);
    let resp: Response;
    try {
      resp = await this.fetchImpl(this.tokenUrl, {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          Accept: "application/json",
        },
        body: params.toString(),
        signal: controller.signal,
      });
    } catch (err) {
      throw new OAuthError(
        `token request failed: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    } finally {
      clearTimeout(timer);
    }
    if (!resp.ok) {
      const detail = await resp.text().catch(() => "");
      throw new OAuthError(
        `token endpoint returned ${resp.status}: ${detail.trim()}`,
      );
    }
    let payload: TokenResponse;
    try {
      payload = (await resp.json()) as TokenResponse;
    } catch (err) {
      throw new OAuthError(
        `parse token response: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    }
    if (!payload.access_token) {
      throw new OAuthError("token response missing access_token");
    }
    const expiresIn =
      payload.expires_in && payload.expires_in > 0 ? payload.expires_in : 3600;
    return {
      accessToken: payload.access_token,
      expiresAtMs: nowMs + expiresIn * 1000,
    };
  }
}

/*
 * security-atlas Java SDK — OAuth 2.0 client_credentials helper.
 *
 * Slice 195 ships this as the Java port of slice 191's Go / Python /
 * TS OAuth clients. SDK consumers construct an OAuthClient that
 * handles token acquisition, caching, and refresh.
 *
 * USAGE:
 *
 *   OAuthClient oc = OAuthClient.builder()
 *       .clientId("...")
 *       .clientSecret("...")
 *       .issuerUrl("https://atlas.example.com")
 *       .build();
 *   String token = oc.getToken();
 *   // Use token as the bearer for any /v1/* call:
 *   //   Authorization: Bearer <token>
 *
 * THREAD SAFETY:
 *
 * getToken() is safe for concurrent callers. The internal cache +
 * refresh state is guarded by a ReentrantLock. The first caller in
 * a refresh window blocks all subsequent callers until the refresh
 * completes — there is no thundering-herd to the issuer.
 *
 * REFRESH POLICY:
 *
 * getToken() returns the cached JWT until 60 seconds before expiry,
 * then refreshes synchronously. Tokens have a 1-hour lifetime per
 * slice 188; the 60-second early refresh handles clock skew + slow
 * requests without ever returning an about-to-expire token.
 *
 * SCOPE DISCIPLINE (slice 195 P0-195-3):
 *
 * This class does NOT implement:
 *   - Refresh-token grant (v3 deferred per slice 188).
 *   - DPoP (v3 deferred per slice 191 P0-191-7).
 *   - Token introspection (the SDK is the resource client, not
 *     the resource server).
 */
package com.security_atlas.sdk;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.locks.ReentrantLock;
import java.util.function.LongSupplier;

/**
 * Thread-safe OAuth 2.0 client_credentials bearer-token acquirer
 * with synchronous refresh-before-expiry.
 *
 * <p>Construct via {@link #builder()}. Required:
 * {@link Builder#clientId(String)},
 * {@link Builder#clientSecret(String)},
 * {@link Builder#issuerUrl(String)}.
 */
public final class OAuthClient {

    /** Default refresh-before-expiry window (60 seconds). */
    public static final long DEFAULT_REFRESH_LEEWAY_MS = 60_000L;

    /** Default per-request HTTP timeout (30 seconds). */
    public static final long DEFAULT_HTTP_TIMEOUT_MS = 30_000L;

    private final String clientId;
    private final String clientSecret;
    private final String audience;
    private final long refreshLeewayMs;
    private final HttpClient httpClient;
    private final LongSupplier clock;
    private final String tokenUrl;
    private final Duration requestTimeout;

    private final ReentrantLock lock = new ReentrantLock();
    private String cachedToken;
    private long expiresAtMs;

    private OAuthClient(final Builder b) {
        this.clientId = b.clientId;
        this.clientSecret = b.clientSecret;
        this.audience = b.audience;
        this.refreshLeewayMs = b.refreshLeewayMs;
        this.clock = b.clock;
        // Build an HttpClient with our timeout policy. The per-request
        // timeout is applied below via HttpRequest.timeout(); the
        // connectTimeout here bounds the initial socket setup.
        this.httpClient = b.httpClient != null
            ? b.httpClient
            : HttpClient.newBuilder()
                .connectTimeout(Duration.ofMillis(b.httpTimeoutMs))
                .build();
        this.requestTimeout = Duration.ofMillis(b.httpTimeoutMs);
        this.tokenUrl = trimTrailingSlashes(b.issuerUrl) + "/oauth/token";
    }

    /**
     * Return a valid bearer token. The cached token is returned
     * when it's still at least refreshLeewayMs away from expiry;
     * otherwise getToken acquires a fresh one. Concurrent callers
     * serialize through a {@link ReentrantLock}.
     *
     * @return a non-empty access_token suitable for the {@code
     *         Authorization: Bearer ...} header
     * @throws OAuthException if the issuer call fails or returns a
     *         non-200 response
     */
    public String getToken() {
        lock.lock();
        try {
            final long now = clock.getAsLong();
            if (cachedToken != null && now + refreshLeewayMs < expiresAtMs) {
                return cachedToken;
            }
            final TokenAndExpiry fresh = acquire(now);
            cachedToken = fresh.token;
            expiresAtMs = fresh.expiresAtMs;
            return cachedToken;
        } finally {
            lock.unlock();
        }
    }

    /**
     * POST grant_type=client_credentials to /oauth/token. Caller
     * holds {@link #lock}; the HTTP call is synchronous so racing
     * callers wait on the lock rather than firing concurrent
     * requests at the issuer.
     */
    private TokenAndExpiry acquire(final long nowMs) {
        final String body = buildForm();
        final HttpRequest req = HttpRequest.newBuilder()
            .uri(URI.create(tokenUrl))
            .timeout(requestTimeout)
            .header("Content-Type", "application/x-www-form-urlencoded")
            .header("Accept", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8))
            .build();

        final HttpResponse<String> resp;
        try {
            resp = httpClient.send(req, HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
        } catch (final InterruptedException ie) {
            // Preserve interrupt status — Java idiom; surface as OAuth
            // failure so callers see a single error type.
            Thread.currentThread().interrupt();
            throw new OAuthException("token request interrupted", ie);
        } catch (final java.io.IOException ioe) {
            throw new OAuthException("token request failed: " + ioe.getMessage(), ioe);
        }

        final String payload = resp.body() == null ? "" : resp.body();
        if (resp.statusCode() != 200) {
            throw new OAuthException(
                "token endpoint returned " + resp.statusCode() + ": " + payload.trim());
        }

        final String accessToken = JsonReader.readStringField(payload, "access_token");
        if (accessToken == null || accessToken.isEmpty()) {
            throw new OAuthException("token response missing access_token");
        }
        long expiresInSeconds = JsonReader.readLongField(payload, "expires_in");
        if (expiresInSeconds <= 0) {
            // Issuer didn't specify; fall back to 1 hour (matches
            // Go / Python / TS sibling defaults).
            expiresInSeconds = 3600L;
        }
        return new TokenAndExpiry(accessToken, nowMs + expiresInSeconds * 1000L);
    }

    private String buildForm() {
        final StringBuilder sb = new StringBuilder(128);
        sb.append("grant_type=client_credentials");
        sb.append("&client_id=").append(urlEncode(clientId));
        sb.append("&client_secret=").append(urlEncode(clientSecret));
        if (audience != null && !audience.isEmpty()) {
            sb.append("&audience=").append(urlEncode(audience));
        }
        return sb.toString();
    }

    private static String urlEncode(final String s) {
        return java.net.URLEncoder.encode(s, StandardCharsets.UTF_8);
    }

    /**
     * Strip trailing "/" without using a regex — matches the TS
     * SDK's CodeQL-clean implementation. Done iteratively because
     * the issuerUrl is operator config, but defensive coding costs
     * nothing.
     */
    private static String trimTrailingSlashes(final String s) {
        int end = s.length();
        while (end > 0 && s.charAt(end - 1) == '/') {
            end--;
        }
        return end == s.length() ? s : s.substring(0, end);
    }

    /** Internal value type for {@link #acquire(long)}. */
    private static final class TokenAndExpiry {
        final String token;
        final long expiresAtMs;

        TokenAndExpiry(final String token, final long expiresAtMs) {
            this.token = token;
            this.expiresAtMs = expiresAtMs;
        }
    }

    /**
     * Create a new builder. All three required fields
     * ({@code clientId}, {@code clientSecret}, {@code issuerUrl})
     * must be set before {@link Builder#build()} is called.
     *
     * @return a fresh builder
     */
    public static Builder builder() {
        return new Builder();
    }

    /**
     * Builder for {@link OAuthClient}.
     *
     * <p>The Go SDK uses a struct, Python uses kwargs, TS uses an
     * options object. Java's natural equivalent is a builder —
     * keeps the constructor argument list bounded and lets us
     * default the optional knobs.
     */
    public static final class Builder {
        private String clientId;
        private String clientSecret;
        private String issuerUrl;
        private String audience;
        private long refreshLeewayMs = DEFAULT_REFRESH_LEEWAY_MS;
        private long httpTimeoutMs = DEFAULT_HTTP_TIMEOUT_MS;
        private LongSupplier clock = System::currentTimeMillis;
        private HttpClient httpClient;

        private Builder() { }

        /**
         * Set the public OAuth client identifier.
         *
         * @param v the client_id registered with the atlas issuer
         * @return this builder
         */
        public Builder clientId(final String v) {
            this.clientId = v;
            return this;
        }

        /**
         * Set the OAuth client secret.
         *
         * @param v the plaintext client_secret (never persist this
         *          in source — load from env or secret manager)
         * @return this builder
         */
        public Builder clientSecret(final String v) {
            this.clientSecret = v;
            return this;
        }

        /**
         * Set the atlas issuer URL.
         *
         * @param v the issuer URL (the iss claim of every minted
         *          JWT); the {@code /oauth/token} path is appended
         *          internally
         * @return this builder
         */
        public Builder issuerUrl(final String v) {
            this.issuerUrl = v;
            return this;
        }

        /**
         * Set the optional RFC 8693 audience form param.
         *
         * @param v the audience hint; may be null
         * @return this builder
         */
        public Builder audience(final String v) {
            this.audience = v;
            return this;
        }

        /**
         * Override the default 60-second refresh-before-expiry
         * window. Non-positive values fall back to the default.
         *
         * @param v leeway in milliseconds
         * @return this builder
         */
        public Builder refreshLeewayMs(final long v) {
            this.refreshLeewayMs = v > 0 ? v : DEFAULT_REFRESH_LEEWAY_MS;
            return this;
        }

        /**
         * Override the default 30-second per-request HTTP timeout.
         * Non-positive values fall back to the default.
         *
         * @param v timeout in milliseconds
         * @return this builder
         */
        public Builder httpTimeoutMs(final long v) {
            this.httpTimeoutMs = v > 0 ? v : DEFAULT_HTTP_TIMEOUT_MS;
            return this;
        }

        /**
         * Inject a clock for tests. Production callers should never
         * set this — the default is {@link System#currentTimeMillis()}.
         *
         * @param v a supplier returning Unix epoch millis
         * @return this builder
         */
        public Builder clock(final LongSupplier v) {
            if (v != null) {
                this.clock = v;
            }
            return this;
        }

        /**
         * Override the {@link HttpClient}. Production callers
         * should never set this; tests use it to point at an
         * in-process fake issuer.
         *
         * @param v an HttpClient instance
         * @return this builder
         */
        public Builder httpClient(final HttpClient v) {
            this.httpClient = v;
            return this;
        }

        /**
         * Build the {@link OAuthClient}.
         *
         * @return a configured OAuthClient
         * @throws InvalidConfigException if clientId, clientSecret,
         *         or issuerUrl is null or empty
         */
        public OAuthClient build() {
            if (clientId == null || clientId.isEmpty()) {
                throw new InvalidConfigException("clientId is required");
            }
            if (clientSecret == null || clientSecret.isEmpty()) {
                throw new InvalidConfigException("clientSecret is required");
            }
            if (issuerUrl == null || issuerUrl.isEmpty()) {
                throw new InvalidConfigException("issuerUrl is required");
            }
            return new OAuthClient(this);
        }
    }
}

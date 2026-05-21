/*
 * security-atlas Java SDK — OAuthClient tests.
 *
 * Mirror coverage of the Go (`pkg/sdk-go/oauth/oauth_test.go`),
 * Python (`sdk/python/tests/test_oauth.py`), and TypeScript
 * (`sdk/typescript/tests/oauth.test.ts`) sibling suites.
 *
 * Strategy: in-process JDK `HttpServer` plays the role of the atlas
 * issuer. No mocking framework — JUnit 5 only.
 */
package com.security_atlas.sdk;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;

/**
 * JUnit 5 unit tests for {@link OAuthClient}.
 *
 * <p>Each test stands up an in-process {@link HttpServer} bound to
 * port 0 (OS-assigned), so they can run in parallel without
 * collision.
 */
class OAuthClientTest {

    /**
     * Counter-bearing handler. Each call increments {@code calls};
     * the access_token returned cycles through {@code tokens}.
     */
    private static final class FakeIssuer implements HttpHandler {
        final AtomicInteger calls = new AtomicInteger();
        final String[] tokens;
        final int expiresIn;
        final int statusCode;
        final CountDownLatch firstCallLatch;

        FakeIssuer(final String[] tokens, final int expiresIn) {
            this(tokens, expiresIn, 200, null);
        }

        FakeIssuer(final String[] tokens, final int expiresIn,
                   final int statusCode, final CountDownLatch firstCallLatch) {
            this.tokens = tokens;
            this.expiresIn = expiresIn;
            this.statusCode = statusCode;
            this.firstCallLatch = firstCallLatch;
        }

        @Override
        public void handle(final HttpExchange ex) throws IOException {
            try (ex) {
                final int n = calls.getAndIncrement();
                if (firstCallLatch != null && n == 0) {
                    try {
                        firstCallLatch.await();
                    } catch (InterruptedException ie) {
                        Thread.currentThread().interrupt();
                    }
                }
                if (statusCode != 200) {
                    final byte[] body = "{\"error\":\"invalid_client\"}"
                        .getBytes(StandardCharsets.UTF_8);
                    ex.getResponseHeaders().set("Content-Type", "application/json");
                    ex.sendResponseHeaders(statusCode, body.length);
                    try (OutputStream os = ex.getResponseBody()) {
                        os.write(body);
                    }
                    return;
                }
                final String tok = tokens[n % tokens.length];
                final String json = "{\"access_token\":\"" + tok
                    + "\",\"token_type\":\"Bearer\",\"expires_in\":"
                    + expiresIn + "}";
                final byte[] body = json.getBytes(StandardCharsets.UTF_8);
                ex.getResponseHeaders().set("Content-Type", "application/json");
                ex.sendResponseHeaders(200, body.length);
                try (OutputStream os = ex.getResponseBody()) {
                    os.write(body);
                }
            }
        }
    }

    /** Start a fake issuer on a random port; caller stops it. */
    private static HttpServer startServer(final FakeIssuer issuer) throws IOException {
        final HttpServer srv = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        srv.createContext("/oauth/token", issuer);
        srv.start();
        return srv;
    }

    private static String urlOf(final HttpServer srv) {
        return "http://127.0.0.1:" + srv.getAddress().getPort();
    }

    @Test
    void builderRejectsMissingClientId() {
        assertThrows(InvalidConfigException.class, () -> OAuthClient.builder()
            .clientSecret("test-secret-fixture")
            .issuerUrl("https://atlas.example.com")
            .build());
    }

    @Test
    void builderRejectsMissingClientSecret() {
        assertThrows(InvalidConfigException.class, () -> OAuthClient.builder()
            .clientId("test-client")
            .issuerUrl("https://atlas.example.com")
            .build());
    }

    @Test
    void builderRejectsMissingIssuerUrl() {
        assertThrows(InvalidConfigException.class, () -> OAuthClient.builder()
            .clientId("test-client")
            .clientSecret("test-secret-fixture")
            .build());
    }

    @Test
    void tokenIsCachedUntilExpiry() throws IOException {
        final FakeIssuer issuer = new FakeIssuer(new String[]{"tok-1"}, 3600);
        final HttpServer srv = startServer(issuer);
        try {
            final OAuthClient oc = OAuthClient.builder()
                .clientId("test-client")
                .clientSecret("test-secret-fixture")
                .issuerUrl(urlOf(srv))
                .clock(() -> 1_700_000_000_000L)
                .build();
            final String t1 = oc.getToken();
            final String t2 = oc.getToken();
            assertEquals(t1, t2);
            assertEquals(1, issuer.calls.get(),
                "expected single issuer call; cache should serve the second");
        } finally {
            srv.stop(0);
        }
    }

    @Test
    void tokenRefreshesNearExpiry() throws IOException {
        // expires_in = 60s; refresh leeway default 60s; clock advanced
        // 30s into the leeway window forces refresh on second call.
        final FakeIssuer issuer = new FakeIssuer(new String[]{"tok-1", "tok-2"}, 60);
        final HttpServer srv = startServer(issuer);
        try {
            final AtomicLong clock = new AtomicLong(1_700_000_000_000L);
            final OAuthClient oc = OAuthClient.builder()
                .clientId("test-client")
                .clientSecret("test-secret-fixture")
                .issuerUrl(urlOf(srv))
                .clock(clock::get)
                .build();
            final String t1 = oc.getToken();
            assertEquals("tok-1", t1);
            // Advance 30s — still inside the 60s expiry but inside
            // the 60s refresh leeway: now + leeway >= expiresAt.
            clock.addAndGet(30_000L);
            final String t2 = oc.getToken();
            assertEquals("tok-2", t2);
            assertEquals(2, issuer.calls.get());
        } finally {
            srv.stop(0);
        }
    }

    @Test
    void concurrentCallersSerializeThroughLock() throws Exception {
        // First call blocks on the latch; while it's blocked, fire
        // 10 concurrent getToken() calls — they all wait on the
        // ReentrantLock. Release the latch and all 10 should see
        // the same cached token from exactly one issuer call.
        final CountDownLatch firstCallLatch = new CountDownLatch(1);
        final FakeIssuer issuer = new FakeIssuer(new String[]{"tok-1"}, 3600, 200,
            firstCallLatch);
        final HttpServer srv = startServer(issuer);
        try {
            final OAuthClient oc = OAuthClient.builder()
                .clientId("test-client")
                .clientSecret("test-secret-fixture")
                .issuerUrl(urlOf(srv))
                .clock(() -> 1_700_000_000_000L)
                .build();

            final int callerCount = 10;
            final CountDownLatch done = new CountDownLatch(callerCount);
            final String[] results = new String[callerCount];
            for (int i = 0; i < callerCount; i++) {
                final int idx = i;
                new Thread(() -> {
                    try {
                        results[idx] = oc.getToken();
                    } finally {
                        done.countDown();
                    }
                }).start();
            }
            // Give the threads a moment to all queue at the lock.
            Thread.sleep(100);
            firstCallLatch.countDown();
            assertTrue(done.await(5, java.util.concurrent.TimeUnit.SECONDS),
                "concurrent getToken() callers did not complete within 5s");
            assertEquals(1, issuer.calls.get(),
                "lock should serialize callers to a single issuer hit");
            for (final String r : results) {
                assertNotNull(r);
                assertEquals("tok-1", r);
            }
        } finally {
            srv.stop(0);
        }
    }

    @Test
    void issuerErrorSurfacesAsOAuthException() throws IOException {
        final FakeIssuer issuer = new FakeIssuer(new String[]{"unused"}, 3600, 401, null);
        final HttpServer srv = startServer(issuer);
        try {
            final OAuthClient oc = OAuthClient.builder()
                .clientId("test-client")
                .clientSecret("test-secret-fixture")
                .issuerUrl(urlOf(srv))
                .build();
            final OAuthException thrown = assertThrows(OAuthException.class, oc::getToken);
            assertTrue(thrown.getMessage().contains("401"),
                "expected status code in message, got: " + thrown.getMessage());
        } finally {
            srv.stop(0);
        }
    }
}

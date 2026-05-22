/*
 * security-atlas Java SDK — OAuthException.
 *
 * Slice 195 mirrors the Go SDK's `ErrInvalidConfig` + ad-hoc errors,
 * the Python SDK's `OAuthError`, and the TS SDK's `OAuthError` class.
 */
package com.security_atlas.sdk;

/**
 * Base class for {@link OAuthClient} errors.
 *
 * <p>Unchecked because callers typically want to propagate; the
 * stack trace + cause-chain carries diagnostic detail. The
 * Go / Python / TS surfaces all expose a single error type, and
 * Java's checked-exception ceremony would diverge from that
 * contract without buying anything.
 */
public class OAuthException extends RuntimeException {

    private static final long serialVersionUID = 1L;

    /**
     * Construct an OAuthException with a message.
     *
     * @param message human-readable diagnostic
     */
    public OAuthException(final String message) {
        super(message);
    }

    /**
     * Construct an OAuthException wrapping a cause.
     *
     * @param message human-readable diagnostic
     * @param cause   the underlying exception (e.g., {@link java.io.IOException})
     */
    public OAuthException(final String message, final Throwable cause) {
        super(message, cause);
    }
}

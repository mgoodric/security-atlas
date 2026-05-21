/*
 * security-atlas Java SDK — InvalidConfigException.
 *
 * Slice 195 mirrors the Go SDK's `ErrInvalidConfig`, the Python
 * SDK's `InvalidConfigError`, and the TS SDK's
 * `InvalidConfigError`.
 */
package com.security_atlas.sdk;

/**
 * Thrown by {@link OAuthClient}'s builder when a required field
 * (clientId, clientSecret, issuerUrl) is missing or empty.
 */
public class InvalidConfigException extends OAuthException {

    private static final long serialVersionUID = 1L;

    /**
     * Construct an InvalidConfigException with a message.
     *
     * @param message human-readable diagnostic
     */
    public InvalidConfigException(final String message) {
        super(message);
    }
}

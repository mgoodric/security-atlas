/*
 * security-atlas Java SDK — tiny JSON reader.
 *
 * Slice 195 D3: we deliberately avoid taking a Jackson / Gson /
 * org.json runtime dependency. The OAuth token response has exactly
 * three fields the SDK reads (access_token, token_type, expires_in)
 * and the issuer is atlas itself — a tightly-controlled producer. A
 * minimal handwritten reader is the right trade-off vs a 1-MB+
 * dependency.
 *
 * This reader supports flat JSON objects: top-level string and
 * integer values only. Nested objects, arrays, scientific notation,
 * and Unicode escapes are out of scope (the OAuth response uses
 * none of them). Anything malformed surfaces as -1 / null.
 *
 * NOT intended as a general-purpose JSON parser. If the SDK grows
 * to call endpoints with richer payloads (slice 196+), reconsider.
 */
package com.security_atlas.sdk;

final class JsonReader {

    private JsonReader() { }

    /**
     * Read a top-level string field. Returns null if missing or
     * the value is not a JSON string.
     */
    static String readStringField(final String json, final String key) {
        final int valueStart = findValueStart(json, key);
        if (valueStart < 0) {
            return null;
        }
        if (json.charAt(valueStart) != '"') {
            return null;
        }
        return parseString(json, valueStart + 1);
    }

    /**
     * Read a top-level integer field. Returns -1 if missing or the
     * value is not a JSON number; the caller treats -1 as "absent".
     */
    static long readLongField(final String json, final String key) {
        final int valueStart = findValueStart(json, key);
        if (valueStart < 0) {
            return -1L;
        }
        return parseLong(json, valueStart);
    }

    /**
     * Find the index of the first non-whitespace char of the value
     * for {@code key}, or -1 if the key is absent. Naive scan over
     * the input — fine for &lt;1 KB OAuth responses.
     */
    private static int findValueStart(final String json, final String key) {
        if (json == null || key == null) {
            return -1;
        }
        final String needle = "\"" + key + "\"";
        int idx = 0;
        while (true) {
            final int found = json.indexOf(needle, idx);
            if (found < 0) {
                return -1;
            }
            // Ensure this occurrence is a top-level key, not a
            // substring inside another string value. The previous
            // non-whitespace char must be '{' or ',' (object-start
            // or field-separator).
            int back = found - 1;
            while (back >= 0 && Character.isWhitespace(json.charAt(back))) {
                back--;
            }
            final boolean topLevelKey = back < 0
                || json.charAt(back) == '{'
                || json.charAt(back) == ',';
            if (!topLevelKey) {
                idx = found + needle.length();
                continue;
            }
            // Skip past needle, whitespace, ':', whitespace.
            int i = found + needle.length();
            while (i < json.length() && Character.isWhitespace(json.charAt(i))) {
                i++;
            }
            if (i >= json.length() || json.charAt(i) != ':') {
                return -1;
            }
            i++;
            while (i < json.length() && Character.isWhitespace(json.charAt(i))) {
                i++;
            }
            if (i >= json.length()) {
                return -1;
            }
            return i;
        }
    }

    /**
     * Parse a JSON string starting at index i (first char after
     * the opening quote). Supports \" and \\ escapes; other escapes
     * are passed through literally (the OAuth payload doesn't use
     * them).
     */
    private static String parseString(final String json, final int start) {
        final StringBuilder out = new StringBuilder();
        int i = start;
        while (i < json.length()) {
            final char c = json.charAt(i);
            if (c == '"') {
                return out.toString();
            }
            if (c == '\\' && i + 1 < json.length()) {
                final char next = json.charAt(i + 1);
                if (next == '"' || next == '\\' || next == '/') {
                    out.append(next);
                    i += 2;
                    continue;
                }
            }
            out.append(c);
            i++;
        }
        // Unterminated string — treat as malformed.
        return null;
    }

    /**
     * Parse a JSON integer starting at index i. Stops at the first
     * non-digit char. Returns -1 if no digits are present.
     */
    private static long parseLong(final String json, final int start) {
        int i = start;
        boolean negative = false;
        if (i < json.length() && json.charAt(i) == '-') {
            negative = true;
            i++;
        }
        long value = 0L;
        boolean anyDigit = false;
        while (i < json.length()) {
            final char c = json.charAt(i);
            if (c < '0' || c > '9') {
                break;
            }
            anyDigit = true;
            value = value * 10L + (long) (c - '0');
            i++;
        }
        if (!anyDigit) {
            return -1L;
        }
        return negative ? -value : value;
    }
}

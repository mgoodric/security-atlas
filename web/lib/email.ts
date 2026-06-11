// Slice 679 (ATLAS-032) — owner-email validation for the vendor form.
//
// The vendor "Owner (email)" field is contractually an email (label +
// placeholder `alice@example.com` + the slice-139 export's MaskEmail
// masking all treat it as one). Slice 679 adds the missing client-side
// guard so a role string ("Head of Security") can no longer be saved
// into a field every downstream surface reads as an email.
//
// Intentionally a small, total, pure predicate — NOT a full RFC 5322
// parser. The platform's job here is to reject the obvious non-email
// (no `@`, no domain dot, embedded whitespace) at submit time, not to
// litigate every quoted-local-part edge case. The authoritative store
// is the backend; this is input hygiene at the edge.

// EMAIL_RE matches `local@domain.tld` with:
//   - a non-empty local part free of whitespace and `@`,
//   - a single `@`,
//   - a domain with at least one dot and a 2+ char final label.
// Deliberately conservative: it rejects role strings, bare usernames,
// and trailing-`@` values while accepting the realistic operator inputs
// (`alice@demo.example`, `a.b+tag@sub.domain.co`).
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/;

// isEmail reports whether `value` is a plausibly-valid email address.
// Total (never throws) and pure. Leading/trailing whitespace is trimmed
// before the test so a copy-pasted address with a stray space still
// validates by content.
export function isEmail(value: string | null | undefined): boolean {
  if (!value) return false;
  return EMAIL_RE.test(value.trim());
}

// VENDOR_OWNER_EMAIL_ERROR is the single user-facing message the vendor
// form surfaces when the owner field fails validation. Centralized so
// the form and its tests reference one string.
export const VENDOR_OWNER_EMAIL_ERROR =
  "Owner must be a valid email address (e.g. alice@demo.example).";

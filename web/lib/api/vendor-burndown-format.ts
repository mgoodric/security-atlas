// Slice 664 — vendor review-burndown on-time-rate formatter.
//
// The Vendors "Review burndown" widget previously rendered "100%" for an
// empty vendor population (zero vendors). Root cause: the platform's
// on-time fraction is computed as on-time / total with total==0 short-
// circuiting to 1.0 (internal/vendor/store.go onTime()), so a 0/0 read
// surfaces as a misleading "100% ON-TIME / 0 vendors".
//
// An empty population is not 100% compliant — it has no compliance signal
// at all. This formatter guards on the POPULATION SIZE (`total`), not on
// the fraction value: guarding the fraction would wrongly blank a genuine
// 100%-on-time populated tenant. When `total <= 0` the rate renders the
// empty token; otherwise the fraction renders as a rounded integer percent
// exactly as before (no change for populated tenants — slice 664 anti-
// criterion). This mirrors the board-pack empty-vendor honesty established
// by slice 273 / 662 (an empty burndown reads as empty, never "100%").

// EMPTY_RATE is the display token for an unmeasurable (zero-population)
// on-time rate. An em-dash is the established empty-numeric glyph in the
// Stat surface; it satisfies AC-1's "—" / "N/A" requirement.
export const EMPTY_RATE = "—"; // em-dash "—"

/**
 * Format a vendor on-time rate for display.
 *
 * @param total    Population size (vendor count) the fraction was computed over.
 * @param fraction On-time fraction in [0, 1] (ignored when total is empty).
 * @returns "—" when the population is empty; otherwise a rounded percent string.
 */
export function formatOnTimeRate(total: number, fraction: number): string {
  if (!Number.isFinite(total) || total <= 0) {
    return EMPTY_RATE;
  }
  return `${Math.round(fraction * 100)}%`;
}

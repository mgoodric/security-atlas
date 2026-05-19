// Slice 162 — pure render-helper for the Active Sessions row's second line.
//
// Takes the augmented session fields (user_agent, ip_address, geo_country,
// geo_city) and produces the human-readable line that renders under the
// existing `Created {date} · last used {date}` line.
//
// Anti-criterion P0-162-1: do NOT fabricate placeholder text for missing
// fields. If a field is absent (pre-migration row, no http.Request at session
// create, geo not yet enriched), we OMIT that part of the line — never
// "(unknown)" / "—" / similar. The line is honest: it shows only what the
// platform actually knows.
//
// Output shapes:
//
//   - all four present:  "Mozilla Safari · 192.0.2.18 · San Francisco, US"
//   - only ua + ip:      "Mozilla Safari · 192.0.2.18"
//   - only ip:           "192.0.2.18"
//   - only geo:          "San Francisco, US"
//   - geo without city:  "US"
//   - city without country: "San Francisco"
//   - none present:      ""   (caller renders nothing)

import type { MeSession } from "@/lib/api";

/** Truncate a User-Agent string to a UI-safe length. Browsers send UAs that
 *  can be 200+ chars; the row UI has limited horizontal space. The full UA
 *  is still on the wire — this is render-only truncation, no fabrication.
 *  Truncating with a unicode ellipsis preserves an honest read-on-hover via
 *  the title attribute (caller sets the title to the unedited UA).
 */
export const UA_DISPLAY_MAX = 64;

export function truncateUA(ua: string): string {
  if (ua.length <= UA_DISPLAY_MAX) {
    return ua;
  }
  return ua.slice(0, UA_DISPLAY_MAX - 1) + "…";
}

/** Render the geo portion: "{city}, {country}" / "{city}" / "{country}" / "".
 *  When both are present, we emit "City, CC" — the standard human ordering.
 */
export function geoLine(
  country: string | undefined,
  city: string | undefined,
): string {
  const hasCountry = !!country && country.trim() !== "";
  const hasCity = !!city && city.trim() !== "";
  if (hasCountry && hasCity) {
    return `${city}, ${country}`;
  }
  if (hasCity) {
    return city as string;
  }
  if (hasCountry) {
    return country as string;
  }
  return "";
}

/** Build the full session line. Returns an empty string when no fields are
 *  present so the caller can short-circuit on `line === ""` and render nothing.
 *  Parts are joined by " · " (middot with spaces) matching the existing line
 *  style above the session metadata.
 */
export function sessionLine(
  s: Pick<MeSession, "user_agent" | "ip_address" | "geo_country" | "geo_city">,
): string {
  const parts: string[] = [];
  if (s.user_agent && s.user_agent.trim() !== "") {
    parts.push(truncateUA(s.user_agent));
  }
  if (s.ip_address && s.ip_address.trim() !== "") {
    parts.push(s.ip_address);
  }
  const geo = geoLine(s.geo_country, s.geo_city);
  if (geo !== "") {
    parts.push(geo);
  }
  return parts.join(" · ");
}

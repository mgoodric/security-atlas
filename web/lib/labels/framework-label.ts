// #2083 — one label for a framework version: name plus version.
//
// A tile that says "2022" is ambiguous, and one that says
// "ISO/IEC 27001:2022 2022" is wrong. frameworkLabel always returns the
// name with its version exactly once:
//
//   frameworkLabel("ISO/IEC 27001", "2022")       → "ISO/IEC 27001:2022"
//   frameworkLabel("ISO/IEC 27001:2022", "2022")  → "ISO/IEC 27001:2022"
//   frameworkLabel("NIST CSF", "2.0")             → "NIST CSF 2.0"
//   frameworkLabel("SOC 2", "")                   → "SOC 2"
//
// ISO standards join the year with a colon, the way ISO cites them; every
// other framework joins with a space.

export function frameworkLabel(
  name: string | null | undefined,
  version: string | null | undefined,
): string {
  const n = (name ?? "").trim();
  const v = (version ?? "").trim();
  if (n === "") return v === "" ? "—" : `Version ${v}`;
  if (v === "" || nameCarriesVersion(n, v)) return n;
  return /^ISO\b/.test(n) ? `${n}:${v}` : `${n} ${v}`;
}

// nameCarriesVersion is true when the name already ends in the version,
// after a space, colon, hyphen or "v" ("SCF 2025.2", "ISO/IEC 27001:2022",
// "NIST CSF v2.0").
function nameCarriesVersion(name: string, version: string): boolean {
  if (name === version) return true;
  const escaped = version.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`[\\s:\\-]v?${escaped}$`, "i").test(name);
}

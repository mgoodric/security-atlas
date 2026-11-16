// #1800 — formatting for the "org mapping" entry, kept out of the .tsx so
// the node-only vitest tier covers it.

// formatAddedOn renders an overlay edge's creation time as YYYY-MM-DD (UTC),
// the form the overlay export uses. An unparseable value renders as given.
export function formatAddedOn(createdAt: string): string {
  const d = new Date(createdAt);
  return Number.isNaN(d.getTime()) ? createdAt : d.toISOString().slice(0, 10);
}

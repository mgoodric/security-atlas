// #2082 — one-line "not available yet" state for a page or panel that has
// to exist before its feature does.
//
// Says what is missing and who can enable it, in the user's terms. It
// never names an endpoint, a slice or a canvas section. Use FeatureGate,
// not this, when a shipped module is turned off for the workspace.

export function ComingSoon({
  what,
  who = "your administrator",
}: {
  /** What the user cannot do yet, e.g. "Running saved queries". */
  what: string;
  /** Who can enable it. */
  who?: string;
}) {
  return (
    <p
      data-testid="coming-soon"
      role="status"
      className="rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground"
    >
      {what} is not available yet. Ask {who} about enabling it.
    </p>
  );
}

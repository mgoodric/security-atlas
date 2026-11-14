// #2057 — status chip tones for the board-pack surfaces, in theme tokens only.
//
// The accent tokens (--pass, --warning, ...) are tuned to clear WCAG AA
// (~4.7:1) as text on a plain surface. A same-hue tint behind the text pulls
// that under 4.5:1 (measured ~3.9:1 at 15%), so a board-pack chip carries its
// hue in the text and border and sits on the card surface instead of a tint.
// Pinned by e2e/board-pack-theme-contrast.spec.ts in both app themes.

export type Tone =
  | "pass"
  | "warning"
  | "critical"
  | "info"
  | "progress"
  | "neutral";

export const TONE_CHIP: Record<Tone, string> = {
  pass: "border-pass/40 bg-card text-pass",
  warning: "border-warning/40 bg-card text-warning",
  critical: "border-critical/40 bg-card text-critical",
  info: "border-info/40 bg-card text-info",
  progress: "border-progress/40 bg-card text-progress",
  neutral: "border-border bg-card text-foreground",
};

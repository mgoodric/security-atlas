// Slice 097 — color-coded threshold badge per AC-3.
//
// Renders the shadcn Badge with a variant chosen by the
// `thresholdBadgeColor` pure function in lib/api/metrics.ts. Color
// mapping:
//   green   -> "default"     (primary; treated as "ok")
//   yellow  -> "outline"     (warning band — no native shadcn yellow)
//   red     -> "destructive"
//   neutral -> "secondary"

import { Badge } from "@/components/ui/badge";
import {
  type MetricTarget,
  type ThresholdColor,
  thresholdBadgeColor,
} from "@/lib/api/metrics";

import { parseValue } from "./format";

const VARIANT: Record<
  ThresholdColor,
  "default" | "outline" | "destructive" | "secondary"
> = {
  green: "default",
  yellow: "outline",
  red: "destructive",
  neutral: "secondary",
};

const LABEL: Record<ThresholdColor, string> = {
  green: "on target",
  yellow: "warning",
  red: "critical",
  neutral: "no data",
};

export function ThresholdBadge({
  value,
  target,
  testid,
}: {
  value: string | undefined;
  target: MetricTarget | null;
  testid?: string;
}) {
  const color = thresholdBadgeColor(parseValue(value), target);
  return (
    <Badge
      variant={VARIANT[color]}
      data-testid={testid}
      data-threshold-color={color}
    >
      {LABEL[color]}
    </Badge>
  );
}

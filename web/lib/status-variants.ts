export type StatusBadgeVariant =
  | "pass"
  | "info"
  | "warning"
  | "critical"
  | "progress"
  | "secondary"
  | "outline";

export function auditStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "open":
    case "in_progress":
      return "progress";
    case "frozen":
      return "info";
    case "closed":
    case "planned":
      return "secondary";
    default:
      return "outline";
  }
}

export function auditStatusDotClass(status: string): string {
  switch (auditStatusVariant(status)) {
    case "progress":
      return "bg-progress animate-pulse";
    case "info":
      return "bg-info";
    default:
      return "bg-muted-foreground/60";
  }
}

export function exceptionStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "active":
      return "pass";
    case "requested":
    case "approved":
      return "progress";
    case "denied":
      return "critical";
    case "expired":
      return "warning";
    default:
      return "outline";
  }
}

export function policyStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "published":
      return "pass";
    case "under_review":
      return "progress";
    case "approved":
      return "info";
    case "retired":
    case "superseded":
      return "warning";
    case "draft":
      return "secondary";
    default:
      return "outline";
  }
}

export function actionPlanStatusVariant(status: string): StatusBadgeVariant {
  switch (status) {
    case "verified":
      return "pass";
    case "completed":
      return "info";
    case "in_progress":
      return "progress";
    case "blocked":
      return "critical";
    case "draft":
      return "secondary";
    default:
      return "outline";
  }
}

export type AckRateStatusVariant = "pass" | "warning" | "critical" | "none";

export function ackRateStatusVariant(
  band: "green" | "amber" | "red" | "none",
): AckRateStatusVariant {
  switch (band) {
    case "green":
      return "pass";
    case "amber":
      return "warning";
    case "red":
      return "critical";
    case "none":
    default:
      return "none";
  }
}

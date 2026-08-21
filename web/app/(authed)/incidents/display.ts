import type {
  Incident,
  IncidentAffectedSystem,
  IncidentSeverity,
  IncidentStatus,
  IncidentTimelineEntry,
} from "@/lib/api/incidents";

export const STATUS_LABELS: Record<IncidentStatus, string> = {
  detected: "Detected",
  triaged: "Triaged",
  contained: "Contained",
  resolved: "Resolved",
  closed: "Closed",
};

export const SEVERITY_LABELS: Record<IncidentSeverity, string> = {
  p3: "P3",
  p2: "P2",
  p1: "P1",
  p0: "P0",
};

export type NextIncidentAction =
  | { kind: "transition"; toState: Exclude<IncidentStatus, "detected"> }
  | { kind: "close"; toState: "closed" };

export function nextIncidentAction(
  status: IncidentStatus,
): NextIncidentAction | null {
  switch (status) {
    case "detected":
      return { kind: "transition", toState: "triaged" };
    case "triaged":
      return { kind: "transition", toState: "contained" };
    case "contained":
      return { kind: "transition", toState: "resolved" };
    case "resolved":
      return { kind: "close", toState: "closed" };
    case "closed":
      return null;
  }
}

export function dateTimeLabel(iso?: string | null): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toISOString().replace("T", " ").slice(0, 16) + "Z";
}

export function affectedSystemsList(value: unknown): IncidentAffectedSystem[] {
  return Array.isArray(value) ? (value as IncidentAffectedSystem[]) : [];
}

export function affectedSystemName(system: IncidentAffectedSystem): string {
  const raw = system.name ?? system.system ?? system.service;
  return typeof raw === "string" && raw.trim() ? raw.trim() : "unnamed system";
}

export function affectedSystemsSummary(value: unknown, max = 2): string {
  const systems = affectedSystemsList(value);
  if (systems.length === 0) return "No affected systems recorded";
  const names = systems.slice(0, max).map(affectedSystemName);
  const extra = systems.length - names.length;
  return extra > 0 ? `${names.join(", ")} +${extra}` : names.join(", ");
}

export function chronologicalTimeline(
  entries: IncidentTimelineEntry[],
): IncidentTimelineEntry[] {
  return [...entries].sort(
    (a, b) =>
      new Date(a.occurred_at).getTime() - new Date(b.occurred_at).getTime(),
  );
}

export function incidentCountsLabel(
  incident: Incident,
  counts: number,
): string {
  return `${SEVERITY_LABELS[incident.severity]} · ${
    STATUS_LABELS[incident.status]
  } · ${counts} linked`;
}

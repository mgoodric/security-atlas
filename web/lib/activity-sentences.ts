// #2093 — Activity rows as sentences.
//
// One unified-log row reads as "<Person> <phrase> <target>", built from
// an event-kind -> template map. A template sees only the row's envelope
// (kind, action, target_type, target_id); it never reads payload_json,
// which is null for rows whose source the caller cannot read (#2379) and
// carries only changed-field names for risk rows (#2367). The actor is
// rendered by <Person> on the page, not here.
//
// Every known kind is either given a template or mapped explicitly to
// GENERIC. A kind the platform adds later (the wire type is a loose
// string) falls back to the generic sentence at runtime, and
// activity-sentences.test.ts fails until it is mapped one way or the
// other.

import { AUDIT_LOG_KINDS, type AuditLogKind } from "@/lib/api/audit-log";

export type SentenceRow = {
  kind: string;
  action: string;
  target_type: string;
  target_id: string;
};

// A sentence, minus its subject. `noun` names the target's type and
// `href` is set only when the target route is one every role can open.
export type Sentence = {
  phrase: string;
  noun: string;
  targetId: string;
  href: string | null;
  // Trailing qualifier, e.g. the evidence intake outcome.
  suffix: string;
};

type Template = (row: SentenceRow) => Sentence;

export const GENERIC = "generic" as const;

// humanizeAction turns an action code into words: "feature_flag.enable"
// -> "enable", "placement_changed" -> "placement changed".
export function humanizeAction(action: string): string {
  const last = action.includes(".")
    ? action.slice(action.lastIndexOf(".") + 1)
    : action;
  return last.replace(/[_-]+/g, " ").trim().toLowerCase();
}

// Controls are the only record type every role (viewer, control_owner,
// auditor, grc_engineer, admin) is admitted to read
// (policies/authz/*.rego). Any other target renders as plain text.
export function targetHref(
  targetType: string,
  targetId: string,
): string | null {
  if (!targetId) return null;
  if (targetType === "control" || targetType === "controls") {
    return `/controls/${encodeURIComponent(targetId)}`;
  }
  return null;
}

function sentence(
  row: SentenceRow,
  phrase: string,
  noun: string,
  suffix = "",
): Sentence {
  return {
    phrase,
    noun,
    targetId: row.target_id,
    href: targetHref(row.target_type, row.target_id),
    suffix,
  };
}

function nounOf(targetType: string): string {
  return targetType.replace(/[_-]+/g, " ").trim();
}

// genericSentence is the sentence for a kind without its own template:
// "<Person> recorded <action> on <target type> <id>".
export function genericSentence(row: SentenceRow): Sentence {
  const action = humanizeAction(row.action);
  return sentence(
    row,
    action ? `recorded "${action}" on` : "recorded an event on",
    nounOf(row.target_type) || "record",
  );
}

// Past tenses the suffix rule below would get wrong.
const PAST: Record<string, string> = {
  submit: "submitted",
  set_default: "set as default",
  carry_over: "carried over",
  duplicate: "marked as duplicate",
  delete: "deleted",
};

// verbed turns an action into a past-tense phrase: "approve" ->
// "approved", "requested" stays. A dotted action keeps its prefix as a
// qualifier: "predicate.update" -> "updated" with "(predicate)".
export function verbed(action: string): { phrase: string; qualifier: string } {
  const dot = action.lastIndexOf(".");
  const verb = dot >= 0 ? action.slice(dot + 1) : action;
  const prefix = dot >= 0 ? action.slice(0, dot).replace(/[._-]+/g, " ") : "";
  let phrase = PAST[verb];
  if (!phrase) {
    const a = verb.replace(/[_-]+/g, " ").trim().toLowerCase();
    if (!a) phrase = "changed";
    else if (/ed$/.test(a)) phrase = a;
    else phrase = a.endsWith("e") ? `${a}d` : `${a}ed`;
  }
  return { phrase, qualifier: prefix ? `(${prefix})` : "" };
}

function lifecycle(noun: string | ((row: SentenceRow) => string)): Template {
  return (row) => {
    const v = verbed(row.action);
    return sentence(
      row,
      v.phrase,
      typeof noun === "string" ? noun : noun(row),
      v.qualifier,
    );
  };
}

const ME_PHRASES: Record<string, string> = {
  "profile.update": "updated their profile",
  "preferences.update": "updated their preferences",
  "session.revoke": "revoked one of their sessions",
};

// risk_audit_log.action_type (CHECK in migrations/sql). An action added
// there later reads "changed risk …" until it gets a phrase here.
const RISK_PHRASES: Record<string, string> = {
  placement_changed: "moved",
  category_changed: "recategorized",
  fields_changed: "edited",
  treatment_changed: "changed the treatment of",
  acceptance_proposed: "proposed acceptance of",
  acceptance_approved: "approved acceptance of",
  acceptance_rejected: "rejected acceptance of",
  acceptance_revoked: "revoked acceptance of",
  closed: "closed",
  reopened: "reopened",
  deleted: "deleted",
  control_linked: "linked a control to",
  control_unlinked: "unlinked a control from",
};

const FLAG_PHRASES: Record<string, string> = {
  "feature_flag.enable": "enabled",
  "feature_flag.disable": "disabled",
  "feature_flag.flip": "changed",
};

export const KIND_TEMPLATES: Record<AuditLogKind, Template | typeof GENERIC> = {
  // Authorization decisions are read telemetry; the generic sentence
  // names the action and the resource honestly.
  decision: GENERIC,
  evidence: (row) =>
    sentence(
      row,
      "submitted",
      "evidence record",
      row.action ? `(${humanizeAction(row.action)})` : "",
    ),
  exception: lifecycle("exception"),
  sample: lifecycle("audit sample"),
  audit_period: lifecycle("audit period"),
  aggregation_rule: lifecycle("aggregation rule"),
  feature_flag: (row) =>
    sentence(row, FLAG_PHRASES[row.action] ?? "changed", "feature flag"),
  // A self-service account event: the target is the actor, so the
  // sentence names no record.
  me: (row) =>
    sentence(
      { ...row, target_id: "" },
      ME_PHRASES[row.action] ??
        `recorded "${
          humanizeAction(row.action) || "an event"
        }" on their account`,
      "",
    ),
  walkthrough: lifecycle("walkthrough"),
  framework_scope: lifecycle("framework scope"),
  taxonomy: lifecycle((row) => nounOf(row.target_type) || "taxonomy entry"),
  mapping_overlay: lifecycle("mapping overlay edge"),
  control_import: lifecycle("imported control row"),
  risk: (row) => sentence(row, RISK_PHRASES[row.action] ?? "changed", "risk"),
  // #2136: the one action is `set`; the target is the framework version.
  posture_target: (row) =>
    sentence(row, "set the posture target for", "framework version"),
};

function isKnownKind(kind: string): kind is AuditLogKind {
  return (AUDIT_LOG_KINDS as readonly string[]).includes(kind);
}

export function describeEvent(row: SentenceRow): Sentence {
  const t = isKnownKind(row.kind) ? KIND_TEMPLATES[row.kind] : GENERIC;
  return t === GENERIC || t === undefined ? genericSentence(row) : t(row);
}

// ----- grouping by day ------------------------------------------------

export type DayGroup<T> = { key: string; label: string; rows: T[] };

function localDayKey(d: Date): string {
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

// groupByDay buckets rows by the viewer's local calendar day of
// occurred_at, preserving input order (the endpoint returns newest
// first). Labels read "Today", "Yesterday", then a long date. An
// unparsable timestamp lands in an "Unknown date" group.
export function groupByDay<T extends { occurred_at: string }>(
  rows: T[],
  now: Date = new Date(),
  locale?: string,
): DayGroup<T>[] {
  const today = localDayKey(now);
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  const yesterday = localDayKey(y);

  const groups: DayGroup<T>[] = [];
  const byKey = new Map<string, DayGroup<T>>();
  for (const row of rows) {
    const d = new Date(row.occurred_at);
    const valid = Number.isFinite(d.getTime());
    const key = valid ? localDayKey(d) : "unknown";
    let g = byKey.get(key);
    if (!g) {
      let label: string;
      if (!valid) label = "Unknown date";
      else if (key === today) label = "Today";
      else if (key === yesterday) label = "Yesterday";
      else
        label = d.toLocaleDateString(locale, {
          weekday: "long",
          year: "numeric",
          month: "long",
          day: "numeric",
        });
      g = { key, label, rows: [] };
      byKey.set(key, g);
      groups.push(g);
    }
    g.rows.push(row);
  }
  return groups;
}

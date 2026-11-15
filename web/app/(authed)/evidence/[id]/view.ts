// #2137 — pure helpers for the `/evidence/[id]` detail page. No React, so
// the node-only vitest tier covers them.

import type { EvidenceResultEnum } from "@/lib/api/evidence";

export type ResultBadgeVariant = "pass" | "critical" | "warning" | "secondary";

// Evidence results are pass | fail | na | inconclusive (DB CHECK); there is
// no `excepted` evidence result.
const RESULT_BADGE: Record<EvidenceResultEnum, ResultBadgeVariant> = {
  pass: "pass",
  fail: "critical",
  inconclusive: "warning",
  na: "secondary",
};

export function resultBadgeVariant(result: string): ResultBadgeVariant {
  return RESULT_BADGE[result as EvidenceResultEnum] ?? "secondary";
}

// safeHttpUrl returns the URL only when it parses as absolute http(s), so
// no other scheme (javascript:, data:) can reach an href.
export function safeHttpUrl(raw: unknown): string | null {
  if (typeof raw !== "string" || raw === "") return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

export type Attestation = {
  attestor: string | null;
  statement: string | null;
  supportingUri: string | null;
};

// isManualAttestation matches every manual.attestation kind
// (`manual.attestation.v1` today).
export function isManualAttestation(kind: string | null): boolean {
  return kind !== null && kind.startsWith("manual.attestation");
}

// attestationFields reads the manual.attestation payload fields. Missing or
// non-string fields come back null; supporting_uri keeps its raw text (the
// page decides whether it is safe to link).
export function attestationFields(payload: unknown): Attestation {
  const p =
    payload !== null && typeof payload === "object"
      ? (payload as Record<string, unknown>)
      : {};
  const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : null);
  return {
    attestor: str(p.attestor),
    statement: str(p.statement),
    supportingUri: str(p.supporting_uri),
  };
}

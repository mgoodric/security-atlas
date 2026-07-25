import { policyStatusVariant } from "@/lib/status-variants";

/** Semantic badge variant for policy lifecycle status. */
export function statusPillVariant(status: string) {
  return policyStatusVariant(status);
}

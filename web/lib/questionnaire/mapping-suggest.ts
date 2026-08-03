export interface MappingSuggestResponse {
  proposal_id?: string;
  question_id?: string;
  scf_anchor_id?: string;
  scf_anchor_uuid?: string;
  title?: string;
  rationale?: string;
  suppressed?: boolean;
  reason?: string;
  manual_only?: boolean;
  model_name?: string;
  model_version?: string;
  model_provider?: string;
  cloud_routed?: boolean;
  prompt_version?: string;
}

export type MappingSuggestStatus =
  | "proposed"
  | "manual"
  | "suppressed"
  | "error";

export interface MappingSuggestViewModel {
  status: MappingSuggestStatus;
  proposalId: string;
  scfAnchorId: string;
  title: string;
  rationale: string;
  canApprove: boolean;
  message: string;
  modelLabel: string;
  cloudRouted: boolean;
}

function modelLabel(r: MappingSuggestResponse): string {
  if (!r.model_name) return "";
  const version = r.model_version ? ` v${r.model_version}` : "";
  const provider = r.model_provider ? ` (${r.model_provider})` : "";
  return `${r.model_name}${version}${provider}`;
}

function messageFor(reason: string | undefined): string {
  switch (reason) {
    case "no_candidates":
      return "No mapping suggestion — map manually.";
    case "out_of_grounding":
    case "unknown_catalog_anchor":
      return "The suggested SCF anchor could not be verified and was withheld. Map manually.";
    case "invalid_model_response":
      return "The AI response was malformed and was withheld. Map manually.";
    case "generation_unavailable":
      return "AI mapping suggestion is temporarily unavailable. Map manually or try again.";
    default:
      return "The mapping suggestion was withheld. Map manually.";
  }
}

export function toMappingViewModel(
  r: MappingSuggestResponse,
): MappingSuggestViewModel {
  const cloudRouted = r.cloud_routed === true;
  const label = modelLabel(r);

  if (r.suppressed === true) {
    return {
      status: "suppressed",
      proposalId: "",
      scfAnchorId: "",
      title: "",
      rationale: "",
      canApprove: false,
      message: messageFor(r.reason),
      modelLabel: label,
      cloudRouted,
    };
  }
  if (r.manual_only === true) {
    return {
      status: "manual",
      proposalId: "",
      scfAnchorId: "",
      title: "",
      rationale: "",
      canApprove: false,
      message: messageFor(r.reason || "no_candidates"),
      modelLabel: label,
      cloudRouted,
    };
  }

  const proposalId = typeof r.proposal_id === "string" ? r.proposal_id : "";
  const scfAnchorId =
    typeof r.scf_anchor_id === "string" ? r.scf_anchor_id : "";
  const title = typeof r.title === "string" ? r.title : "";
  const rationale = typeof r.rationale === "string" ? r.rationale : "";
  const canApprove =
    proposalId.length > 0 && scfAnchorId.length > 0 && rationale.length > 0;

  return {
    status: canApprove ? "proposed" : "error",
    proposalId,
    scfAnchorId,
    title,
    rationale,
    canApprove,
    message: canApprove
      ? ""
      : "The AI mapping response was incomplete and cannot be approved.",
    modelLabel: label,
    cloudRouted,
  };
}

export function parseMappingSuggest(
  ok: boolean,
  status: number,
  raw: unknown,
): MappingSuggestViewModel {
  if (!ok) {
    let msg = `Mapping suggestion failed (${status}).`;
    if (
      raw &&
      typeof raw === "object" &&
      typeof (raw as { error?: string }).error === "string"
    ) {
      msg = (raw as { error: string }).error;
    }
    return {
      status: "error",
      proposalId: "",
      scfAnchorId: "",
      title: "",
      rationale: "",
      canApprove: false,
      message: msg,
      modelLabel: "",
      cloudRouted: false,
    };
  }
  if (!raw || typeof raw !== "object") {
    return {
      status: "error",
      proposalId: "",
      scfAnchorId: "",
      title: "",
      rationale: "",
      canApprove: false,
      message: "The AI mapping response was malformed and cannot be approved.",
      modelLabel: "",
      cloudRouted: false,
    };
  }
  return toMappingViewModel(raw as MappingSuggestResponse);
}

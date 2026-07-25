// Slice 499 — pure, node-testable view-model for the tenant's cloud-LLM
// routing config + the visible "routes to {provider}" banner.
//
// This is the CONFIG-DRIVEN banner source (the slice narrative's "driven by the
// tenant routing config, not hardcoded per surface" requirement). A surface
// renders <CloudRoutingBanner> (or consumes useRoutingBanner) and inherits the
// banner for free when the tenant is on a cloud provider — no per-surface
// banner logic.
//
// The platform's masked config shape (internal/api/llmrouting, JSON-tagged):
//   { provider: "local-ollama"|"anthropic"|"openai"|"bedrock",
//     is_cloud: boolean, has_api_key: boolean, api_key?: "<redacted>" }
// The key is NEVER present in plaintext — api_key, if set at all, is the
// "<redacted>" mask. The view-model deliberately drops it entirely.

export type RoutingProvider =
  | "local-ollama"
  | "anthropic"
  | "openai"
  | "bedrock";

export interface RoutingConfigResponse {
  provider?: string;
  is_cloud?: boolean;
  has_api_key?: boolean;
  // api_key is masked ("<redacted>") when present; never the plaintext. The
  // banner ignores it.
  api_key?: string;
}

export interface RoutingBannerViewModel {
  // True ONLY when the tenant is on a cloud provider — drives whether the
  // banner renders. local-ollama (the default) => false => no banner (AC-7).
  isCloud: boolean;
  // The active provider, for the banner text + the config UI. Defaults to
  // local-ollama when the config is absent/unparseable (the off-by-default
  // posture: an unknown config is treated as local, never as cloud).
  provider: RoutingProvider;
  // The human-readable banner message. Empty when not cloud.
  message: string;
}

const CLOUD_PROVIDERS: ReadonlySet<string> = new Set([
  "anthropic",
  "openai",
  "bedrock",
]);

// providerLabel renders a provider id as the human-facing name in the banner.
export function providerLabel(provider: RoutingProvider): string {
  switch (provider) {
    case "anthropic":
      return "Anthropic";
    case "openai":
      return "OpenAI";
    case "bedrock":
      return "AWS Bedrock";
    case "local-ollama":
    default:
      return "the local model";
  }
}

// normalizeProvider maps an arbitrary string to a known provider, defaulting to
// local-ollama. An unknown provider is NEVER treated as cloud (fail-safe).
export function normalizeProvider(raw: string | undefined): RoutingProvider {
  switch ((raw ?? "").toLowerCase()) {
    case "anthropic":
      return "anthropic";
    case "openai":
      return "openai";
    case "bedrock":
      return "bedrock";
    case "local-ollama":
    default:
      return "local-ollama";
  }
}

// bannerMessage is the canonical "routes to {provider}" honesty string (canvas
// §4.6.5). One definition so every surface shows identical wording.
export function bannerMessage(provider: RoutingProvider): string {
  return `AI assist routes to ${providerLabel(
    provider,
  )} — your data leaves this deployment.`;
}

// parseRoutingConfig interprets the BFF GET /api/admin/llm-routing response (ok
// + status + raw body) into the banner view-model. A failed/non-ok fetch, or an
// unparseable body, yields the local-ollama default (no banner) — the
// off-by-default posture: the banner is shown only when the config AFFIRMATIVELY
// names a cloud provider.
export function parseRoutingConfig(
  ok: boolean,
  raw: unknown,
): RoutingBannerViewModel {
  if (!ok || raw === null || typeof raw !== "object") {
    return { isCloud: false, provider: "local-ollama", message: "" };
  }
  const r = raw as RoutingConfigResponse;
  const provider = normalizeProvider(r.provider);
  // is_cloud is the server's authoritative flag; cross-check against the
  // provider so a malformed flag cannot force a local provider into "cloud" or
  // hide a cloud provider.
  const isCloud =
    provider !== "local-ollama" &&
    (r.is_cloud === true || CLOUD_PROVIDERS.has(provider));
  return {
    isCloud,
    provider,
    message: isCloud ? bannerMessage(provider) : "",
  };
}

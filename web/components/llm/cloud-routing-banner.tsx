// Slice 499 — the reusable, config-driven cloud-routing banner.
//
// The visible "AI assist routes to {provider} — your data leaves this
// deployment" affordance (canvas §4.6.5). It is the CONSTITUTIONAL CONTROL, not
// a UI nicety: it is the honesty affordance that an operator can never be
// unaware their confidential evidence is leaving the deployment for a third
// party.
//
// Every AI-assist surface renders this once (driven by the tenant routing
// config via useRoutingBanner) so a new surface inherits it for free — the
// banner is NOT hardcoded per surface. It renders NOTHING when the tenant is on
// the local-ollama default (AC-7: no banner for local).

"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { type RoutingBannerViewModel } from "@/lib/llm-routing/routing";
import { useRoutingBanner } from "@/lib/llm-routing/use-routing-banner";

interface CloudRoutingBannerProps {
  // Optional pre-resolved view-model. When omitted, the component resolves it
  // itself via the hook — so a surface can drop <CloudRoutingBanner /> in with
  // zero wiring. A surface that already holds the view-model (e.g. it shows a
  // per-draft cloud flag too) can pass it to avoid a second fetch.
  vm?: RoutingBannerViewModel;
}

// CloudRoutingBanner renders the routing banner when the tenant is on a cloud
// provider, and nothing otherwise. Self-resolving by default.
export function CloudRoutingBanner({ vm }: CloudRoutingBannerProps) {
  const resolved = useResolvedBanner(vm);
  if (!resolved.isCloud) return null;
  return (
    <Alert data-testid="cloud-routing-banner" variant="destructive">
      <AlertTitle>Cloud AI routing enabled</AlertTitle>
      <AlertDescription>{resolved.message}</AlertDescription>
    </Alert>
  );
}

// useResolvedBanner returns the passed view-model, or resolves one via the hook
// when none is given. The hook is always called (rules-of-hooks) but its result
// is ignored when an explicit vm is supplied.
function useResolvedBanner(
  vm: RoutingBannerViewModel | undefined,
): RoutingBannerViewModel {
  const fromHook = useRoutingBanner();
  return vm ?? fromHook;
}

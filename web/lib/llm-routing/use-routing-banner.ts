// Slice 499 — the reusable routing-banner hook.
//
// THIS is the deliverable that makes the visible "routes to {provider}" banner
// config-driven and inheritable: any AI-assist surface calls useRoutingBanner()
// and renders <CloudRoutingBanner vm={vm} /> — it does NOT re-implement the
// banner logic. When a future surface ships, it gets the banner for free by
// adopting the hook.
//
// The hook reads the tenant's routing config from the BFF (GET
// /api/admin/llm-routing) and returns the banner view-model. It fails quiet to
// the local-ollama default (no banner) — the off-by-default posture means an
// unreachable config never falsely claims local routing is cloud, and never
// suppresses a real cloud banner once the config loads.

"use client";

import { useEffect, useState } from "react";

import {
  parseRoutingConfig,
  type RoutingBannerViewModel,
} from "@/lib/llm-routing/routing";

const LOCAL_DEFAULT: RoutingBannerViewModel = {
  isCloud: false,
  provider: "local-ollama",
  message: "",
};

export function useRoutingBanner(): RoutingBannerViewModel {
  const [vm, setVm] = useState<RoutingBannerViewModel>(LOCAL_DEFAULT);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const res = await fetch("/api/admin/llm-routing", {
          cache: "no-store",
          credentials: "include",
        });
        let raw: unknown = null;
        try {
          raw = await res.json();
        } catch {
          raw = null;
        }
        if (cancelled) return;
        setVm(parseRoutingConfig(res.ok, raw));
      } catch {
        // Fail quiet: keep the local default (no banner). The config endpoint
        // being unreachable must never assert cloud routing.
        if (!cancelled) setVm(LOCAL_DEFAULT);
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  return vm;
}

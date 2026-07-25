// Slice 660 — clean "module not enabled" state.
//
// Rendered by a gated page (OSCAL vendor claims, board packs) when its
// backing API returns the featureflag.Gate 404 ("feature disabled"). The
// nav entry is already hidden server-side for flag-off tenants; this panel
// covers the direct-navigation case so the user sees a calm, explanatory
// state instead of a raw error toast. It does NOT leak any internal detail
// (slice 367) — it states only that the module is off and who can enable
// it.

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function FeatureDisabledState({ moduleName }: { moduleName: string }) {
  return (
    <Card data-testid="feature-disabled-state" className="mx-auto max-w-xl">
      <CardHeader>
        <CardTitle>{moduleName} is not enabled</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-sm text-muted-foreground">
        <p>
          This module is turned off for your workspace. An administrator can
          enable it under <span className="font-medium">Admin → Features</span>.
        </p>
        <p>
          Any existing data is preserved and reappears when it is re-enabled.
        </p>
      </CardContent>
    </Card>
  );
}

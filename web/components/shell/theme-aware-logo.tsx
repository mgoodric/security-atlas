"use client";

// Slice 176 -- Theme-aware logo component.
//
// Replaces the slice 075 inline `<picture media="(prefers-color-scheme:
// ...)">` blocks at three mount sites. The OS preference is the WRONG
// signal -- slice 170 made app theme an operator-controlled localStorage
// value persisted via the /settings AppearanceSelector, and that value
// is written to <html data-theme="...">. Operators with OS=dark + app
// theme="light" were being served logo-dark.svg (near-white ink) onto
// a light app background -> invisible.
//
// This component reads the app theme from <html data-theme> and falls
// back to `prefers-color-scheme` only when the app theme is "system"
// (or absent / unrecognized -- parseTheme normalizes both to "system").
// All four cases are exercised in web/lib/theme-aware-logo.test.ts.
//
// Hydration discipline (P0-A8): initial render uses the SSR-safe state
// (appTheme = DEFAULT_THEME, prefersDark = false), which resolves to
// /logo-light.svg -- byte-identical to the slice 075 `<picture>`
// element's fallback `<img src>` attribute. The useEffect post-mount
// sync mirrors slice 170's AppearanceSelector pattern: seed state with
// DEFAULT_THEME, read the real value on mount, no SSR throw. The brief
// one-frame flicker for users whose persisted theme differs from
// "system" matches the slice 170 precedent (acceptable below the fold;
// the logo is above the fold but the variant difference is the ink
// color of an 8-point compass star -- the silhouette is the same).
//
// Reactivity: listens for two signals so the component re-renders
// without a page reload when either changes:
//   1. <html data-theme> attribute changes -- the /settings page writes
//      this on selection. MutationObserver(attributes, attributeFilter).
//   2. OS-level prefers-color-scheme changes -- matchMedia listener.
// Both are torn down on unmount.
//
// P0-A4: this component does NOT modify Tailwind dark-mode config. It
// reads `data-theme` directly because that is what slice 170 writes;
// no class-mode opt-in is touched.

import { useEffect, useState } from "react";

import {
  DEFAULT_THEME,
  parseTheme,
  type Theme,
} from "@/app/(authed)/settings/theme";
import { resolveLogoSrc } from "@/lib/theme-aware-logo";

interface ThemeAwareLogoProps {
  // Pixel dimensions for the <img> intrinsic size. Required so the
  // browser reserves layout space before the SVG paints (CLS guard --
  // mirrors slice 075's <img width height> pattern).
  width: number;
  height: number;
  // Tailwind classes to apply -- usually a sizing pair like "h-7 w-7"
  // or "h-16 w-16" matching the topbar / login site contracts.
  className: string;
  // Alt text. Topbar passes "" (decorative; wordmark is the brand),
  // login passes "security-atlas" (image carries the brand name).
  alt: string;
}

export function ThemeAwareLogo({
  width,
  height,
  className,
  alt,
}: ThemeAwareLogoProps) {
  // SSR + first-paint state: defer to "system" + OS-light. This
  // resolves to /logo-light.svg -- byte-identical to the slice 075
  // `<picture>` element's fallback <img src> so hydration is silent.
  const [appTheme, setAppTheme] = useState<Theme>(DEFAULT_THEME);
  const [prefersDark, setPrefersDark] = useState<boolean>(false);

  useEffect(() => {
    // Read the actual app theme from <html data-theme>. Slice 170's
    // AppearanceSelector writes this attribute on selection; on first
    // visit (no prior selection) the attribute is absent and
    // parseTheme normalizes to DEFAULT_THEME = "system".
    const readAppTheme = (): Theme => {
      const attr = document.documentElement.getAttribute("data-theme");
      return parseTheme(attr);
    };

    // Post-mount: sync to the real values. The setState here is the
    // canonical "synchronizing with external systems" pattern (same
    // discipline slice 170 used in AppearanceSelector). Without this,
    // the picker would stay frozen at DEFAULT_THEME forever.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAppTheme(readAppTheme());

    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    setPrefersDark(mq.matches);

    // Re-sync when the operator selects a new theme on /settings (the
    // attribute changes; MutationObserver fires).
    const observer = new MutationObserver(() => {
      setAppTheme(readAppTheme());
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });

    // Re-sync when the OS-level preference changes (relevant only for
    // app theme === "system"; the picker re-runs anyway).
    const onPrefChange = (event: MediaQueryListEvent) => {
      setPrefersDark(event.matches);
    };
    mq.addEventListener("change", onPrefChange);

    return () => {
      observer.disconnect();
      mq.removeEventListener("change", onPrefChange);
    };
  }, []);

  const src = resolveLogoSrc(appTheme, prefersDark);

  return (
    // The Next.js <Image> component is intentionally NOT used here.
    // Slice 075's mount sites used a raw <img> (with the
    // @next/next/no-img-element rule suppressed) so the layout
    // semantics carry forward byte-identically -- swapping to <Image>
    // would require width/height/quality knob review per slice 075
    // AC-5 (out of scope for slice 176 per P0-A1).
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={src}
      alt={alt}
      width={width}
      height={height}
      className={className}
      // Stable hook for the slice 176 Playwright e2e contrast-delta
      // assertion under web/e2e/logo-render.spec.ts.
      data-testid="theme-aware-logo"
    />
  );
}

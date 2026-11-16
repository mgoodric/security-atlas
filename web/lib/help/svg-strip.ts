// #2148 — strip everything active from a rendered SVG, with the browser's
// own XML parser.
//
// `scripts/render-help-diagrams.mjs` runs this inside the Chromium page that
// rendered the diagram, so it must stay self-contained: Playwright sends the
// function's source to the page, and it can use nothing but DOM globals.
// It removes `<script>` elements, `on*` event-handler attributes (Mermaid's
// click directive can emit them), and links that are not same-document
// fragments. Node then refuses to write anything `unsafeSvgParts`
// (diagram-build.ts) still finds. `e2e/help-svg-strip.spec.ts` tests it in
// a real browser.

export function stripActiveSvg(svg: string): string {
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  if (doc.getElementsByTagName("parsererror").length > 0) {
    throw new Error("rendered SVG does not parse as XML");
  }
  for (const el of Array.from(doc.getElementsByTagName("script"))) {
    el.remove();
  }
  for (const el of Array.from(doc.getElementsByTagName("*"))) {
    for (const attr of Array.from(el.attributes)) {
      const name = attr.name.toLowerCase();
      const link = name === "href" || name === "xlink:href";
      if (name.startsWith("on") || (link && !attr.value.startsWith("#"))) {
        el.removeAttribute(attr.name);
      }
    }
  }
  return new XMLSerializer().serializeToString(doc.documentElement);
}

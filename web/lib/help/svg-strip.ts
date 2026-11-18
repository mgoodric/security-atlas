// #2148, #2429 — strip everything active from a rendered SVG, with the
// browser's own XML parser.
//
// `scripts/render-help-diagrams.mjs` runs this inside the Chromium page that
// rendered the diagram, so it must stay self-contained: Playwright sends the
// function's source to the page, and it can use nothing but DOM globals.
// That is why the element allow-list is repeated here rather than imported
// from `diagram-build.ts` (`SVG_ELEMENTS`).
//
// It keeps only allow-listed SVG elements, and drops processing
// instructions, attributes outside the SVG/XLink namespaces, `on*`
// handlers, any attribute whose local name is `href` unless it is a
// same-document fragment, `javascript:` values and non-fragment `url()`,
// and style sheets with `@import`, escapes or external `url()`. Node then
// refuses to write anything `unsafeSvgParts` (diagram-build.ts) still
// finds. `e2e/help-svg-strip.spec.ts` tests it in a real browser.

export function stripActiveSvg(svg: string): string {
  const SVG_NS = "http://www.w3.org/2000/svg";
  const XLINK_NS = "http://www.w3.org/1999/xlink";
  const XML_NS = "http://www.w3.org/XML/1998/namespace";
  const XMLNS_NS = "http://www.w3.org/2000/xmlns/";
  const ALLOWED = new Set([
    "a",
    "circle",
    "clipPath",
    "defs",
    "desc",
    "ellipse",
    "feBlend",
    "feColorMatrix",
    "feComposite",
    "feDropShadow",
    "feFlood",
    "feGaussianBlur",
    "feMerge",
    "feMergeNode",
    "feOffset",
    "filter",
    "g",
    "line",
    "linearGradient",
    "marker",
    "mask",
    "path",
    "pattern",
    "polygon",
    "polyline",
    "radialGradient",
    "rect",
    "stop",
    "style",
    "svg",
    "symbol",
    "text",
    "title",
    "tspan",
    "use",
  ]);
  // The same reading as `squash` and `activeValue` in diagram-build.ts.
  const squash = (v: string) =>
    v.replace(/[\s\u0000-\u001f\u007f]/g, "").toLowerCase();
  const active = (value: string, css: boolean) => {
    const v = squash(value);
    return (
      /(?:java|vb)script:/.test(v) ||
      /url\((?!["']?#)/.test(v) ||
      (css &&
        (v.includes("@import") ||
          v.includes("\\") ||
          /expression\(|-moz-binding|behavior:/.test(v)))
    );
  };

  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  if (doc.getElementsByTagName("parsererror").length > 0) {
    throw new Error("rendered SVG does not parse as XML");
  }
  if (doc.doctype !== null) {
    throw new Error("rendered SVG carries a DOCTYPE");
  }
  const root = doc.documentElement;
  if (root.namespaceURI !== SVG_NS || root.localName !== "svg") {
    throw new Error("rendered SVG has no <svg> root");
  }

  const walker = doc.createTreeWalker(
    root,
    NodeFilter.SHOW_PROCESSING_INSTRUCTION,
  );
  const instructions: Node[] = [];
  while (walker.nextNode()) instructions.push(walker.currentNode);
  for (const pi of instructions) pi.parentNode?.removeChild(pi);

  for (const el of Array.from(root.getElementsByTagName("*"))) {
    if (!root.contains(el)) continue;
    if (el.namespaceURI !== SVG_NS || !ALLOWED.has(el.localName)) {
      el.remove();
    } else if (el.localName === "style" && active(el.textContent ?? "", true)) {
      el.remove();
    }
  }
  for (const el of [root, ...Array.from(root.getElementsByTagName("*"))]) {
    for (const attr of Array.from(el.attributes)) {
      const local = attr.localName.toLowerCase();
      const ns = attr.namespaceURI;
      let drop: boolean;
      if (ns === XMLNS_NS) {
        drop = attr.value !== SVG_NS && attr.value !== XLINK_NS;
      } else {
        drop =
          (ns !== null && ns !== XLINK_NS && ns !== XML_NS) ||
          local.startsWith("on") ||
          (local === "href" && !squash(attr.value).startsWith("#")) ||
          active(attr.value, local === "style");
      }
      if (drop) el.removeAttributeNode(attr);
    }
  }
  return new XMLSerializer().serializeToString(root);
}

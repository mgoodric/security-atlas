// #2429 — hostile SVGs, one per way an active part can hide.
//
// `diagrams.test.ts` checks the Node gate (`unsafeSvgParts`) refuses each
// one with `why`; `e2e/help-svg-strip.spec.ts` checks the browser strip
// (`stripActiveSvg`) leaves nothing of it that the gate refuses, and no
// trace of its payload. Every payload contains "evil", so the browser test
// can look for it; `browserThrows` marks input the strip refuses outright.

const NS = 'xmlns="http://www.w3.org/2000/svg"';
const XLINK = "http://www.w3.org/1999/xlink";

export interface UnsafeSvgFixture {
  name: string;
  svg: string;
  /** A part of `unsafeSvgParts`' answer. */
  why: string;
  browserThrows?: RegExp;
}

export const UNSAFE_SVG_FIXTURES: readonly UnsafeSvgFixture[] = [
  {
    name: "an href under an unusual XLink prefix",
    svg: `<svg ${NS} xmlns:x="${XLINK}"><a x:href="https://evil.example"><text>a</text></a></svg>`,
    why: "external link",
  },
  {
    name: "an href under a prefix bound to another namespace",
    svg: `<svg ${NS} xmlns:y="urn:evil"><a y:href="https://evil.example"><text>a</text></a></svg>`,
    why: "external link",
  },
  {
    name: "an <animate> that rewrites href",
    svg: `<svg ${NS}><a href="#a"><animate attributeName="href" to="https://evil.example"/><text>a</text></a></svg>`,
    why: "<animate>",
  },
  {
    name: "a <set> that rewrites xlink:href",
    svg: `<svg ${NS} xmlns:xlink="${XLINK}"><a xlink:href="#a"><set attributeName="xlink:href" to="https://evil.example"/><text>a</text></a></svg>`,
    why: "<set>",
  },
  {
    name: "an entity-encoded javascript: link",
    svg: `<svg ${NS}><a href="&#106;ava&#x09;script:evil()"><text>a</text></a></svg>`,
    why: "javascript: URL",
  },
  {
    name: "an entity-encoded javascript: value in another attribute",
    svg: `<svg ${NS}><g data-x="&#x6A;avascript:evil()"/></svg>`,
    why: "javascript: URL",
  },
  {
    name: "@import in a style sheet",
    svg: `<svg ${NS}><style>@import "https://evil.example/x.css";</style></svg>`,
    why: "@import",
  },
  {
    name: "@import split by a comment",
    svg: `<svg ${NS}><style>@imp<!-- -->ort "evil.css";</style></svg>`,
    why: "@import",
  },
  {
    name: "a CSS-escaped @import",
    svg: `<svg ${NS}><style>@\\69mport "evil.css";</style></svg>`,
    why: "CSS escape",
  },
  {
    name: "an external url() in a style sheet",
    svg: `<svg ${NS}><style>rect{fill:url(https://evil.example/p.svg#g)}</style><rect/></svg>`,
    why: "non-fragment url()",
  },
  {
    name: "@import in a style attribute",
    svg: `<svg ${NS}><rect style='@import "evil.css"'/></svg>`,
    why: "@import",
  },
  {
    name: "an external url() in a style attribute",
    svg: `<svg ${NS}><rect style="fill: url( 'https://evil.example/p.svg#g' )"/></svg>`,
    why: "non-fragment url()",
  },
  {
    name: "an external url() in a presentation attribute",
    svg: `<svg ${NS}><rect fill="url(https://evil.example/p.svg#g)"/></svg>`,
    why: "non-fragment url()",
  },
  {
    name: "a CSS-escaped url() in a presentation attribute",
    svg: `<svg ${NS}><rect fill="u\\72l(https://evil.example/p.svg#g)" mask="u\\72l(https://evil.example/m.svg#m)"/></svg>`,
    why: "CSS escape",
  },
  {
    name: "image-set() in a style sheet",
    svg: `<svg ${NS}><style>rect{fill:image-set("evil.png" 1x)}</style><rect/></svg>`,
    why: "image-set()",
  },
  {
    name: "image-set() in a style attribute",
    svg: `<svg ${NS}><rect style='fill: image-set("evil.png" 1x)'/></svg>`,
    why: "image-set()",
  },
  {
    name: "an xml:base that re-roots fragment links",
    svg: `<svg ${NS}><a xml:base="https://evil.example/" href="#a"><text>a</text></a></svg>`,
    why: "xml:base",
  },
  {
    name: "a <foreignObject>",
    svg: `<svg ${NS}><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">evil</div></foreignObject></svg>`,
    why: "<foreignObject>",
  },
  {
    name: "an element in the XHTML namespace",
    svg: `<svg ${NS}><h:iframe xmlns:h="http://www.w3.org/1999/xhtml" src="https://evil.example"/></svg>`,
    why: "<h:iframe>",
  },
  {
    name: "an event handler",
    svg: `<svg ${NS}><g onclick="evil()"/></svg>`,
    why: "event-handler attribute",
  },
  {
    name: "a namespaced event handler",
    svg: `<svg ${NS} xmlns:x="${XLINK}"><g x:onclick="evil()"/></svg>`,
    why: "event-handler attribute",
  },
  {
    name: "a style-sheet processing instruction",
    svg: `<svg ${NS}><?xml-stylesheet href="https://evil.example/x.css"?><rect/></svg>`,
    why: "instruction",
  },
  {
    name: "an entity declared in a DOCTYPE",
    svg: `<!DOCTYPE svg [<!ENTITY e "javascript:evil()">]><svg ${NS}><a href="&e;"/></svg>`,
    why: "not well-formed XML",
    browserThrows: /DOCTYPE/,
  },
];

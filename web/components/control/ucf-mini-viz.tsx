"use client";

// Slice 041 — UCF graph mini-visualization (AC-3).
//
// A hand-rolled SVG graph: `control -> SCF anchor -> framework requirements`.
// Hand-rolled deliberately — adding a graph library would touch
// web/package.json (a spine file). The shape is small and bounded (an
// anchor maps to 1-8 requirements per `UCF_GRAPH_MODEL.md` §7), so a
// dynamic-layout SVG is the right tool.
//
// Constitutional invariant 1 (one control, N satisfactions) is what this
// view renders. Anti-criterion P0-1: edges ALWAYS route
// control -> anchor -> requirement; this component has no code path that
// draws a requirement-to-requirement edge. Anti-criterion P0-2:
// out-of-scope requirements are rendered dashed/greyed, never hidden.
//
// #2096 — labels are never truncated. Every node label wraps onto as many
// lines as it needs (lib/ucf-graph-text.ts) and each node is sized to its
// lines. Anchor-to-requirement edges are elbows whose last leg runs
// horizontally into the requirement node; the edge label sits above that
// leg, in a gutter wide enough for the longest label, so no edge label
// crosses a node. Labels are org- and catalog-supplied text, so they render
// only as React text children of SVG <text>/<tspan>, never as markup.

import type {
  ControlCoverage,
  CoverageRequirement,
} from "@/lib/api/control-detail";
import { strmStyle } from "@/components/control/strm";
import {
  CHAR_EM,
  type CharEm,
  estimateTextWidth,
  wrapText,
} from "@/lib/ucf-graph-text";

const MARGIN_X = 20;
const CONTROL_W = 200;
const ANCHOR_W = 170;
const REQ_W = 250;
const PAD_X = 12;
const PAD_Y = 10;
const ROW_GAP = 12;
const LINE_HEIGHT = 1.4;
// Horizontal run out of the anchor before an edge turns toward its row.
const ELBOW_RUN = 16;
// Clearance between an edge label and the node or elbow beside it.
const LABEL_CLEARANCE = 10;
const EDGE_LABEL_SIZE = 9;
// Vertical space reserved beneath the node area for the legend row.
const LEGEND_BAND = 48;

type Line = {
  text: string;
  size: number;
  weight?: number;
  mono?: boolean;
  fill: string;
};

type Block = {
  text: string;
  size: number;
  em: CharEm;
  weight?: number;
  mono?: boolean;
  fill: string;
};

// layoutLines wraps each block to the node's inner width.
function layoutLines(blocks: Block[], nodeW: number): Line[] {
  const inner = nodeW - 2 * PAD_X;
  return blocks.flatMap((b) =>
    wrapText(b.text, inner, b.size, b.em).map((text) => ({
      text,
      size: b.size,
      weight: b.weight,
      mono: b.mono,
      fill: b.fill,
    })),
  );
}

function linesHeight(lines: Line[]): number {
  return lines.reduce((h, l) => h + l.size * LINE_HEIGHT, 0) + 2 * PAD_Y;
}

// NodeText renders wrapped lines inside a node whose top-left is (x, y).
function NodeText({
  lines,
  x,
  y,
  w,
  align,
}: {
  lines: Line[];
  x: number;
  y: number;
  w: number;
  align: "start" | "middle";
}) {
  const tx = align === "middle" ? x + w / 2 : x + PAD_X;
  return (
    <>
      {lines.map((l, i) => {
        const lineTop = lines
          .slice(0, i)
          .reduce((t, prev) => t + prev.size * LINE_HEIGHT, y + PAD_Y);
        return (
          <text
            key={i}
            x={tx}
            y={lineTop + l.size}
            textAnchor={align}
            fontSize={l.size}
            fontWeight={l.weight}
            fontFamily={l.mono ? "monospace" : undefined}
            fill={l.fill}
            data-testid="ucf-viz-node-text"
          >
            {l.text}
          </text>
        );
      })}
    </>
  );
}

function edgeLabel(req: CoverageRequirement, outOfScope: boolean): string {
  const style = strmStyle(req.relationship_type);
  return `${style.label} · ${req.strength.toFixed(2)}${
    outOfScope ? " · out-of-scope" : ""
  }`;
}

export function UcfMiniViz({
  coverage,
  outOfScopeFvIds,
}: {
  coverage: ControlCoverage;
  // framework_version_ids whose effective scope is empty for this control.
  outOfScopeFvIds: ReadonlySet<string>;
}) {
  const { control, anchor, requirements } = coverage;

  if (!anchor) {
    return (
      <p
        data-testid="ucf-viz-unanchored"
        className="text-sm text-muted-foreground"
      >
        This control is not yet anchored to an SCF concept, so it has no UCF
        graph neighborhood. Anchor it to a SCF control to see the framework
        requirements it satisfies.
      </p>
    );
  }

  // ---- node contents ----
  const controlLines = layoutLines(
    [
      {
        text: control.title,
        size: 11,
        em: CHAR_EM.sansBold,
        weight: 600,
        fill: "rgb(49 46 129)",
      },
      {
        text: control.bundle_id,
        size: 10,
        em: CHAR_EM.mono,
        mono: true,
        fill: "rgb(79 70 229)",
      },
    ],
    CONTROL_W,
  );
  const anchorLines = layoutLines(
    [
      {
        text: anchor.scf_id,
        size: 11,
        em: CHAR_EM.mono,
        weight: 700,
        mono: true,
        fill: "rgb(146 64 14)",
      },
      { text: anchor.name, size: 10, em: CHAR_EM.sans, fill: "rgb(146 64 14)" },
      {
        text: "semantic anchor",
        size: 9,
        em: CHAR_EM.sans,
        fill: "rgb(120 53 15)",
      },
    ],
    ANCHOR_W,
  );
  const rows = requirements.map((req) => {
    const outOfScope = outOfScopeFvIds.has(req.framework_version_id);
    const lines = layoutLines(
      [
        {
          text: `${req.framework_name} · ${req.code}`,
          size: 10,
          em: CHAR_EM.sansBold,
          weight: 600,
          fill: outOfScope ? "rgb(100 116 139)" : "rgb(15 23 42)",
        },
        {
          text: req.title,
          size: 9,
          em: CHAR_EM.sans,
          fill: "rgb(100 116 139)",
        },
      ],
      REQ_W,
    );
    return {
      req,
      outOfScope,
      lines,
      h: linesHeight(lines),
      label: edgeLabel(req, outOfScope),
    };
  });

  // ---- horizontal layout: gutters sized to the labels they hold ----
  const anchoredAtW = estimateTextWidth("anchored_at", 9, CHAR_EM.mono);
  const controlX = MARGIN_X;
  const anchorX = controlX + CONTROL_W + anchoredAtW + 2 * LABEL_CLEARANCE;
  const elbowX = anchorX + ANCHOR_W + ELBOW_RUN;
  const widestEdgeLabel = Math.max(
    0,
    ...rows.map((r) =>
      estimateTextWidth(r.label, EDGE_LABEL_SIZE, CHAR_EM.mono),
    ),
  );
  const reqX = elbowX + widestEdgeLabel + 2 * LABEL_CLEARANCE;
  const viewW = reqX + REQ_W + MARGIN_X;

  // ---- vertical layout ----
  const controlH = linesHeight(controlLines);
  const anchorH = linesHeight(anchorLines);
  const stackH =
    rows.length === 0
      ? 40
      : rows.reduce((h, r) => h + r.h, 0) + (rows.length - 1) * ROW_GAP;
  const nodeArea = Math.max(stackH, anchorH + 40, controlH + 40);
  const height = nodeArea + LEGEND_BAND;
  const centerY = nodeArea / 2;
  const anchorY = centerY - anchorH / 2;
  const controlY = centerY - controlH / 2;

  const stackTop = (nodeArea - stackH) / 2;
  const placed = rows.map((r, i) => ({
    ...r,
    y: rows.slice(0, i).reduce((y, prev) => y + prev.h + ROW_GAP, stackTop),
  }));

  return (
    <svg
      data-testid="ucf-mini-viz"
      viewBox={`0 0 ${viewW} ${height}`}
      className="h-auto w-full"
      role="img"
      aria-label={`UCF graph neighborhood for ${control.title}`}
    >
      {/* control node */}
      <g data-testid="ucf-viz-control">
        <rect
          x={controlX}
          y={controlY}
          width={CONTROL_W}
          height={controlH}
          rx={8}
          fill="rgb(238 242 255)"
          stroke="rgb(99 102 241)"
          strokeWidth={2}
          data-testid="ucf-viz-node"
        />
        <NodeText
          lines={controlLines}
          x={controlX}
          y={controlY}
          w={CONTROL_W}
          align="middle"
        />
      </g>

      {/* control -> anchor edge */}
      <line
        x1={controlX + CONTROL_W}
        y1={centerY}
        x2={anchorX}
        y2={centerY}
        stroke="rgb(99 102 241)"
        strokeWidth={2}
      />
      <text
        x={(controlX + CONTROL_W + anchorX) / 2}
        y={centerY - 6}
        textAnchor="middle"
        fontSize={9}
        fontFamily="monospace"
        fill="rgb(100 116 139)"
        data-testid="ucf-viz-edge-label"
      >
        anchored_at
      </text>

      {/* SCF anchor node */}
      <g data-testid="ucf-viz-anchor">
        <rect
          x={anchorX}
          y={anchorY}
          width={ANCHOR_W}
          height={anchorH}
          rx={10}
          fill="rgb(255 247 237)"
          stroke="rgb(245 158 11)"
          strokeWidth={2}
          data-testid="ucf-viz-node"
        />
        <NodeText
          lines={anchorLines}
          x={anchorX}
          y={anchorY}
          w={ANCHOR_W}
          align="middle"
        />
      </g>

      {/* framework requirement nodes + edges */}
      {placed.length === 0 ? (
        <text
          x={reqX + REQ_W / 2}
          y={centerY}
          textAnchor="middle"
          fontSize={10}
          fill="rgb(100 116 139)"
        >
          no mapped framework requirements
        </text>
      ) : (
        placed.map(({ req, outOfScope, lines, h, y, label }) => {
          const style = strmStyle(req.relationship_type);
          const edgeStroke = outOfScope ? "rgb(148 163 184)" : style.stroke;
          const rowCenter = y + h / 2;
          return (
            <g key={req.edge_id} data-testid="ucf-viz-requirement">
              <path
                d={`M ${
                  anchorX + ANCHOR_W
                } ${centerY} H ${elbowX} V ${rowCenter} H ${reqX}`}
                fill="none"
                stroke={edgeStroke}
                strokeWidth={outOfScope ? 1.5 : 2}
                strokeDasharray={outOfScope ? "3 3" : undefined}
                data-strm={req.relationship_type}
                data-out-of-scope={outOfScope ? "true" : "false"}
              />
              <text
                x={reqX - LABEL_CLEARANCE}
                y={rowCenter - 5}
                textAnchor="end"
                fontSize={EDGE_LABEL_SIZE}
                fontFamily="monospace"
                fill={edgeStroke}
                data-testid="ucf-viz-edge-label"
              >
                {label}
              </text>
              <rect
                x={reqX}
                y={y}
                width={REQ_W}
                height={h}
                rx={6}
                fill={outOfScope ? "rgb(248 250 252)" : "white"}
                stroke="rgb(226 232 240)"
                strokeDasharray={outOfScope ? "3 3" : undefined}
                data-testid="ucf-viz-node"
              />
              <NodeText lines={lines} x={reqX} y={y} w={REQ_W} align="start" />
            </g>
          );
        })
      )}

      {/* legend */}
      <g transform={`translate(20, ${height - 18})`}>
        <text
          fontSize={10}
          fontWeight={600}
          fill="rgb(100 116 139)"
          x={0}
          y={0}
        >
          Legend
        </text>
        <line
          x1={56}
          y1={-4}
          x2={76}
          y2={-4}
          stroke={strmStyle("equal").stroke}
          strokeWidth={2}
        />
        <text x={82} y={0} fontSize={9} fill="rgb(100 116 139)">
          equal
        </text>
        <line
          x1={120}
          y1={-4}
          x2={140}
          y2={-4}
          stroke={strmStyle("subset_of").stroke}
          strokeWidth={2}
        />
        <text x={146} y={0} fontSize={9} fill="rgb(100 116 139)">
          subset
        </text>
        <line
          x1={188}
          y1={-4}
          x2={208}
          y2={-4}
          stroke={strmStyle("intersects_with").stroke}
          strokeWidth={2}
        />
        <text x={214} y={0} fontSize={9} fill="rgb(100 116 139)">
          intersects
        </text>
        <line
          x1={270}
          y1={-4}
          x2={290}
          y2={-4}
          stroke="rgb(148 163 184)"
          strokeWidth={1.5}
          strokeDasharray="3 3"
        />
        <text x={296} y={0} fontSize={9} fill="rgb(100 116 139)">
          out-of-scope
        </text>
      </g>
    </svg>
  );
}

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

import type { ControlCoverage, CoverageRequirement } from "@/lib/api";
import { strmStyle } from "@/components/control/strm";

const VIEW_W = 800;
const NODE_W = 190;
const NODE_H = 40;
const ROW_GAP = 12;
const CONTROL_X = 20;
const ANCHOR_X = 305;
const ANCHOR_W = 140;
const ANCHOR_H = 88;
const REQ_X = 590;
// Vertical space reserved beneath the node area for the legend row.
const LEGEND_BAND = 48;

// requirementStackHeight is the total height of the right-hand column of
// requirement node rows.
function requirementStackHeight(reqCount: number): number {
  return Math.max(reqCount, 1) * (NODE_H + ROW_GAP) - ROW_GAP;
}

function vizHeight(reqCount: number): number {
  const nodeArea = Math.max(requirementStackHeight(reqCount), ANCHOR_H + 40);
  return nodeArea + LEGEND_BAND;
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

  const height = vizHeight(requirements.length);
  const stackH = requirementStackHeight(requirements.length);
  const stackTop = (height - LEGEND_BAND - stackH) / 2;
  const centerY = (height - LEGEND_BAND) / 2;
  const anchorY = centerY - ANCHOR_H / 2;
  const controlY = centerY - NODE_H / 2;

  const reqRow = (req: CoverageRequirement, i: number) => {
    const y = stackTop + i * (NODE_H + ROW_GAP);
    const style = strmStyle(req.relationship_type);
    const outOfScope = outOfScopeFvIds.has(req.framework_version_id);
    const edgeStroke = outOfScope ? "rgb(148 163 184)" : style.stroke;
    return (
      <g key={req.edge_id} data-testid="ucf-viz-requirement">
        <line
          x1={ANCHOR_X + ANCHOR_W}
          y1={centerY}
          x2={REQ_X}
          y2={y + NODE_H / 2}
          stroke={edgeStroke}
          strokeWidth={outOfScope ? 1.5 : 2}
          strokeDasharray={outOfScope ? "3 3" : undefined}
          data-strm={req.relationship_type}
          data-out-of-scope={outOfScope ? "true" : "false"}
        />
        <text
          x={(ANCHOR_X + ANCHOR_W + REQ_X) / 2}
          y={(centerY + y + NODE_H / 2) / 2 - 4}
          textAnchor="middle"
          fontSize={9}
          fontFamily="monospace"
          fill={edgeStroke}
        >
          {req.relationship_type} · {req.strength.toFixed(2)}
          {outOfScope ? " · out-of-scope" : ""}
        </text>
        <rect
          x={REQ_X}
          y={y}
          width={NODE_W}
          height={NODE_H}
          rx={6}
          fill={outOfScope ? "rgb(248 250 252)" : "white"}
          stroke="rgb(226 232 240)"
          strokeDasharray={outOfScope ? "3 3" : undefined}
        />
        <text
          x={REQ_X + 10}
          y={y + 16}
          fontSize={10}
          fontWeight={600}
          fill={outOfScope ? "rgb(100 116 139)" : "rgb(15 23 42)"}
        >
          {`${req.framework_name} · ${req.code}`.slice(0, 30)}
        </text>
        <text x={REQ_X + 10} y={y + 30} fontSize={9} fill="rgb(100 116 139)">
          {req.title.slice(0, 34)}
        </text>
      </g>
    );
  };

  return (
    <svg
      data-testid="ucf-mini-viz"
      viewBox={`0 0 ${VIEW_W} ${height}`}
      className="h-auto w-full"
      role="img"
      aria-label={`UCF graph neighborhood for ${control.title}`}
    >
      {/* control node */}
      <g data-testid="ucf-viz-control">
        <rect
          x={CONTROL_X}
          y={controlY}
          width={NODE_W}
          height={NODE_H}
          rx={8}
          fill="rgb(238 242 255)"
          stroke="rgb(99 102 241)"
          strokeWidth={2}
        />
        <text
          x={CONTROL_X + NODE_W / 2}
          y={controlY + 17}
          textAnchor="middle"
          fontSize={11}
          fontWeight={600}
          fill="rgb(49 46 129)"
        >
          {control.title.slice(0, 26)}
        </text>
        <text
          x={CONTROL_X + NODE_W / 2}
          y={controlY + 31}
          textAnchor="middle"
          fontSize={10}
          fontFamily="monospace"
          fill="rgb(79 70 229)"
        >
          {control.bundle_id}
        </text>
      </g>

      {/* control -> anchor edge */}
      <line
        x1={CONTROL_X + NODE_W}
        y1={centerY}
        x2={ANCHOR_X}
        y2={centerY}
        stroke="rgb(99 102 241)"
        strokeWidth={2}
      />
      <text
        x={(CONTROL_X + NODE_W + ANCHOR_X) / 2}
        y={centerY - 6}
        textAnchor="middle"
        fontSize={9}
        fontFamily="monospace"
        fill="rgb(100 116 139)"
      >
        anchored_at
      </text>

      {/* SCF anchor node */}
      <g data-testid="ucf-viz-anchor">
        <rect
          x={ANCHOR_X}
          y={anchorY}
          width={ANCHOR_W}
          height={ANCHOR_H}
          rx={10}
          fill="rgb(255 247 237)"
          stroke="rgb(245 158 11)"
          strokeWidth={2}
        />
        <text
          x={ANCHOR_X + ANCHOR_W / 2}
          y={anchorY + 24}
          textAnchor="middle"
          fontSize={11}
          fontWeight={700}
          fontFamily="monospace"
          fill="rgb(146 64 14)"
        >
          {anchor.scf_id}
        </text>
        <text
          x={ANCHOR_X + ANCHOR_W / 2}
          y={anchorY + 42}
          textAnchor="middle"
          fontSize={10}
          fill="rgb(146 64 14)"
        >
          {anchor.name.slice(0, 18)}
        </text>
        <text
          x={ANCHOR_X + ANCHOR_W / 2}
          y={anchorY + 64}
          textAnchor="middle"
          fontSize={9}
          fill="rgb(120 53 15)"
        >
          semantic anchor
        </text>
      </g>

      {/* framework requirement nodes + edges */}
      {requirements.length === 0 ? (
        <text
          x={REQ_X + NODE_W / 2}
          y={centerY}
          textAnchor="middle"
          fontSize={10}
          fill="rgb(100 116 139)"
        >
          no mapped framework requirements
        </text>
      ) : (
        requirements.map(reqRow)
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

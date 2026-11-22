// #2836 — BFF for POST /v1/controls/imports/preview: the mapping step's
// view of an upload (header row, the field each header maps to, the fields
// a control row reads, the first rows). Nothing is staged.

import type { NextRequest } from "next/server";

import { forwardControlUpload } from "../upload-form";

export function POST(req: NextRequest) {
  return forwardControlUpload(req, "/v1/controls/imports/preview");
}

# security-atlas#1969 — XLSX bounds: overflow-safe pre-check, column cap

Follow-up to #1957 / #1960 (review findings). Moves the questionnaire
parser's xlsx bounds into a shared `internal/xlsxbound` package, makes the
decompressed-size sum overflow-safe, and adds a column cap.

detection_tier_actual: manual_review
detection_tier_target: unit

## D1 — Compare before adding

`checkDecompressedSize` added each entry's `UncompressedSize64` to the
running total before comparing, so a zip64 entry declaring close to 2^64
wrapped the sum back under the cap. excelize's own `UnzipSizeLimit` still
refused the workbook, but as an open error, which the handler maps to 400
rather than 413. The check is now `size > max-total` before the add;
`total` never exceeds `max`, so the subtraction cannot underflow.

## D2 — Column cap of 256, counted to the last non-blank cell

excelize's `Rows().Columns()` pads a row out to its highest cell index, so
one cell at `XFD` yields 16,384 strings and 5,000 such rows about 1.3 GB.
`MaxColumns = 256` (the pre-2007 Excel `IV` limit) is an order of magnitude
above any canonical questionnaire (CAIQ, HECVAT and SIG use under 20).

Width is measured after trailing blank cells are trimmed, and the trimmed
row is what the parser keeps. A workbook whose formatting leaves empty
styled cells far to the right is therefore still accepted; only a row with
real content past column 256 is rejected (`ErrTooManyColumns`, 400, the
same mapping as the row cap). Each padded row is still allocated once by
excelize before it is trimmed, which bounds the transient cost to one row.

## D3 — Shared helper, questionnaire adopts it now

`internal/xlsxbound` exposes `Limits`, `Open` (pre-check, then excelize
with the matching unzip limits), `ReadRows` (row and column caps) and
`CheckDecompressedSize`. The questionnaire package keeps its exported cap
constants and sentinel names; the sentinels are now aliases of the
`xlsxbound` ones, so `errors.Is` at the handler is unchanged. The sentinel
messages lose the `questionnaire:` prefix.

`internal/controlimport` is not changed here: #1929 is adding its own
bounds in flight, and moving it onto `xlsxbound` is that work's follow-up.

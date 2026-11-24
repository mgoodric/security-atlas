// Package docxmd converts a Word document (.docx, Office Open XML) to the
// Markdown a synced policy body holds (#2837). It is the SharePoint
// connector's counterpart of the Confluence connector's storagemd.
//
// What converts: paragraphs, headings (the built-in "heading 1".."heading
// 6" and "Title" styles), bold, italic and strikethrough, hard line
// breaks, bulleted and numbered lists (from numbering, directly or
// through a list style), external hyperlinks, and tables (as GFM pipe
// tables, the first row as the header). Tracked changes read as
// accepted: insertions are kept, deletions dropped.
//
// What does not: images and other drawings, embedded objects, equations,
// footnote and endnote references, and tables nested in a table cell.
// Each leaves a visible marker in the Markdown and is counted in
// Result.Unsupported, so the connector reports it per document. Headers,
// footers, comments and field codes are not part of the body and are
// left out.
//
// A document that is not a .docx, or is past MaxFileBytes, MaxPartBytes
// or MaxDepth, is refused with an error; the connector then sends it
// unreadable.
package docxmd

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Unsupported is one kind of content the converter could not render, with
// the number of times it occurs in the document.
type Unsupported struct {
	// Name is the kind, for example "image" or "equation".
	Name  string
	Count int
}

// Result is a converted document.
type Result struct {
	Markdown string
	// Unsupported is sorted by Name; nil when everything converted.
	Unsupported []Unsupported
}

// Marker is the text left in the Markdown where unsupported content was.
func Marker(name string) string {
	return "[Unsupported Word content: " + name + "]"
}

// Limits on one document. The renderer recurses over the element tree, so
// an unbounded nesting depth would overflow the goroutine stack; a zip
// part's declared size can lie, so every read is bounded too.
const (
	// MaxFileBytes is the largest .docx file accepted.
	MaxFileBytes = 16 << 20
	// MaxPartBytes is the largest uncompressed XML part read.
	MaxPartBytes = 8 << 20
	// MaxDepth is the deepest element nesting accepted.
	MaxDepth = 512
)

var (
	// ErrNotDocx is returned for a file that is not a readable .docx.
	ErrNotDocx = errors.New("docxmd: not a .docx document")
	// ErrTooLarge is returned for a file or part past its size limit.
	ErrTooLarge = errors.New("docxmd: document too large")
	// ErrTooDeep is returned for a part nested deeper than MaxDepth.
	ErrTooDeep = errors.New("docxmd: document nested too deeply")
)

// Relationship types, as Word writes them (transitional OOXML).
const (
	relOfficeDocument = "/officeDocument"
	relStyles         = "/styles"
	relNumbering      = "/numbering"
	relHyperlink      = "/hyperlink"
)

// Convert converts a .docx file to Markdown.
func Convert(docx []byte) (Result, error) {
	if len(docx) > MaxFileBytes {
		return Result{}, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(docx), MaxFileBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNotDocx, err)
	}
	pkg := &pkg{files: map[string]*zip.File{}}
	for _, f := range zr.File {
		pkg.files[f.Name] = f
	}
	main := "word/document.xml"
	if rels, err := pkg.rels("_rels/.rels", ""); err != nil {
		return Result{}, err
	} else if t := rels.target(relOfficeDocument); t != "" {
		main = t
	}
	doc, err := pkg.xml(main)
	if err != nil {
		return Result{}, err
	}
	if doc == nil {
		return Result{}, fmt.Errorf("%w: no main document part", ErrNotDocx)
	}
	rels, err := pkg.rels(path.Join(path.Dir(main), "_rels", path.Base(main)+".rels"), path.Dir(main))
	if err != nil {
		return Result{}, err
	}
	c := &converter{rels: rels, styles: map[string]style{}, numFmt: map[string]map[string]string{}, unsupported: map[string]int{}}
	if t := rels.target(relStyles); t != "" {
		st, err := pkg.xml(t)
		if err != nil {
			return Result{}, err
		}
		c.loadStyles(st)
	}
	if t := rels.target(relNumbering); t != "" {
		num, err := pkg.xml(t)
		if err != nil {
			return Result{}, err
		}
		c.loadNumbering(num)
	}
	body := doc.child("body")
	if body == nil {
		return Result{}, fmt.Errorf("%w: no document body", ErrNotDocx)
	}
	md := c.render(c.blocks(body.children))
	return Result{Markdown: md, Unsupported: c.report()}, nil
}

// pkg is the opened zip package.
type pkg struct {
	files map[string]*zip.File
}

// read returns a part's bytes, or nil when the part is absent.
func (p *pkg) read(name string) ([]byte, error) {
	f, ok := p.files[strings.TrimPrefix(name, "/")]
	if !ok {
		return nil, nil
	}
	if f.UncompressedSize64 > MaxPartBytes {
		return nil, fmt.Errorf("%w: part %s is %d bytes, limit %d", ErrTooLarge, f.Name, f.UncompressedSize64, MaxPartBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %v", ErrNotDocx, f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, MaxPartBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrNotDocx, f.Name, err)
	}
	if len(b) > MaxPartBytes {
		return nil, fmt.Errorf("%w: part %s is over %d bytes", ErrTooLarge, f.Name, MaxPartBytes)
	}
	return b, nil
}

// xml parses a part, or returns nil when the part is absent.
func (p *pkg) xml(name string) (*node, error) {
	b, err := p.read(name)
	if err != nil || b == nil {
		return nil, err
	}
	n, err := parse(b)
	if err != nil {
		return nil, fmt.Errorf("part %s: %w", name, err)
	}
	return n, nil
}

// relationships maps a part's relationship ids to their targets.
type relationships struct {
	byID   map[string]rel
	byType map[string]string
}

type rel struct {
	target   string
	external bool
}

// target is the package path of the first relationship whose type ends
// with suffix, or "".
func (r relationships) target(suffix string) string {
	return r.byType[suffix]
}

// rels reads a relationships part. dir is the directory internal targets
// resolve against.
func (p *pkg) rels(name, dir string) (relationships, error) {
	out := relationships{byID: map[string]rel{}, byType: map[string]string{}}
	root, err := p.xml(name)
	if err != nil || root == nil {
		return out, err
	}
	for _, r := range root.children {
		if r.name != "Relationship" {
			continue
		}
		t, typ := r.attr("Target"), r.attr("Type")
		external := strings.EqualFold(r.attr("TargetMode"), "External")
		if !external {
			if strings.HasPrefix(t, "/") {
				t = strings.TrimPrefix(t, "/")
			} else {
				t = path.Join(dir, t)
			}
		}
		out.byID[r.attr("Id")] = rel{target: t, external: external}
		for _, suffix := range []string{relOfficeDocument, relStyles, relNumbering} {
			if strings.HasSuffix(typ, suffix) && out.byType[suffix] == "" && !external {
				out.byType[suffix] = t
			}
		}
	}
	return out, nil
}

// node is a parsed XML element, or a text node when name is "". Names are
// local names: Word's prefixes vary between transitional and strict
// OOXML, but its local names do not.
type node struct {
	name     string
	attrs    map[string]string
	children []*node
	text     string
}

func (n *node) attr(name string) string { return n.attrs[name] }

func (n *node) child(name string) *node {
	for _, ch := range n.children {
		if ch.name == name {
			return ch
		}
	}
	return nil
}

// on reports whether a toggle property (w:b, w:i, ...) is set: present,
// and its w:val not false.
func on(n *node) bool {
	if n == nil {
		return false
	}
	switch strings.ToLower(n.attr("val")) {
	case "0", "false", "off", "none":
		return false
	}
	return true
}

func parse(b []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	root := &node{}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotDocx, err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) > MaxDepth {
				return nil, fmt.Errorf("%w: limit %d", ErrTooDeep, MaxDepth)
			}
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.children = append(top.children, &node{text: string(t)})
		}
	}
	for _, ch := range root.children {
		if ch.name != "" {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("%w: empty part", ErrNotDocx)
}

// style is what the converter reads from a paragraph style.
type style struct {
	name  string // lower case, e.g. "heading 1"
	numID string // a list style's numbering
	ilvl  string
}

// converter holds a document's styles, numbering and hyperlink targets,
// and counts what it could not render.
type converter struct {
	rels        relationships
	styles      map[string]style
	numFmt      map[string]map[string]string // numId -> ilvl -> numFmt
	unsupported map[string]int
}

func (c *converter) loadStyles(root *node) {
	if root == nil {
		return
	}
	for _, s := range root.children {
		if s.name != "style" {
			continue
		}
		st := style{}
		if n := s.child("name"); n != nil {
			st.name = strings.ToLower(strings.TrimSpace(n.attr("val")))
		}
		if ppr := s.child("pPr"); ppr != nil {
			if num := ppr.child("numPr"); num != nil {
				if id := num.child("numId"); id != nil {
					st.numID = id.attr("val")
				}
				if l := num.child("ilvl"); l != nil {
					st.ilvl = l.attr("val")
				}
			}
		}
		c.styles[s.attr("styleId")] = st
	}
}

func (c *converter) loadNumbering(root *node) {
	if root == nil {
		return
	}
	abstract := map[string]map[string]string{}
	for _, a := range root.children {
		if a.name != "abstractNum" {
			continue
		}
		lvls := map[string]string{}
		for _, l := range a.children {
			if l.name == "lvl" {
				if f := l.child("numFmt"); f != nil {
					lvls[l.attr("ilvl")] = f.attr("val")
				}
			}
		}
		abstract[a.attr("abstractNumId")] = lvls
	}
	for _, n := range root.children {
		if n.name != "num" {
			continue
		}
		if a := n.child("abstractNumId"); a != nil {
			c.numFmt[n.attr("numId")] = abstract[a.attr("val")]
		}
	}
}

func (c *converter) unsupportedContent(name string) string {
	c.unsupported[name]++
	return escapeText(Marker(name))
}

func (c *converter) report() []Unsupported {
	if len(c.unsupported) == 0 {
		return nil
	}
	out := make([]Unsupported, 0, len(c.unsupported))
	for name, n := range c.unsupported {
		out = append(out, Unsupported{Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// block is one rendered block: a paragraph, heading or table, or one list
// item.
type block struct {
	text string
	list *listItem
}

type listItem struct {
	level   int
	ordered bool
}

// blocks renders the block-level children of the body, a table cell or a
// content control.
func (c *converter) blocks(nodes []*node) []block {
	var out []block
	for _, n := range nodes {
		switch n.name {
		case "p":
			if b, ok := c.paragraph(n); ok {
				out = append(out, b)
			}
		case "tbl":
			if t := c.table(n); t != "" {
				out = append(out, block{text: t})
			}
		case "sdt":
			if content := n.child("sdtContent"); content != nil {
				out = append(out, c.blocks(content.children)...)
			}
		case "customXml", "ins", "moveTo":
			out = append(out, c.blocks(n.children)...)
		case "oMathPara":
			out = append(out, block{text: c.unsupportedContent("equation")})
		}
	}
	return out
}

// render joins blocks into the document's Markdown. Consecutive list
// items form one tight list; everything else is separated by a blank
// line. A list item is never indented more than one level below the one
// before it, since a deeper indent would read as a code block.
func (c *converter) render(blocks []block) string {
	var b strings.Builder
	prevList := false
	depth := -1
	for i, bl := range blocks {
		if i > 0 {
			if bl.list != nil && prevList {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		if bl.list == nil {
			b.WriteString(bl.text)
			prevList, depth = false, -1
			continue
		}
		level := min(bl.list.level, depth+1)
		depth = level
		marker := "- "
		if bl.list.ordered {
			marker = "1. "
		}
		indent := strings.Repeat("    ", level)
		b.WriteString(indent + marker + indentLines(bl.text, indent+strings.Repeat(" ", len(marker))))
		prevList = true
	}
	return b.String()
}

func indentLines(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = indent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

var headingStyle = regexp.MustCompile(`^heading ([1-6])$`)

// paragraph renders one w:p. An empty paragraph renders nothing.
func (c *converter) paragraph(p *node) (block, bool) {
	var styleID, numID, ilvl string
	if ppr := p.child("pPr"); ppr != nil {
		if s := ppr.child("pStyle"); s != nil {
			styleID = s.attr("val")
		}
		if num := ppr.child("numPr"); num != nil {
			if id := num.child("numId"); id != nil {
				numID = id.attr("val")
			}
			if l := num.child("ilvl"); l != nil {
				ilvl = l.attr("val")
			}
		}
	}
	st := c.styles[styleID]
	if numID == "" {
		numID = st.numID
	}
	if ilvl == "" {
		ilvl = st.ilvl
	}
	if ilvl == "" {
		ilvl = "0"
	}
	level := 0
	if m := headingStyle.FindStringSubmatch(st.name); m != nil {
		level, _ = strconv.Atoi(m[1])
	} else if st.name == "title" {
		level = 1
	}
	if level > 0 {
		text := finishInline(c.inline(p.children), " ")
		if text == "" {
			return block{}, false
		}
		return block{text: strings.Repeat("#", level) + " " + text}, true
	}
	text := finishInline(c.inline(p.children), "\\\n")
	if text == "" {
		return block{}, false
	}
	text = escapeLineStart(text)
	if numID != "" && numID != "0" {
		lvl, _ := strconv.Atoi(ilvl)
		fmtVal := c.numFmt[numID][ilvl]
		ordered := fmtVal != "" && fmtVal != "bullet" && fmtVal != "none"
		return block{text: text, list: &listItem{level: max(0, lvl), ordered: ordered}}, true
	}
	return block{text: text}, true
}

// segment is a run of text with one set of formatting.
type segment struct {
	text                 string // escaped Markdown
	bold, italic, strike bool
}

// inline renders a paragraph's (or hyperlink's) inline content.
func (c *converter) inline(nodes []*node) string {
	var segs []segment
	c.collect(nodes, &segs)
	// Merge neighbours with the same formatting, so `**a****b**` reads
	// `**ab**`.
	var merged []segment
	for _, s := range segs {
		if n := len(merged); n > 0 && merged[n-1].bold == s.bold && merged[n-1].italic == s.italic && merged[n-1].strike == s.strike {
			merged[n-1].text += s.text
			continue
		}
		merged = append(merged, s)
	}
	var b strings.Builder
	for _, s := range merged {
		t := s.text
		if s.strike {
			t = wrap("~~", t)
		}
		if s.italic {
			t = wrap("*", t)
		}
		if s.bold {
			t = wrap("**", t)
		}
		b.WriteString(t)
	}
	return b.String()
}

// collect appends the segments of inline content to segs.
func (c *converter) collect(nodes []*node, segs *[]segment) {
	for _, n := range nodes {
		switch n.name {
		case "r":
			c.run(n, segs)
		case "hyperlink":
			*segs = append(*segs, segment{text: c.hyperlink(n)})
		case "ins", "moveTo", "smartTag", "customXml", "fldSimple", "dir", "bdo":
			c.collect(n.children, segs)
		case "sdt":
			if content := n.child("sdtContent"); content != nil {
				c.collect(content.children, segs)
			}
		case "oMath", "oMathPara":
			*segs = append(*segs, segment{text: c.unsupportedContent("equation")})
		}
	}
}

// run renders one w:r.
func (c *converter) run(r *node, segs *[]segment) {
	s := segment{}
	if rpr := r.child("rPr"); rpr != nil {
		s.bold = on(rpr.child("b"))
		s.italic = on(rpr.child("i"))
		s.strike = on(rpr.child("strike")) || on(rpr.child("dstrike"))
	}
	var b strings.Builder
	for _, ch := range r.children {
		switch ch.name {
		case "t":
			b.WriteString(escapeText(textOf(ch)))
		case "tab":
			b.WriteString(" ")
		case "br", "cr":
			if t := ch.attr("type"); t == "page" || t == "column" {
				b.WriteString(" ")
				continue
			}
			b.WriteString(lineBreak)
		case "noBreakHyphen":
			b.WriteString("-")
		case "drawing", "pict":
			b.WriteString(c.unsupportedContent("image"))
		case "object":
			b.WriteString(c.unsupportedContent("embedded object"))
		case "footnoteReference":
			b.WriteString(c.unsupportedContent("footnote"))
		case "endnoteReference":
			b.WriteString(c.unsupportedContent("endnote"))
		}
	}
	s.text = b.String()
	if s.text != "" {
		*segs = append(*segs, s)
	}
}

// hyperlink renders a w:hyperlink: a Markdown link for an external http(s)
// or mailto target, else its text alone.
func (c *converter) hyperlink(h *node) string {
	text := c.inline(h.children)
	r, ok := c.rels.byID[h.attr("id")]
	if !ok || !r.external || !linkable(r.target) {
		return text
	}
	if strings.TrimSpace(strings.ReplaceAll(text, lineBreak, "")) == "" {
		text = escapeText(r.target)
	}
	return "[" + text + "](" + linkDest(r.target) + ")"
}

func linkable(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:")
}

func textOf(n *node) string {
	var b strings.Builder
	for _, ch := range n.children {
		if ch.name == "" {
			b.WriteString(ch.text)
		}
	}
	return b.String()
}

// table renders a GFM pipe table, the first row as the header row since
// GFM requires one. A merged cell keeps its column count: a horizontal
// span adds empty cells, a vertical continuation is empty.
func (c *converter) table(t *node) string {
	var rows [][]string
	for _, tr := range t.children {
		if tr.name != "tr" {
			continue
		}
		var row []string
		for _, tc := range tr.children {
			if tc.name != "tc" {
				continue
			}
			span := 1
			continued := false
			if pr := tc.child("tcPr"); pr != nil {
				if g := pr.child("gridSpan"); g != nil {
					if n, err := strconv.Atoi(g.attr("val")); err == nil && n > 1 && n <= 64 {
						span = n
					}
				}
				if v := pr.child("vMerge"); v != nil && v.attr("val") != "restart" {
					continued = true
				}
			}
			cell := ""
			if !continued {
				cell = c.cell(tc)
			}
			row = append(row, cell)
			for i := 1; i < span; i++ {
				row = append(row, "")
			}
		}
		rows = append(rows, row)
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return ""
	}
	line := func(cells []string) string {
		for len(cells) < cols {
			cells = append(cells, "")
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	out := []string{line(rows[0]), line(sep)}
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

// cell flattens a table cell onto one line: blocks and hard breaks become
// <br>, since a pipe table cannot hold a newline. A table nested in the
// cell is not rendered.
func (c *converter) cell(tc *node) string {
	var parts []string
	for _, ch := range tc.children {
		if ch.name == "tbl" {
			parts = append(parts, c.unsupportedContent("nested table"))
			continue
		}
		for _, b := range c.blocks([]*node{ch}) {
			text := b.text
			if b.list != nil {
				text = "- " + text
			}
			parts = append(parts, strings.ReplaceAll(strings.ReplaceAll(text, "\\\n", "<br>"), "\n", "<br>"))
		}
	}
	return strings.ReplaceAll(strings.Join(parts, "<br>"), "|", `\|`)
}

// lineBreak marks a hard break until the paragraph is finished, so
// whitespace collapsing cannot eat it.
const lineBreak = "\x01"

var spaces = regexp.MustCompile(`[ \t\r\n\x{00A0}]+`)

// finishInline collapses whitespace, trims the paragraph, and turns
// line-break markers into br.
func finishInline(s, br string) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	parts := strings.Split(s, lineBreak)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.TrimSpace(strings.Join(parts, br))
}

// wrap puts an emphasis marker round text, keeping surrounding whitespace
// outside it (`** x **` is not emphasis in Markdown).
func wrap(marker, s string) string {
	inner := strings.TrimSpace(strings.Trim(s, lineBreak))
	if inner == "" {
		return s
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " "+lineBreak))]
	trail := s[len(strings.TrimRight(s, " "+lineBreak)):]
	return lead + marker + strings.Trim(s, " "+lineBreak) + marker + trail
}

func linkDest(u string) string {
	u = strings.TrimSpace(u)
	if strings.ContainsAny(u, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(u) + ">"
	}
	return u
}

var textEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`, `<`, `\<`, `~`, `\~`,
	"\x01", "",
)

func escapeText(s string) string { return textEscaper.Replace(s) }

var (
	blockStart  = regexp.MustCompile(`^(?:#|>|[-+=] |[-+=]$|\d+[.)](?: |$))`)
	leadingNums = regexp.MustCompile(`^\d+`)
)

// escapeLineStart stops a paragraph that begins like a heading, quote or
// list item from being read as one.
func escapeLineStart(p string) string {
	if !blockStart.MatchString(p) {
		return p
	}
	if loc := leadingNums.FindStringIndex(p); loc != nil {
		return p[:loc[1]] + `\` + p[loc[1]:]
	}
	return `\` + p
}

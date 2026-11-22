// Package storagemd converts a Confluence page body in storage format
// (XHTML with ac:/ri: elements) to Markdown, the platform's one policy
// body format (#2813, #2855).
//
// Convert is a pure function: the same body always produces the same
// Markdown, byte for byte, so the platform's body hash is stable across
// sync runs. Nothing is dropped silently. A macro or Confluence element
// the converter has no Markdown form for is left in the output as a
// visible marker and listed in Result.Unsupported, so the connector can
// report it against the page.
package storagemd

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Unsupported is one macro or element the converter could not render,
// with the number of times it occurs in the page.
type Unsupported struct {
	// Name is the macro name (for example "toc") for a structured macro,
	// or the prefixed element name (for example "ac:image") for any other
	// Confluence element.
	Name  string
	Count int
}

// Result is a converted page body.
type Result struct {
	Markdown string
	// Unsupported is sorted by Name; nil when everything converted.
	Unsupported []Unsupported
}

// MacroMarker is the text left in the Markdown where an unsupported macro
// was. It is exported so callers and tests can recognise it.
func MacroMarker(name string) string {
	return "[Unsupported Confluence macro: " + name + "]"
}

// ElementMarker is the text left where an unsupported Confluence element
// (one that is not a macro) was.
func ElementMarker(name string) string {
	return "[Unsupported Confluence element: " + name + "]"
}

// Limits on one page body. The renderer recurses over the element tree,
// so an unbounded nesting depth (writable through Confluence's REST API)
// would overflow the goroutine stack, which is fatal and kills the
// connector. A body past either limit is refused with an error for that
// page instead.
const (
	// MaxDepth is the deepest element nesting accepted. Real pages nest a
	// few dozen levels at most.
	MaxDepth = 512
	// MaxBodyBytes is the largest storage-format body accepted.
	MaxBodyBytes = 8 << 20
)

var (
	// ErrTooDeep is returned for a body nested deeper than MaxDepth.
	ErrTooDeep = errors.New("storagemd: body nested too deeply")
	// ErrBodyTooLarge is returned for a body larger than MaxBodyBytes.
	ErrBodyTooLarge = errors.New("storagemd: body too large")
)

// Convert converts a storage-format body to Markdown. It returns an error
// when the body is not parseable XML or is past MaxDepth or MaxBodyBytes.
func Convert(storage string) (Result, error) {
	root, err := parse(storage)
	if err != nil {
		return Result{}, err
	}
	c := &converter{counts: map[string]int{}}
	md := strings.Join(c.blocks(root.children), "\n\n")
	if md != "" {
		md += "\n"
	}
	res := Result{Markdown: md}
	for name, n := range c.counts {
		res.Unsupported = append(res.Unsupported, Unsupported{Name: name, Count: n})
	}
	sort.Slice(res.Unsupported, func(i, j int) bool {
		return res.Unsupported[i].Name < res.Unsupported[j].Name
	})
	return res, nil
}

// node is the parsed tree. An element has a name; a text node has none.
type node struct {
	name     string // "p", "ac:structured-macro"; "" for text
	attrs    map[string]string
	text     string
	children []*node
}

func (n *node) attr(name string) string { return n.attrs[name] }

func qualified(n xml.Name) string {
	if n.Space == "" {
		return strings.ToLower(n.Local)
	}
	return n.Space + ":" + n.Local
}

// voidElements are the HTML elements a body may leave unclosed. It is
// xml.HTMLAutoClose without "link": the decoder matches on the local name
// only, so that entry would auto-close every <ac:link>.
var voidElements = []string{"br", "hr", "img", "col", "area", "input", "wbr"}

// parse reads the body into a tree. Storage format uses HTML entities
// (&nbsp;, &rsquo;) and the ac:/ri: prefixes without declaring them, so the
// decoder runs non-strict with the HTML entity table; an undeclared prefix
// is kept as the name's Space.
func parse(storage string) (*node, error) {
	if len(storage) > MaxBodyBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrBodyTooLarge, len(storage), MaxBodyBytes)
	}
	d := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	d.Strict = false
	d.AutoClose = voidElements
	d.Entity = xml.HTMLEntity

	sentinel := &node{}
	stack := []*node{sentinel}
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("storagemd: parse storage format: %w", err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: qualified(t.Name), attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[qualified(a.Name)] = a.Value
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
			// The stack holds the sentinel and the <root> wrapper too.
			if len(stack)-2 > MaxDepth {
				return nil, fmt.Errorf("%w: limit %d", ErrTooDeep, MaxDepth)
			}
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			top.children = append(top.children, &node{text: string(t)})
		}
	}
	// The wrapper <root> element is the sentinel's only child.
	return sentinel.children[0], nil
}

type converter struct {
	counts map[string]int
}

func (c *converter) unsupportedMacro(name string) string {
	c.counts[name]++
	return MacroMarker(name)
}

func (c *converter) unsupportedElement(name string) string {
	c.counts[name]++
	return ElementMarker(name)
}

// Macros with a Markdown equivalent.
var (
	panelMacros = map[string]string{
		"info": "Info", "note": "Note", "warning": "Warning", "tip": "Tip", "panel": "",
	}
	codeMacros = map[string]bool{"code": true, "noformat": true}
	// Container macros whose body is ordinary page content.
	transparentMacros = map[string]bool{"excerpt": true, "section": true, "column": true, "expand": true}
)

func macroName(n *node) string {
	return strings.ToLower(n.attr("ac:name"))
}

func isMacro(n *node) bool {
	return n.name == "ac:structured-macro" || n.name == "ac:macro"
}

var blockElements = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "table": true, "pre": true, "blockquote": true, "hr": true,
	"div": true, "section": true, "ac:layout": true, "ac:layout-section": true,
	"ac:layout-cell": true, "ac:rich-text-body": true, "ac:task-list": true,
}

func isBlock(n *node) bool {
	if n.name == "" {
		return false
	}
	if isMacro(n) {
		name := macroName(n)
		_, panel := panelMacros[name]
		return panel || codeMacros[name] || transparentMacros[name]
	}
	return blockElements[n.name]
}

// blocks renders a sequence of nodes as Markdown blocks. Runs of inline
// nodes between block elements become paragraphs.
func (c *converter) blocks(nodes []*node) []string {
	var out []string
	var run []*node
	flush := func() {
		if p := finishInline(c.inline(run), "\\\n"); p != "" {
			out = append(out, escapeLineStart(p))
		}
		run = nil
	}
	for _, n := range nodes {
		if !isBlock(n) {
			run = append(run, n)
			continue
		}
		flush()
		out = append(out, c.block(n)...)
	}
	flush()
	return out
}

func (c *converter) block(n *node) []string {
	switch n.name {
	case "p":
		return c.blocks(n.children)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.name[1] - '0')
		text := finishInline(c.inline(n.children), " ")
		if text == "" {
			return nil
		}
		return []string{strings.Repeat("#", level) + " " + text}
	case "ul", "ol":
		if l := c.list(n); l != "" {
			return []string{l}
		}
		return nil
	case "table":
		if t := c.table(n); t != "" {
			return []string{t}
		}
		return nil
	case "pre":
		return []string{fence(rawText(n), "")}
	case "blockquote":
		return quote(c.blocks(n.children))
	case "hr":
		return []string{"---"}
	case "ac:task-list":
		if l := c.taskList(n); l != "" {
			return []string{l}
		}
		return nil
	case "ac:structured-macro", "ac:macro":
		return c.blockMacro(n)
	}
	// div, layout cells, rich-text bodies: containers only.
	return c.blocks(n.children)
}

func (c *converter) blockMacro(n *node) []string {
	name := macroName(n)
	params := macroParams(n)
	if label, ok := panelMacros[name]; ok {
		var body []string
		if heading := joinNonEmpty(label, params["title"]); heading != "" {
			body = append(body, "**"+escapeText(heading)+"**")
		}
		body = append(body, c.blocks(childrenOf(n, "ac:rich-text-body"))...)
		return quote(body)
	}
	if codeMacros[name] {
		var out []string
		if title := params["title"]; title != "" {
			out = append(out, "**"+escapeText(title)+"**")
		}
		var body string
		for _, b := range childNodes(n, "ac:plain-text-body") {
			body += rawText(b)
		}
		return append(out, fence(body, params["language"]))
	}
	// Transparent containers. expand's title is the only text a reader saw
	// before opening it, so it is kept as a bold line.
	var out []string
	if title := params["title"]; name == "expand" && title != "" {
		out = append(out, "**"+escapeText(title)+"**")
	}
	return append(out, c.blocks(childrenOf(n, "ac:rich-text-body"))...)
}

func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + ": " + b
}

func macroParams(n *node) map[string]string {
	params := map[string]string{}
	for _, ch := range n.children {
		if ch.name == "ac:parameter" {
			params[strings.ToLower(ch.attr("ac:name"))] = strings.TrimSpace(rawText(ch))
		}
	}
	return params
}

func childNodes(n *node, name string) []*node {
	var out []*node
	for _, ch := range n.children {
		if ch.name == name {
			out = append(out, ch)
		}
	}
	return out
}

func childrenOf(n *node, name string) []*node {
	var out []*node
	for _, ch := range childNodes(n, name) {
		out = append(out, ch.children...)
	}
	return out
}

func quote(blocks []string) []string {
	if len(blocks) == 0 {
		return nil
	}
	lines := strings.Split(strings.Join(blocks, "\n\n"), "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return []string{strings.Join(lines, "\n")}
}

// list renders ul/ol. Items are tight; an item's later blocks are indented
// under its marker so nested lists stay nested.
func (c *converter) list(n *node) string {
	ordered := n.name == "ol"
	start := 1
	if s, err := strconv.Atoi(n.attr("start")); ordered && err == nil && s >= 0 {
		start = s
	}
	var items []string
	i := start
	for _, li := range n.children {
		if li.name != "li" {
			if li.name != "" {
				// A stray element directly in a list: keep its content.
				items = append(items, c.blocks([]*node{li})...)
			}
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(i) + ". "
			i++
		}
		items = append(items, listItem(marker, c.blocks(li.children)))
	}
	return strings.Join(items, "\n")
}

func (c *converter) taskList(n *node) string {
	var items []string
	for _, task := range childNodes(n, "ac:task") {
		box := "[ ] "
		if len(childNodes(task, "ac:task-status")) > 0 &&
			strings.TrimSpace(rawText(childNodes(task, "ac:task-status")[0])) == "complete" {
			box = "[x] "
		}
		items = append(items, listItem("- "+box, c.blocks(childrenOf(task, "ac:task-body"))))
	}
	return strings.Join(items, "\n")
}

func listItem(marker string, parts []string) string {
	indent := strings.Repeat(" ", len(marker))
	var b strings.Builder
	b.WriteString(strings.TrimRight(marker, " "))
	for i, p := range parts {
		switch {
		case i == 0:
			b.WriteString(" ")
		case isListBlock(p):
			b.WriteString("\n")
		default:
			b.WriteString("\n\n")
		}
		b.WriteString(indentLines(p, indent, i == 0))
	}
	return b.String()
}

var listStart = regexp.MustCompile(`^(?:[-*+] |\d+\. )`)

func isListBlock(s string) bool { return listStart.MatchString(s) }

func indentLines(s, indent string, skipFirst bool) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if (i == 0 && skipFirst) || l == "" {
			continue
		}
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// table renders a GFM pipe table. The first row is the header row, since
// GFM requires one.
func (c *converter) table(n *node) string {
	var rows [][]string
	var walk func(*node)
	walk = func(n *node) {
		for _, ch := range n.children {
			switch ch.name {
			case "tr":
				var row []string
				for _, cell := range ch.children {
					if cell.name == "td" || cell.name == "th" {
						row = append(row, c.cell(cell))
					}
				}
				rows = append(rows, row)
			case "thead", "tbody", "tfoot":
				walk(ch)
			}
		}
	}
	walk(n)
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
	out := []string{line(rows[0]), line(repeat("---", cols))}
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// cell flattens a table cell onto one line: blocks and hard breaks become
// <br>, since a pipe table cannot hold a newline.
func (c *converter) cell(n *node) string {
	var parts []string
	for _, b := range c.blocks(n.children) {
		parts = append(parts, strings.ReplaceAll(strings.ReplaceAll(b, "\\\n", "<br>"), "\n", "<br>"))
	}
	return strings.ReplaceAll(strings.Join(parts, "<br>"), "|", `\|`)
}

// lineBreak marks a <br> until the paragraph is finished, so whitespace
// collapsing cannot eat it.
const lineBreak = ""

var spaces = regexp.MustCompile(`[ \t\r\n\x{00A0}]+`)

// finishInline collapses whitespace the way HTML rendering does, trims the
// paragraph, and turns line-break markers into br.
func finishInline(s, br string) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	parts := strings.Split(s, lineBreak)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.TrimSpace(strings.Join(parts, br))
}

func (c *converter) inline(nodes []*node) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(c.inlineNode(n))
	}
	return b.String()
}

func (c *converter) inlineNode(n *node) string {
	if n.name == "" {
		return escapeText(spaces.ReplaceAllString(n.text, " "))
	}
	if isBlock(n) {
		// A block inside inline content (a list inside a heading, say):
		// keep its text on the line.
		return " " + strings.Join(c.block(n), " ") + " "
	}
	switch n.name {
	case "strong", "b":
		return wrap("**", c.inline(n.children))
	case "em", "i":
		return wrap("*", c.inline(n.children))
	case "s", "del", "strike":
		return wrap("~~", c.inline(n.children))
	case "code":
		return codeSpan(rawText(n))
	case "br":
		return lineBreak
	case "a":
		text := c.inline(n.children)
		href := n.attr("href")
		if href == "" {
			return text
		}
		if strings.TrimSpace(text) == "" {
			text = escapeText(href)
		}
		return "[" + text + "](" + linkDest(href) + ")"
	case "time":
		return escapeText(n.attr("datetime"))
	case "ac:link":
		return c.confluenceLink(n)
	case "ac:emoticon":
		if fb := n.attr("ac:emoji-fallback"); fb != "" {
			return escapeText(fb)
		}
		return ":" + escapeText(n.attr("ac:name")) + ":"
	case "ac:image":
		for _, ch := range childNodes(n, "ri:url") {
			if u := ch.attr("ri:value"); u != "" {
				return "![" + escapeText(n.attr("ac:alt")) + "](" + linkDest(u) + ")"
			}
		}
		return c.unsupportedElement("ac:image")
	case "ac:structured-macro", "ac:macro":
		return c.unsupportedMacro(macroName(n))
	case "ac:placeholder", "ac:parameter":
		// Editor-only text, never shown on the published page.
		return ""
	}
	if strings.HasPrefix(n.name, "ac:") || strings.HasPrefix(n.name, "ri:") {
		return c.unsupportedElement(n.name)
	}
	// span, u, sup, sub, font and other presentational HTML: keep the text.
	return c.inline(n.children)
}

// confluenceLink renders an ac:link. A link to a URL keeps its target; a
// link to another page, user or attachment has no URL in storage format,
// so only its text is kept.
func (c *converter) confluenceLink(n *node) string {
	var text string
	for _, ch := range n.children {
		switch ch.name {
		case "ac:link-body":
			text += c.inline(ch.children)
		case "ac:plain-text-link-body":
			text += escapeText(rawText(ch))
		}
	}
	for _, ch := range n.children {
		switch ch.name {
		case "ri:url":
			u := ch.attr("ri:value")
			if strings.TrimSpace(text) == "" {
				text = escapeText(u)
			}
			return "[" + text + "](" + linkDest(u) + ")"
		case "ri:page":
			if strings.TrimSpace(text) == "" {
				text = escapeText(ch.attr("ri:content-title"))
			}
		case "ri:attachment":
			if strings.TrimSpace(text) == "" {
				text = escapeText(ch.attr("ri:filename"))
			}
		}
	}
	if strings.TrimSpace(text) == "" && n.attr("ac:anchor") != "" {
		text = escapeText(n.attr("ac:anchor"))
	}
	return text
}

// wrap puts an emphasis marker round text, keeping surrounding whitespace
// outside it (`** x **` is not emphasis in Markdown).
func wrap(marker, s string) string {
	inner := strings.TrimSpace(s)
	if inner == "" || inner == lineBreak {
		return s
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " "))]
	trail := s[len(strings.TrimRight(s, " ")):]
	return lead + marker + inner + marker + trail
}

func codeSpan(s string) string {
	s = spaces.ReplaceAllString(s, " ")
	if strings.TrimSpace(s) == "" {
		return s
	}
	ticks := strings.Repeat("`", longestRun(s, '`')+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return ticks + " " + s + " " + ticks
	}
	return ticks + s + ticks
}

// fence renders a fenced code block, with a fence longer than any backtick
// run in the body.
func fence(body, lang string) string {
	body = strings.TrimSuffix(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	body = strings.TrimPrefix(body, "\n")
	ticks := strings.Repeat("`", max(3, longestRun(body, '`')+1))
	return ticks + fenceInfo(lang) + "\n" + body + "\n" + ticks
}

// fenceInfo cleans a code macro's language for the fence's info string: a
// newline would end the fence line and a backtick is not allowed there, so
// either could turn page text into Markdown structure.
func fenceInfo(lang string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '`' {
			return -1
		}
		return r
	}, lang))
}

func longestRun(s string, r rune) int {
	best, cur := 0, 0
	for _, ch := range s {
		if ch == r {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}

// rawText is the unescaped text under n, with <br> as a newline.
func rawText(n *node) string {
	if n.name == "" {
		return n.text
	}
	if n.name == "br" {
		return "\n"
	}
	var b strings.Builder
	for _, ch := range n.children {
		b.WriteString(rawText(ch))
	}
	return b.String()
}

func linkDest(u string) string {
	u = strings.TrimSpace(u)
	if strings.ContainsAny(u, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(u) + ">"
	}
	return u
}

var textEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`, `<`, `\<`,
)

func escapeText(s string) string { return textEscaper.Replace(s) }

var blockStart = regexp.MustCompile(`^(?:#|>|[-+=] |[-+=]$|\d+[.)](?: |$))`)

// escapeLineStart stops a paragraph that begins like a heading, quote or
// list item from being read as one.
func escapeLineStart(p string) string {
	if !blockStart.MatchString(p) {
		return p
	}
	if loc := regexp.MustCompile(`^\d+`).FindStringIndex(p); loc != nil {
		return p[:loc[1]] + `\` + p[loc[1]:]
	}
	return `\` + p
}

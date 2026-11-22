package storagemd

import (
	"sort"
	"strings"
)

// PropertiesMacro is the macro name of Confluence's Page Properties macro
// in storage format.
const PropertiesMacro = "details"

// Property is one row of a Page Properties table: the key cell's text and
// the value cell's text. Value keeps one line per paragraph, list item or
// line break in the cell.
type Property struct {
	Key   string
	Value string
}

// Page is a converted policy page: the body as Markdown, and the page's
// Page Properties table.
type Page struct {
	Result
	// Properties are the rows of the first Page Properties macro on the
	// page, in table order. Nil when HasProperties is false.
	Properties []Property
	// HasProperties is false when the page has no Page Properties macro.
	HasProperties bool
}

// ConvertPage converts a policy page. The first Page Properties macro is
// read as the page's properties and left out of the Markdown, since it is
// metadata, not policy text. Any further Page Properties macro is page
// content the converter cannot render, so it is marked and reported as
// unsupported like any other macro.
//
// It returns an error under the same conditions as Convert.
func ConvertPage(storage string) (Page, error) {
	root, err := parse(storage)
	if err != nil {
		return Page{}, err
	}
	var page Page
	if m := detachFirstMacro(root, PropertiesMacro); m != nil {
		page.HasProperties = true
		page.Properties = propertiesOf(m)
	}
	c := &converter{counts: map[string]int{}}
	md := strings.Join(c.blocks(root.children), "\n\n")
	if md != "" {
		md += "\n"
	}
	page.Markdown = md
	for name, n := range c.counts {
		page.Unsupported = append(page.Unsupported, Unsupported{Name: name, Count: n})
	}
	sort.Slice(page.Unsupported, func(i, j int) bool {
		return page.Unsupported[i].Name < page.Unsupported[j].Name
	})
	return page, nil
}

// detachFirstMacro removes the first macro named name, in document order,
// from the tree and returns it.
func detachFirstMacro(n *node, name string) *node {
	for i, ch := range n.children {
		if isMacro(ch) && macroName(ch) == name {
			n.children = append(n.children[:i:i], n.children[i+1:]...)
			return ch
		}
		if found := detachFirstMacro(ch, name); found != nil {
			return found
		}
	}
	return nil
}

// propertiesOf reads the table in a Page Properties macro. Confluence
// accepts two layouts: one property per row (key cell, value cell), and a
// header row of keys over one row of values. A table whose first row is
// all header cells and has a second row is read as the second layout.
func propertiesOf(macro *node) []Property {
	var rows [][]*node
	var tables []*node
	for _, body := range childNodes(macro, "ac:rich-text-body") {
		tables = append(tables, descendants(body, "table")...)
	}
	if len(tables) > 0 {
		// The first table only.
		for _, tr := range descendants(tables[0], "tr") {
			var cells []*node
			for _, ch := range tr.children {
				if ch.name == "th" || ch.name == "td" {
					cells = append(cells, ch)
				}
			}
			rows = append(rows, cells)
		}
	}
	var out []Property
	add := func(k, v *node) {
		key := strings.Join(cellLines(k), " ")
		if key == "" {
			return
		}
		value := ""
		if v != nil {
			value = strings.Join(cellLines(v), "\n")
		}
		out = append(out, Property{Key: key, Value: value})
	}
	if len(rows) >= 2 && len(rows[0]) > 0 && allHeaders(rows[0]) && !allHeaders(rows[1]) {
		for i, k := range rows[0] {
			var v *node
			if i < len(rows[1]) {
				v = rows[1][i]
			}
			add(k, v)
		}
		return out
	}
	for _, r := range rows {
		switch len(r) {
		case 0:
		case 1:
			add(r[0], nil)
		default:
			add(r[0], r[1])
		}
	}
	return out
}

func allHeaders(cells []*node) bool {
	for _, c := range cells {
		if c.name != "th" {
			return false
		}
	}
	return true
}

// descendants returns the elements named name under n, outermost first,
// without descending into a match.
func descendants(n *node, name string) []*node {
	var out []*node
	for _, ch := range n.children {
		if ch.name == name {
			out = append(out, ch)
			continue
		}
		out = append(out, descendants(ch, name)...)
	}
	return out
}

// cellLines is a property cell's plain text, one entry per paragraph,
// list item or line break, with whitespace collapsed. A date picker value
// (<time datetime>) is its date; a link is its text.
func cellLines(n *node) []string {
	var lines []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(spaces.ReplaceAllString(cur.String(), " ")); s != "" {
			lines = append(lines, s)
		}
		cur.Reset()
	}
	var walk func(*node)
	walk = func(n *node) {
		switch n.name {
		case "":
			cur.WriteString(n.text)
			return
		case "br":
			flush()
			return
		case "time":
			cur.WriteString(n.attr("datetime"))
			return
		case "ac:parameter", "ac:placeholder":
			return
		case "ac:link":
			// The link's own text, or the linked page's title. A user
			// mention has no text in storage format and reads as empty.
			bodies := append(childNodes(n, "ac:link-body"), childNodes(n, "ac:plain-text-link-body")...)
			for _, b := range bodies {
				walk(b)
			}
			if len(bodies) == 0 {
				for _, p := range childNodes(n, "ri:page") {
					cur.WriteString(p.attr("ri:content-title"))
				}
			}
			return
		}
		lineBreaking := n.name == "p" || n.name == "li" || n.name == "div"
		if lineBreaking {
			flush()
		}
		for _, ch := range n.children {
			walk(ch)
		}
		if lineBreaking {
			flush()
		}
	}
	for _, ch := range n.children {
		walk(ch)
	}
	flush()
	return lines
}

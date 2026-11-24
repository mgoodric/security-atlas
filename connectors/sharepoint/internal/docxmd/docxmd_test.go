package docxmd

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the .md.golden files")

// fixture builds a .docx from testdata: the parts under testdata/_common
// (package rels, styles, numbering, hyperlink targets) overlaid with the
// parts under testdata/<name>. The fixtures are kept unzipped so a
// reviewer can read every part; the zip is made here, as Word stores it.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	parts := map[string][]byte{}
	for _, dir := range []string{"_common", name} {
		root := filepath.Join("testdata", dir)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			b, err := os.ReadFile(p)
			parts[filepath.ToSlash(rel)] = b
			return err
		})
		if err != nil {
			t.Fatalf("read fixture %s: %v", dir, err)
		}
	}
	return zipParts(t, parts)
}

func zipParts(t *testing.T, parts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestConvertGolden(t *testing.T) {
	t.Parallel()
	want := map[string][]Unsupported{
		"basic":       nil,
		"lists":       nil,
		"table":       {{Name: "nested table", Count: 1}},
		"unsupported": {{Name: "embedded object", Count: 1}, {Name: "endnote", Count: 1}, {Name: "equation", Count: 2}, {Name: "footnote", Count: 1}, {Name: "image", Count: 2}},
	}
	for name, unsupported := range want {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res, err := Convert(fixture(t, name))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			golden := filepath.Join("testdata", name+".md.golden")
			if *update {
				if err := os.WriteFile(golden, []byte(res.Markdown+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			b, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if got := res.Markdown + "\n"; got != string(b) {
				t.Errorf("Markdown mismatch\n--- got\n%s\n--- want\n%s", got, b)
			}
			if !reflect.DeepEqual(res.Unsupported, unsupported) {
				t.Errorf("Unsupported = %+v, want %+v", res.Unsupported, unsupported)
			}
		})
	}
}

// Markers are escaped text, so they render literally and never as a link.
// The converter's line-break sentinel (U+0001) cannot come from document
// text: XML 1.0 forbids it, and the "control char" refusal below shows the
// parser refuses it.
func TestConvertMarkerEscaped(t *testing.T) {
	t.Parallel()
	res, err := Convert(fixture(t, "unsupported"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, `\[Unsupported Word content: image\]`) {
		t.Errorf("no escaped image marker in %q", res.Markdown)
	}
}

// A document with no package rels, styles or numbering still converts
// from word/document.xml; a list it cannot type renders as bullets.
func TestConvertMinimal(t *testing.T) {
	t.Parallel()
	doc := `<w:document xmlns:w="w"><w:body><w:p><w:pPr><w:numPr><w:numId w:val="7"/></w:numPr></w:pPr><w:r><w:t>item</w:t></w:r></w:p><w:p><w:pPr><w:pStyle w:val="Missing"/></w:pPr><w:r><w:t>text</w:t></w:r></w:p></w:body></w:document>`
	res, err := Convert(zipParts(t, map[string][]byte{"word/document.xml": []byte(doc)}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "- item\n\ntext"; res.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", res.Markdown, want)
	}
}

// The main part is found through the package relationships, wherever it
// is, and an absolute target resolves from the package root.
func TestConvertMainPartFromRels(t *testing.T) {
	t.Parallel()
	rels := `<Relationships xmlns="r"><Relationship Id="rId1" Type="http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument" Target="/doc/main.xml"/></Relationships>`
	doc := `<document><body><p><r><t>strict</t></r></p></body></document>`
	res, err := Convert(zipParts(t, map[string][]byte{"_rels/.rels": []byte(rels), "doc/main.xml": []byte(doc)}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Markdown != "strict" {
		t.Fatalf("Markdown = %q", res.Markdown)
	}
}

func TestConvertRefusals(t *testing.T) {
	t.Parallel()
	deep := "<w:document xmlns:w=\"w\"><w:body>" + strings.Repeat("<w:sdt>", MaxDepth) + strings.Repeat("</w:sdt>", MaxDepth) + "</w:body></w:document>"
	big := zipParts(t, map[string][]byte{"word/document.xml": bytes.Repeat([]byte(" "), MaxPartBytes+1)})
	many := zipParts(t, map[string][]byte{"word/document.xml": []byte("<w:document><w:body>" + strings.Repeat("<a/>", MaxElements) + "</w:body></w:document>")})
	for _, tc := range []struct {
		name string
		docx []byte
		want error
	}{
		{"not a zip", []byte("%PDF-1.7"), ErrNotDocx},
		{"file too large", make([]byte, MaxFileBytes+1), ErrTooLarge},
		{"no document part", zipParts(t, map[string][]byte{"other.xml": []byte("<a/>")}), ErrNotDocx},
		{"no body", zipParts(t, map[string][]byte{"word/document.xml": []byte(`<w:document xmlns:w="w"/>`)}), ErrNotDocx},
		{"unparseable", zipParts(t, map[string][]byte{"word/document.xml": []byte("<w:document><w:body>")}), ErrNotDocx},
		{"empty part", zipParts(t, map[string][]byte{"word/document.xml": []byte("<?xml version=\"1.0\"?>")}), ErrNotDocx},
		{"control char", zipParts(t, map[string][]byte{"word/document.xml": []byte("<a>\x01</a>")}), ErrNotDocx},
		{"too deep", zipParts(t, map[string][]byte{"word/document.xml": []byte(deep)}), ErrTooDeep},
		{"part too large", big, ErrTooLarge},
		{"too many elements", many, ErrTooLarge},
		{"bad rels", zipParts(t, map[string][]byte{"_rels/.rels": []byte("<Relationships>"), "word/document.xml": []byte("<a/>")}), ErrNotDocx},
		{"bad styles", zipParts(t, map[string][]byte{
			"word/document.xml":            []byte(`<document><body/></document>`),
			"word/_rels/document.xml.rels": []byte(`<Relationships><Relationship Id="a" Type="x/styles" Target="styles.xml"/></Relationships>`),
			"word/styles.xml":              []byte("<styles>"),
		}), ErrNotDocx},
		{"bad numbering", zipParts(t, map[string][]byte{
			"word/document.xml":            []byte(`<document><body/></document>`),
			"word/_rels/document.xml.rels": []byte(`<Relationships><Relationship Id="a" Type="x/numbering" Target="numbering.xml"/></Relationships>`),
			"word/numbering.xml":           []byte("<numbering>"),
		}), ErrNotDocx},
		{"bad document rels", zipParts(t, map[string][]byte{
			"word/document.xml":            []byte(`<document><body/></document>`),
			"word/_rels/document.xml.rels": []byte(`<Relationships>`),
		}), ErrNotDocx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Convert(tc.docx); !errors.Is(err, tc.want) {
				t.Fatalf("Convert err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A zip entry whose header understates its size is still cut off at
// MaxPartBytes.
func TestReadBoundsLyingHeader(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "word/document.xml", Method: zip.Store, CompressedSize64: MaxPartBytes + 2, UncompressedSize64: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bytes.Repeat([]byte(" "), MaxPartBytes+2)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(buf.Bytes()); !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrNotDocx) {
		t.Fatalf("Convert err = %v, want a refusal", err)
	}
}

func TestOn(t *testing.T) {
	t.Parallel()
	for val, want := range map[string]bool{"": true, "1": true, "true": true, "on": true, "0": false, "false": false, "off": false, "none": false} {
		n := &node{name: "b", attrs: map[string]string{}}
		if val != "" {
			n.attrs["val"] = val
		}
		if got := on(n); got != want {
			t.Errorf("on(val=%q) = %v, want %v", val, got, want)
		}
	}
	if on(nil) {
		t.Error("on(nil) = true")
	}
}

func TestWrapAndEscape(t *testing.T) {
	t.Parallel()
	if got := wrap("**", "  "); got != "  " {
		t.Errorf("wrap(blank) = %q", got)
	}
	if got := wrap("**", " a "); got != " **a** " {
		t.Errorf("wrap = %q", got)
	}
	if got := escapeLineStart("+ plus"); got != `\+ plus` {
		t.Errorf("escapeLineStart = %q", got)
	}
	if got := escapeLineStart("> quote"); got != `\> quote` {
		t.Errorf("escapeLineStart = %q", got)
	}
	if got := escapeLineStart("12) x"); got != `12\) x` {
		t.Errorf("escapeLineStart = %q", got)
	}
	if got := linkDest(" https://a.example/x "); got != "https://a.example/x" {
		t.Errorf("linkDest = %q", got)
	}
}

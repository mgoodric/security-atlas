package storagemd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Each fixture is testdata/<name>.xhtml (a storage-format body) with its
// expected Markdown in testdata/<name>.md.golden (not .md, so prettier
// leaves the expected bytes alone).
func TestConvertFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		unsupported []Unsupported
	}{
		{name: "basic"},
		{name: "lists"},
		{name: "table"},
		{name: "blocks"},
		{name: "macros"},
		{name: "links"},
		{name: "unsupported", unsupported: []Unsupported{
			{Name: "ac:image", Count: 1},
			{Name: "jira", Count: 1},
			{Name: "toc", Count: 2},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := readFixture(t, tc.name+".xhtml")
			want := readFixture(t, tc.name+".md.golden")

			got, err := Convert(in)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if got.Markdown != want {
				t.Errorf("Markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got.Markdown, want)
			}
			if !reflect.DeepEqual(got.Unsupported, tc.unsupported) {
				t.Errorf("Unsupported = %+v, want %+v", got.Unsupported, tc.unsupported)
			}
		})
	}
}

// The platform hashes the converted body to detect drift, so the same
// input must give the same bytes on every run.
func TestConvertIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"basic", "lists", "table", "blocks", "macros", "links", "unsupported"} {
		in := readFixture(t, name+".xhtml")
		first, err := Convert(in)
		if err != nil {
			t.Fatalf("%s: Convert: %v", name, err)
		}
		for i := range 50 {
			again, err := Convert(in)
			if err != nil {
				t.Fatalf("%s: Convert: %v", name, err)
			}
			if !reflect.DeepEqual(first, again) {
				t.Fatalf("%s: run %d differs from run 0", name, i+1)
			}
		}
	}
}

func TestConvertSmallCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"whitespace only", " \n\t ", ""},
		{"emphasis keeps spaces outside markers", "<p>a<strong> b </strong>c</p>", "a **b** c\n"},
		{"empty emphasis dropped", "<p>a<em> </em>b</p>", "a b\n"},
		{"link without text shows url", `<p><a href="https://x.test/a"></a></p>`, "[https://x.test/a](https://x.test/a)\n"},
		{"link with spaces is bracketed", `<p><a href="https://x.test/a b">t</a></p>`, "[t](<https://x.test/a b>)\n"},
		{"anchor without href is text", `<p><a name="top">Top</a></p>`, "Top\n"},
		{"url ac:link", `<p><ac:link><ri:url ri:value="https://x.test"/><ac:plain-text-link-body><![CDATA[site]]></ac:plain-text-link-body></ac:link></p>`, "[site](https://x.test)\n"},
		{"attachment ac:link", `<p><ac:link><ri:attachment ri:filename="a.pdf"/></ac:link></p>`, "a.pdf\n"},
		{"code span with backtick", "<p><code>a`b</code></p>", "``a`b``\n"},
		{"emoticon fallback", `<p>ok <ac:emoticon ac:name="tick" ac:emoji-fallback="✅"/></p>`, "ok ✅\n"},
		{"emoticon name", `<p><ac:emoticon ac:name="tick"/></p>`, ":tick:\n"},
		{"unknown ac element is marked", `<p><ac:adf-extension>x</ac:adf-extension></p>`, ElementMarker("ac:adf-extension") + "\n"},
		{"placeholder is editor-only", `<p><ac:placeholder>Type here</ac:placeholder>Body</p>`, "Body\n"},
		{"legacy ac:macro panel", `<ac:macro ac:name="tip"><ac:rich-text-body><p>T</p></ac:rich-text-body></ac:macro>`, "> **Tip**\n>\n> T\n"},
		{"panel macro without label", `<ac:structured-macro ac:name="panel"><ac:rich-text-body><p>P</p></ac:rich-text-body></ac:structured-macro>`, "> P\n"},
		{"code macro title kept", `<ac:structured-macro ac:name="code"><ac:parameter ac:name="title">t.sh</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>`, "**t.sh**\n\n```\nx\n```\n"},
		{"layout cells are containers", `<ac:layout><ac:layout-section ac:type="two_equal"><ac:layout-cell><p>L</p></ac:layout-cell><ac:layout-cell><p>R</p></ac:layout-cell></ac:layout-section></ac:layout>`, "L\n\nR\n"},
		{"list item with two paragraphs", "<ul><li><p>a</p><p>b</p></li></ul>", "- a\n\n  b\n"},
		{"pre with br", "<pre>a<br/>b</pre>", "```\na\nb\n```\n"},
		{"empty heading and table dropped", "<h2> </h2><table></table><p>x</p>", "x\n"},
		{"plus at line start", "<p>+ one</p>", "\\+ one\n"},
		{"html entities", "<p>&lt;b&gt; &amp; &copy;</p>", "\\<b> & ©\n"},
		{"stray text in table ignored by rows", "<table><tr><td>a</td><td>b</td></tr><tr><td>c</td><td>d</td></tr></table>", "| a | b |\n| --- | --- |\n| c | d |\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Convert(tc.in)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if got.Markdown != tc.want {
				t.Errorf("Convert(%q)\n got %q\nwant %q", tc.in, got.Markdown, tc.want)
			}
		})
	}
}

func TestConvertRejectsUnparseableBody(t *testing.T) {
	t.Parallel()
	if _, err := Convert("<p>unterminated <![CDATA[ x"); err == nil {
		t.Fatal("Convert accepted an unterminated CDATA section")
	}
}

// A deeply nested body used to recurse until the goroutine stack
// overflowed, a fatal error no recover catches. Each case here must come
// back as an error for that page, with the test process still running.
func TestConvertRefusesHostileBodies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"5 MB of nested <b>", strings.Repeat("<b>", 5<<20/3), ErrTooDeep},
		{"one level past the cap", strings.Repeat("<span>", MaxDepth+1) + "x", ErrTooDeep},
		{"nested lists past the cap", strings.Repeat("<ul><li>", MaxDepth/2+1), ErrTooDeep},
		{"oversized body", "<p>" + strings.Repeat("a", MaxBodyBytes) + "</p>", ErrBodyTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := Convert(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Convert error = %v, want %v", err, tc.want)
			}
			if res.Markdown != "" || res.Unsupported != nil {
				t.Errorf("Convert returned output with an error: %+v", res)
			}
		})
	}
}

func TestConvertAcceptsNestingAtTheCap(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("<span>", MaxDepth) + "deep" + strings.Repeat("</span>", MaxDepth)
	got, err := Convert(in)
	if err != nil {
		t.Fatalf("Convert at MaxDepth: %v", err)
	}
	if got.Markdown != "deep\n" {
		t.Errorf("Markdown = %q, want %q", got.Markdown, "deep\n")
	}
}

// Attribute values reach the output, so they are escaped or cleaned like
// any other page text.
func TestConvertCleansAttributeText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"emoticon fallback escaped", `<p><ac:emoticon ac:name="x" ac:emoji-fallback="[a](b) *c*"/></p>`, "\\[a\\](b) \\*c\\*\n"},
		{"emoticon name escaped", `<p><ac:emoticon ac:name="a_b*"/></p>`, ":a\\_b\\*:\n"},
		{"code language newline and backticks stripped", "<ac:structured-macro ac:name=\"code\"><ac:parameter ac:name=\"language\">go\n# Heading```</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>", "```go# Heading\nx\n```\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Convert(tc.in)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if got.Markdown != tc.want {
				t.Errorf("Convert(%q)\n got %q\nwant %q", tc.in, got.Markdown, tc.want)
			}
		})
	}
}

func TestUnsupportedSortedAndCounted(t *testing.T) {
	t.Parallel()
	in := strings.Repeat(`<ac:structured-macro ac:name="zeta"/>`, 3) +
		`<ac:structured-macro ac:name="alpha"/>`
	got, err := Convert(in)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	want := []Unsupported{{Name: "alpha", Count: 1}, {Name: "zeta", Count: 3}}
	if !reflect.DeepEqual(got.Unsupported, want) {
		t.Errorf("Unsupported = %+v, want %+v", got.Unsupported, want)
	}
	if n := strings.Count(got.Markdown, MacroMarker("zeta")); n != 3 {
		t.Errorf("zeta marker appears %d times, want 3", n)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

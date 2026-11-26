package storagemd

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestConvertPage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          string
		props       []Property
		hasProps    bool
		markdown    string
		unsupported []Unsupported
	}{
		{
			name: "vertical table, left out of the body",
			in: `<ac:structured-macro ac:name="details" ac:schema-version="1"><ac:rich-text-body>` +
				`<table><tbody>` +
				`<tr><th><p>Version</p></th><td><p>1.2</p></td></tr>` +
				`<tr><th><p>Approved by</p></th><td><p>Ada  Lovelace</p></td></tr>` +
				`<tr><th><p>Approval date</p></th><td><p><time datetime="2026-03-01" /></p></td></tr>` +
				`<tr><th><p>Acknowledgment audience</p></th><td><ul><li>all_staff</li><li>engineering</li></ul></td></tr>` +
				`<tr><th><p>Related</p></th><td><p><ac:link><ri:page ri:content-title="Other policy" /></ac:link></p></td></tr>` +
				`<tr><th><p>Approver</p></th><td><p><ac:link><ri:user ri:account-id="abc" /></ac:link></p></td></tr>` +
				`<tr><th><p>Notes</p></th><td><p>one<br/>two</p></td></tr>` +
				`<tr><td><p>Owner role</p></td></tr>` +
				`<tr></tr>` +
				`<tr><th><p> </p></th><td><p>ignored: no key</p></td></tr>` +
				`</tbody></table></ac:rich-text-body></ac:structured-macro>` +
				`<h1>Purpose</h1><p>Protect data.</p>`,
			props: []Property{
				{Key: "Version", Value: "1.2"},
				{Key: "Approved by", Value: "Ada Lovelace"},
				{Key: "Approval date", Value: "2026-03-01"},
				{Key: "Acknowledgment audience", Value: "all_staff\nengineering"},
				{Key: "Related", Value: "Other policy"},
				{Key: "Approver", Value: ""},
				{Key: "Notes", Value: "one\ntwo"},
				{Key: "Owner role", Value: ""},
			},
			hasProps: true,
			markdown: "# Purpose\n\nProtect data.\n",
		},
		{
			name: "horizontal table",
			in: `<p>Intro</p><ac:structured-macro ac:name="details"><ac:parameter ac:name="id">p</ac:parameter><ac:rich-text-body>` +
				`<table><tbody><tr><th>Version</th><th>Effective date</th><th>Extra</th></tr>` +
				`<tr><td>2.0</td><td><ac:link><ac:link-body>2026-01-01</ac:link-body></ac:link></td></tr></tbody></table>` +
				`<table><tbody><tr><th>Second</th><td>table</td></tr></tbody></table>` +
				`</ac:rich-text-body></ac:structured-macro>`,
			props: []Property{
				{Key: "Version", Value: "2.0"},
				{Key: "Effective date", Value: "2026-01-01"},
				{Key: "Extra", Value: ""},
			},
			hasProps: true,
			markdown: "Intro\n",
		},
		{
			name: "nested inside a layout; a second macro is unsupported",
			in: `<ac:layout><ac:layout-section><ac:layout-cell>` +
				`<ac:structured-macro ac:name="details"><ac:rich-text-body><table><tr><th>Version</th><td>1</td></tr></table></ac:rich-text-body></ac:structured-macro>` +
				`<p>Body</p><ac:structured-macro ac:name="details"><ac:rich-text-body><table><tr><th>Version</th><td>9</td></tr></table></ac:rich-text-body></ac:structured-macro>` +
				`</ac:layout-cell></ac:layout-section></ac:layout>`,
			props:       []Property{{Key: "Version", Value: "1"}},
			hasProps:    true,
			markdown:    "Body\n\n" + MacroMarker("details") + "\n",
			unsupported: []Unsupported{{Name: "details", Count: 1}},
		},
		{
			name:     "no properties macro",
			in:       `<p>Just text</p><ac:structured-macro ac:name="toc" />`,
			markdown: "Just text\n\n" + MacroMarker("toc") + "\n",
			unsupported: []Unsupported{
				{Name: "toc", Count: 1},
			},
		},
		{
			name:     "macro with no table",
			in:       `<ac:structured-macro ac:name="details"><ac:rich-text-body><p>nothing</p></ac:rich-text-body></ac:structured-macro>`,
			hasProps: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConvertPage(tc.in)
			if err != nil {
				t.Fatalf("ConvertPage: %v", err)
			}
			if got.HasProperties != tc.hasProps {
				t.Errorf("HasProperties = %v, want %v", got.HasProperties, tc.hasProps)
			}
			if !reflect.DeepEqual(got.Properties, tc.props) {
				t.Errorf("Properties =\n%#v\nwant\n%#v", got.Properties, tc.props)
			}
			if got.Markdown != tc.markdown {
				t.Errorf("Markdown = %q, want %q", got.Markdown, tc.markdown)
			}
			if !reflect.DeepEqual(got.Unsupported, tc.unsupported) {
				t.Errorf("Unsupported = %#v, want %#v", got.Unsupported, tc.unsupported)
			}
		})
	}
}

// ConvertPage's body matches Convert's for a page without properties, so
// the two never disagree on the hash the platform keeps.
func TestConvertPageMatchesConvert(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"basic", "lists", "table", "blocks", "macros", "unsupported"} {
		in := readFixture(t, name+".xhtml")
		a, err := Convert(in)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ConvertPage(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b.Result) {
			t.Errorf("%s: ConvertPage result differs from Convert", name)
		}
	}
}

func TestConvertPageErrors(t *testing.T) {
	t.Parallel()
	if _, err := ConvertPage(strings.Repeat("<div>", MaxDepth+1)); !errors.Is(err, ErrTooDeep) {
		t.Errorf("deep body: err = %v, want ErrTooDeep", err)
	}
	if _, err := ConvertPage("<p>a</q>"); err == nil {
		t.Error("malformed body: want an error")
	}
}

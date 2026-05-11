package scope

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Canonicalize returns a key-sorted JSON encoding of dims plus its sha256 hex
// digest. The DB UNIQUE constraint on scope_cells(tenant_id, dimensions_hash)
// relies on this being deterministic across map-iteration order.
//
// Format: {"k1":"v1","k2":"v2",...} with keys ASCII-sorted. Strings are
// JSON-escaped using strconv-style rules but only for the characters the
// dimension surface uses (we control it via scope_dimensions). The encoder
// rejects empty maps because a scope cell with zero dimensions is meaningless.
func Canonicalize(dims map[string]string) (canonical []byte, hashHex string, err error) {
	if len(dims) == 0 {
		return nil, "", errors.New("scope: dimensions map must be non-empty")
	}
	keys := make([]string, 0, len(dims))
	for k := range dims {
		if k == "" {
			return nil, "", errors.New("scope: dimension key must be non-empty")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(&b, k)
		b.WriteByte(':')
		writeJSONString(&b, dims[k])
	}
	b.WriteByte('}')

	out := []byte(b.String())
	sum := sha256.Sum256(out)
	return out, hex.EncodeToString(sum[:]), nil
}

// writeJSONString writes s as a JSON string literal: surrounding quotes, with
// standard escapes for the control characters and reverse-solidus/quote.
// Equivalent to json.Marshal of a string but allocation-free.
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

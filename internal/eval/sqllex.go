// sqllex.go — a Postgres lexer for the SQL evidence-query static checks.
//
// The static guard and the stock-bundle lints read a query's tokens, not its
// text, so a dotted word inside a string literal ('injection.sql') or a
// comment is not mistaken for a schema-qualified name, and whitespace or a
// comment between a schema and its table (`public /**/ . users`) does not hide
// one (#2973).
//
// The lexer follows Postgres's scanner (src/backend/parser/scan.l) for the
// tokens that decide where a literal ends:
//
//   - '...' is a standard string: ” is a quote and a backslash is an
//     ordinary character. That is the standard_conforming_strings = on
//     reading, and evalSQLQuery sets it for the query so the server reads
//     the literal the same way (a server configured off would end
//     'a\' public.t --' later than this lexer does).
//   - E'...' (an e or E that starts a token) also takes a backslash escape.
//   - $tag$...$tag$ is a dollar-quoted string. A $ inside an identifier does
//     not start one, and $1 is a parameter.
//   - "..." is a quoted identifier, with "" as a quote.
//   - -- runs to the end of the line; /* */ nests.
//
// Anything unterminated is an error, so a query this lexer cannot read is
// refused rather than checked on a guess.
package eval

import (
	"errors"
	"fmt"
	"strings"
)

type sqlTokenKind int

const (
	tokIdent       sqlTokenKind = iota // unquoted identifier or keyword; val is lowercased
	tokQuotedIdent                     // "..."; val is the identifier
	tokString                          // any string literal; val is its content
	tokNumber
	tokParam // $1
	tokOp    // a run of operator characters, e.g. ->>, ~, #>>
	tokPunct // ( ) [ ] , ; . :: :
)

type sqlToken struct {
	kind sqlTokenKind
	val  string
}

// errSQLLex marks a query the lexer cannot read to the end.
var errSQLLex = errors.New("unreadable SQL")

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || c == '$' || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isOpChar(c byte) bool { return strings.IndexByte("+-*/<>=~!@#%^&|`?", c) >= 0 }

// lexSQL splits a query into tokens, dropping whitespace and comments.
func lexSQL(s string) ([]sqlToken, error) {
	var toks []sqlToken
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < n && s[i+1] == '-':
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && s[i+1] == '*':
			end, err := skipBlockComment(s, i)
			if err != nil {
				return nil, err
			}
			i = end
		case c == '\'':
			val, end, err := lexQuoted(s, i, '\'', false)
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlToken{tokString, val})
			i = end
		case c == '"':
			val, end, err := lexQuoted(s, i, '"', false)
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlToken{tokQuotedIdent, val})
			i = end
		case c == '$' && i+1 < n && isDigit(s[i+1]):
			j := i + 1
			for j < n && isDigit(s[j]) {
				j++
			}
			toks = append(toks, sqlToken{tokParam, s[i:j]})
			i = j
		case c == '$':
			val, end, err := lexDollar(s, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlToken{tokString, val})
			i = end
		case isIdentStart(c):
			// E'...' takes backslash escapes; U&'...' and U&"..." are the
			// Unicode-escape forms, which end like their plain forms.
			if (c == 'e' || c == 'E') && i+1 < n && s[i+1] == '\'' {
				val, end, err := lexQuoted(s, i+1, '\'', true)
				if err != nil {
					return nil, err
				}
				toks = append(toks, sqlToken{tokString, val})
				i = end
				continue
			}
			if (c == 'u' || c == 'U') && i+2 < n && s[i+1] == '&' && (s[i+2] == '\'' || s[i+2] == '"') {
				q := s[i+2]
				val, end, err := lexQuoted(s, i+2, q, false)
				if err != nil {
					return nil, err
				}
				kind := tokString
				if q == '"' {
					kind = tokQuotedIdent
				}
				toks = append(toks, sqlToken{kind, val})
				i = end
				continue
			}
			j := i + 1
			for j < n && isIdentCont(s[j]) {
				j++
			}
			toks = append(toks, sqlToken{tokIdent, strings.ToLower(s[i:j])})
			i = j
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(s[i+1])):
			j := i
			for j < n && (isDigit(s[j]) || s[j] == '_') {
				j++
			}
			if j < n && s[j] == '.' && !(j+1 < n && s[j+1] == '.') {
				j++
				for j < n && (isDigit(s[j]) || s[j] == '_') {
					j++
				}
			}
			if j < n && (s[j] == 'e' || s[j] == 'E') {
				k := j + 1
				if k < n && (s[k] == '+' || s[k] == '-') {
					k++
				}
				if k < n && isDigit(s[k]) {
					j = k
					for j < n && isDigit(s[j]) {
						j++
					}
				}
			}
			toks = append(toks, sqlToken{tokNumber, s[i:j]})
			i = j
		case c == ':' && i+1 < n && s[i+1] == ':':
			toks = append(toks, sqlToken{tokPunct, "::"})
			i += 2
		case strings.IndexByte("()[],;.:", c) >= 0:
			toks = append(toks, sqlToken{tokPunct, string(c)})
			i++
		case isOpChar(c):
			// An operator ends where a comment starts, as in Postgres.
			j := i
			for j < n && isOpChar(s[j]) {
				if (s[j] == '-' && j+1 < n && s[j+1] == '-') || (s[j] == '/' && j+1 < n && s[j+1] == '*') {
					break
				}
				j++
			}
			toks = append(toks, sqlToken{tokOp, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("%w: unexpected character %q at offset %d", errSQLLex, c, i)
		}
	}
	return toks, nil
}

// lexQuoted reads a literal opened by q at s[start]. A doubled q is one q;
// with backslash set, a backslash escapes the next character.
func lexQuoted(s string, start int, q byte, backslash bool) (string, int, error) {
	var b strings.Builder
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if backslash && c == '\\' {
			if i+1 >= len(s) {
				break
			}
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if c == q {
			if i+1 < len(s) && s[i+1] == q {
				b.WriteByte(q)
				i++
				continue
			}
			return b.String(), i + 1, nil
		}
		b.WriteByte(c)
	}
	return "", 0, fmt.Errorf("%w: unterminated %c literal at offset %d", errSQLLex, q, start)
}

// lexDollar reads $tag$...$tag$ starting at s[start]. The tag is empty or an
// identifier without a $.
func lexDollar(s string, start int) (string, int, error) {
	j := start + 1
	if j < len(s) && isIdentStart(s[j]) {
		j++
		for j < len(s) && isIdentCont(s[j]) && s[j] != '$' {
			j++
		}
	}
	if j >= len(s) || s[j] != '$' {
		return "", 0, fmt.Errorf("%w: stray $ at offset %d", errSQLLex, start)
	}
	delim := s[start : j+1]
	body := j + 1
	end := strings.Index(s[body:], delim)
	if end < 0 {
		return "", 0, fmt.Errorf("%w: unterminated %s string at offset %d", errSQLLex, delim, start)
	}
	return s[body : body+end], body + end + len(delim), nil
}

// skipBlockComment returns the offset just past the /* */ comment opened at
// s[start], counting nested comments as Postgres does.
func skipBlockComment(s string, start int) (int, error) {
	depth := 0
	for i := start; i+1 < len(s); i++ {
		switch {
		case s[i] == '/' && s[i+1] == '*':
			depth++
			i++
		case s[i] == '*' && s[i+1] == '/':
			depth--
			i++
			if depth == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: unterminated /* comment at offset %d", errSQLLex, start)
}

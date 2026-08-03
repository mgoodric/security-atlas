package qmapsuggest

import (
	"sort"
	"strings"
	"unicode"
)

const (
	minKeywordLen  = 3
	maxExcerptRune = 420
)

var stopwords = map[string]bool{
	"the": true, "and": true, "are": true, "you": true, "your": true,
	"for": true, "with": true, "that": true, "this": true, "from": true,
	"have": true, "has": true, "does": true, "did": true, "will": true,
	"can": true, "any": true, "all": true, "how": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "whom": true,
	"please": true, "describe": true, "provide": true, "list": true,
	"explain": true, "company": true, "organization": true, "organisation": true,
	"policy": true, "process": true, "procedures": true,
}

func keywordsFrom(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < minKeywordLen || stopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func rankCandidates(cands []Candidate, keywords []string, limit int) []Candidate {
	type scored struct {
		c     Candidate
		score int
	}
	ranked := make([]scored, 0, len(cands))
	for _, c := range cands {
		hay := strings.ToLower(c.SCFID + " " + c.Title + " " + c.Excerpt)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(hay, kw) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{c: c, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].c.SCFID < ranked[j].c.SCFID
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]Candidate, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.c)
	}
	return out
}

func boundExcerpt(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	cut := maxRunes
	for cut > 0 && !unicode.IsSpace(r[cut]) {
		cut--
	}
	if cut == 0 {
		cut = maxRunes
	}
	return strings.TrimSpace(string(r[:cut])) + "..."
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

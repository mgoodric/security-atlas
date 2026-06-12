package qaisuggest

import (
	"sort"
	"strings"
	"unicode"
)

// keywordsFrom extracts the distinct, lowercased, de-stopworded keyword tokens
// from a question's text. This is the v0 keyword first-pass (P0-441-5 — NO
// pgvector): retrieval matches candidate material whose title/body contains
// these tokens. Tokens shorter than minKeywordLen and common stopwords are
// dropped so the ILIKE candidate query is not dominated by "the"/"a"/"is".
//
// Pure function — the JUDGMENT-call surface for the retrieval strategy
// (decisions log). Deliberately simple: split on non-alphanumeric, lowercase,
// drop stopwords + short tokens, dedupe preserving order. No stemming, no
// synonyms — those are the retrieval-quality follow-on (pgvector), not v0.
func keywordsFrom(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < minKeywordLen {
			continue
		}
		if stopwords[f] {
			continue
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// minKeywordLen drops 1-2 char tokens ("a", "is", "of" survive stopwords; this
// catches stray short tokens like "x" or "v2"'s "v"). 3 keeps "mfa", "sso",
// "rbac", "iam" — the high-signal security acronyms.
const minKeywordLen = 3

// stopwords is a small, security-questionnaire-tuned stoplist. Intentionally
// short: the keyword query is already capped + tenant-scoped, so over-pruning
// risks dropping a real match for marginal noise reduction. The bias is toward
// recall (catch a candidate) over precision (the human reviews every draft
// anyway, and a too-broad candidate set is bounded by maxCandidates).
var stopwords = map[string]bool{
	"the": true, "and": true, "are": true, "you": true, "your": true,
	"for": true, "with": true, "that": true, "this": true, "from": true,
	"have": true, "has": true, "does": true, "did": true, "will": true,
	"can": true, "any": true, "all": true, "how": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "whom": true,
	"please": true, "describe": true, "provide": true, "list": true,
	"explain": true, "company": true, "organization": true, "organisation": true,
}

// rankCandidates scores each candidate by how many distinct question keywords
// appear in its title or excerpt, and returns the top `limit` (highest score
// first, ties broken by id for determinism). A candidate scoring zero is
// dropped — a candidate the keyword pass surfaced but that matches no token is
// noise. Pure function over already-retrieved rows: the SQL ILIKE pass casts a
// wide net (any token matches), this in-memory rank tightens it + bounds it.
//
// This is the JUDGMENT call on retrieval scoring (decisions log): a simple
// keyword-overlap count, NOT TF-IDF or BM25 — the v0 bar is "surface the
// obviously-relevant policy/evidence", and the operator reviews every draft.
func rankCandidates(cands []Candidate, keywords []string, limit int) []Candidate {
	type scored struct {
		c     Candidate
		score int
	}
	ranked := make([]scored, 0, len(cands))
	for _, c := range cands {
		hay := strings.ToLower(c.Title + " " + c.Excerpt)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(hay, kw) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		ranked = append(ranked, scored{c: c, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].c.ID < ranked[j].c.ID
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]Candidate, 0, len(ranked))
	for _, s := range ranked {
		out = append(out, s.c)
	}
	return out
}

// boundExcerpt trims a candidate's source text to a bounded prefix so the
// prompt carries excerpts, not the full corpus (AC-2, D-mitigation). Cuts on a
// word boundary near the cap to avoid a mid-word slice.
func boundExcerpt(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	cut := maxRunes
	// Walk back to the last space within the cap so we don't split a word.
	for cut > 0 && !unicode.IsSpace(r[cut]) {
		cut--
	}
	if cut == 0 {
		cut = maxRunes
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

// maxExcerptRunes bounds one candidate excerpt fed to the model. A policy body
// can be long; the model only needs enough to phrase + cite, not the whole
// document.
const maxExcerptRunes = 600

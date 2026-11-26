// #2653 — an answer's citations, checked server-side.
//
// A citation names one control or one evidence record in the caller's
// tenant. The client sends {id, type, title}; the server keeps the id and
// type, refuses an id its tenant does not hold, and writes the title from
// its own record. ParseCitations is the shape check (no DB); the existence
// check and the titles run inside UpsertAnswer's RLS transaction, so a
// foreign tenant's id reads as absent.

package questionnaire

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Citation types, the plural resource names the web picker writes.
const (
	CitationControls = "controls"
	CitationEvidence = "evidence"
)

// MaxCitations bounds one answer's citation list.
const MaxCitations = 100

// ErrInvalidCitation is the one refusal for a citation the server will not
// store: a malformed entry, an id the caller's tenant does not hold, or one
// the caller may not read. The HTTP layer answers all of them with the
// same 400, so the refusal does not tell an id that exists elsewhere from
// one that exists nowhere.
var ErrInvalidCitation = errors.New("questionnaire: invalid citation")

// Citation is one cited control or evidence record.
type Citation struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// ParseCitations checks the shape of a client's citation list: each entry
// an object with a UUID id and a type of controls or evidence. The id is
// returned in canonical lower-case form and the client's title is dropped
// (UpsertAnswer writes the server's). A nil or empty list is valid.
func ParseCitations(raw []any) ([]Citation, error) {
	if len(raw) > MaxCitations {
		return nil, fmt.Errorf("%w: more than %d citations", ErrInvalidCitation, MaxCitations)
	}
	out := make([]Citation, 0, len(raw))
	for i, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: entry %d is not an object", ErrInvalidCitation, i)
		}
		typ, _ := obj["type"].(string)
		if typ != CitationControls && typ != CitationEvidence {
			return nil, fmt.Errorf("%w: entry %d type", ErrInvalidCitation, i)
		}
		idStr, _ := obj["id"].(string)
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("%w: entry %d id", ErrInvalidCitation, i)
		}
		out = append(out, Citation{ID: id.String(), Type: typ})
	}
	return out, nil
}

// evidenceCitationTitle is an evidence record's citation title: its kind
// and the UTC day it was observed, the label the web picker shows for a
// listed record (web/lib/entity-picker.ts evidenceOptions).
func evidenceCitationTitle(kind string, observedAt time.Time) string {
	if kind == "" {
		kind = "Evidence"
	}
	return kind + " · " + observedAt.UTC().Format("2006-01-02")
}

// resolveCitations fills each citation's title from the tenant's own
// record, under the RLS context already applied to tx. Any id the tenant
// does not hold is ErrInvalidCitation.
func resolveCitations(ctx context.Context, tx pgx.Tx, cites []Citation) error {
	if len(cites) == 0 {
		return nil
	}
	ids := map[string][]string{}
	for _, c := range cites {
		ids[c.Type] = append(ids[c.Type], c.ID)
	}
	titles := map[string]string{} // type + "/" + id -> title
	if len(ids[CitationControls]) > 0 {
		rows, err := tx.Query(ctx,
			`SELECT id::text, title FROM controls WHERE id = ANY($1::uuid[])`,
			ids[CitationControls])
		if err != nil {
			return fmt.Errorf("questionnaire: resolve control citations: %w", err)
		}
		for rows.Next() {
			var id, title string
			if err := rows.Scan(&id, &title); err != nil {
				rows.Close()
				return fmt.Errorf("questionnaire: scan control citation: %w", err)
			}
			titles[CitationControls+"/"+id] = title
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("questionnaire: resolve control citations: %w", err)
		}
	}
	if len(ids[CitationEvidence]) > 0 {
		rows, err := tx.Query(ctx,
			`SELECT id::text, COALESCE(evidence_kind, ''), observed_at FROM evidence_records WHERE id = ANY($1::uuid[])`,
			ids[CitationEvidence])
		if err != nil {
			return fmt.Errorf("questionnaire: resolve evidence citations: %w", err)
		}
		for rows.Next() {
			var id, kind string
			var observedAt time.Time
			if err := rows.Scan(&id, &kind, &observedAt); err != nil {
				rows.Close()
				return fmt.Errorf("questionnaire: scan evidence citation: %w", err)
			}
			titles[CitationEvidence+"/"+id] = evidenceCitationTitle(kind, observedAt)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("questionnaire: resolve evidence citations: %w", err)
		}
	}
	for i, c := range cites {
		title, ok := titles[c.Type+"/"+c.ID]
		if !ok {
			return fmt.Errorf("%w: %s %s not found", ErrInvalidCitation, c.Type, c.ID)
		}
		cites[i].Title = title
	}
	return nil
}

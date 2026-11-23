package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #3005: an error that comes from what is stored is ErrUnevaluable and keeps
// its language sentinel and its message; a runtime or transient error is not.
func TestUnevaluable_Classification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now()

	_, regoCompile := evalRegoQuery(ctx, "package evidence.query\nresult := ", nil, now)
	_, regoEmpty := evalRegoQuery(ctx, "", nil, now)
	_, regoNoResult := evalRegoQuery(ctx, "package evidence.query\nother := 1", nil, now)
	_, jsonCompile := evalJSONPathQuery(ctx, "$[", nil)
	_, sqlGuard := evalSQLQuery(ctx, nil, "DELETE FROM evidence", nil, now)
	_, parse := parseEvidenceQueries([]byte(`{not json`))

	cases := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"rego compile", regoCompile, ErrRegoQuery},
		{"rego empty", regoEmpty, ErrRegoQuery},
		{"rego no result", regoNoResult, ErrRegoQuery},
		{"jsonpath compile", jsonCompile, ErrJSONPathQuery},
		{"sql guard", sqlGuard, ErrSQLQuery},
		{"evidence_queries parse", parse, nil},
	}
	for _, c := range cases {
		if c.err == nil {
			t.Fatalf("%s: no error", c.name)
		}
		if !errors.Is(c.err, ErrUnevaluable) {
			t.Errorf("%s: %v is not ErrUnevaluable", c.name, c.err)
		}
		if c.sentinel != nil && !errors.Is(c.err, c.sentinel) {
			t.Errorf("%s: %v lost its sentinel %v", c.name, c.err, c.sentinel)
		}
		if strings.Contains(c.err.Error(), ErrUnevaluable.Error()) {
			t.Errorf("%s: message changed: %q", c.name, c.err.Error())
		}
	}

	transient := []error{
		fmt.Errorf("%w: eval: %v", ErrRegoQuery, errors.New("conn reset")),
		fmt.Errorf("%w: statement timeout after %s", ErrSQLQuery, sqlStatementTimeout),
		errors.New("resolve applicable cells: list cells (in tx): conn reset"),
	}
	for _, err := range transient {
		if errors.Is(err, ErrUnevaluable) {
			t.Errorf("transient %v classified unevaluable", err)
		}
	}
}

// #3005: a deterministic evaluation failure terms the message (no
// redelivery loop under MaxDeliver -1) and counts it.
func TestHandle_UnevaluableTerms(t *testing.T) {
	t.Parallel()
	_, cause := evalRegoQuery(context.Background(), "package evidence.query\nresult := ", nil, time.Now())
	ev := &fakeEvaluator{err: fmt.Errorf("control %s rego query: %w", uuid.New(), cause)}
	s := newSubscriberWithEvaluator(ev)
	msg := newFakeMsg(validProtoRecord(t, uuid.NewString()), validTenantHeader())
	before := UnevaluableControls()

	s.handle(context.Background(), msg)

	if msg.terms != 1 || msg.naks != 0 || msg.acks != 0 {
		t.Fatalf("unevaluable: terms/naks/acks = %d/%d/%d, want 1/0/0", msg.terms, msg.naks, msg.acks)
	}
	if got := UnevaluableControls() - before; got < 1 {
		t.Fatalf("unevaluable count delta = %d, want >= 1", got)
	}
}

package risk

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
)

// stubEngine returns a fixed error per effectivenessEngine method.
type stubEngine struct {
	evalErr, effErr, stateErr error
}

func (s stubEngine) Effectiveness(context.Context, uuid.UUID) (eval.Effectiveness, error) {
	return eval.Effectiveness{TotalCount: 2, PassCount: 1, PassRate: 0.5}, s.effErr
}

func (s stubEngine) ControlState(context.Context, uuid.UUID, string, time.Time) ([]eval.State, error) {
	return []eval.State{{Result: eval.ResultPass}}, s.stateErr
}

func (s stubEngine) EvaluateControl(context.Context, uuid.UUID, string, time.Time) (int, error) {
	return 1, s.evalErr
}

// #3005: how controlBreakdown classifies each callee's errors.
func TestControlBreakdown_ErrorClassification(t *testing.T) {
	t.Parallel()
	unevaluable := fmt.Errorf("control: %w", eval.ErrUnevaluable)
	transient := errors.New("conn reset")

	cases := []struct {
		name            string
		engine          stubEngine
		wantErr         bool
		wantUnderivable bool
	}{
		{name: "recompute unevaluable reads the rows it has", engine: stubEngine{evalErr: unevaluable}},
		{name: "recompute transient stays transient", engine: stubEngine{evalErr: transient}, wantErr: true},
		{name: "effectiveness unevaluable is underivable", engine: stubEngine{effErr: unevaluable}, wantErr: true, wantUnderivable: true},
		{name: "coverage unevaluable is underivable", engine: stubEngine{stateErr: unevaluable}, wantErr: true, wantUnderivable: true},
		{name: "effectiveness transient stays transient", engine: stubEngine{effErr: transient}, wantErr: true},
	}
	for _, c := range cases {
		d := NewResidualDeriver(nil, c.engine)
		bd, err := d.controlBreakdown(context.Background(), uuid.New(), dbx.ListRiskControlLinkWeightsRow{}, true)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err = %v, want error %v", c.name, err, c.wantErr)
		}
		if got := errors.Is(err, ErrUnderivable); got != c.wantUnderivable {
			t.Fatalf("%s: errors.Is(ErrUnderivable) = %v, want %v (err %v)", c.name, got, c.wantUnderivable, err)
		}
		if !c.wantErr && (bd.OperationalScore != 0.5 || bd.CoverageScore != 1) {
			t.Fatalf("%s: breakdown = %+v, want the stored evaluations read", c.name, bd)
		}
	}
}

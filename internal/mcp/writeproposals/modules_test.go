package writeproposals

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type switchesOff map[string]bool

func (s switchesOff) SwitchedOn(_ context.Context, _ uuid.UUID, key string) (bool, error) {
	return !s[key], nil
}

// #2175: a pending proposal into a switched-off module is refused; core
// tools, settled proposals and a store with no switches are not.
func TestModuleOff(t *testing.T) {
	t.Parallel()
	s := NewStore(nil).WithSwitches(switchesOff{"risk": true})
	pending := func(tool string) Proposal {
		return Proposal{TenantID: uuid.New(), ToolName: tool, State: StateAIProposed}
	}
	for _, tool := range []string{ToolCreateRisk, ToolUpdateRiskTreatment, ToolLinkChildRisks, ToolUnlinkChildRisk} {
		if err := s.moduleOff(context.Background(), pending(tool)); !errors.Is(err, ErrModuleOff) {
			t.Errorf("%s: err = %v; want ErrModuleOff", tool, err)
		}
	}
	for _, tool := range []string{ToolUpdateControlState, ToolPushEvidence} {
		if err := s.moduleOff(context.Background(), pending(tool)); err != nil {
			t.Errorf("%s (core): err = %v", tool, err)
		}
	}
	applied := pending(ToolCreateRisk)
	applied.State = StateApplied
	if err := s.moduleOff(context.Background(), applied); err != nil {
		t.Errorf("applied proposal: err = %v; want nil (Confirm reports the state)", err)
	}
	if err := NewStore(nil).moduleOff(context.Background(), pending(ToolCreateRisk)); err != nil {
		t.Errorf("no switches: err = %v", err)
	}
}

// Every write tool is either mapped to a module or deliberately core.
func TestToolModule_CoversAllowedTools(t *testing.T) {
	t.Parallel()
	core := map[string]bool{ToolUpdateControlState: true, ToolPushEvidence: true}
	for tool := range AllowedTools {
		if _, ok := toolModule[tool]; !ok && !core[tool] {
			t.Errorf("tool %q has no module and is not listed as core", tool)
		}
	}
}

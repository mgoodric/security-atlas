package writeproposals

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// #2175: a proposal aimed at a switched-off module stays pending. Confirm
// refuses to apply it while the module is off, without touching the row,
// and applies it as usual once the module is switched back on.

// ErrModuleOff is returned by Confirm for a proposal whose tool writes into
// a switched-off module. The proposal stays ai_proposed. HTTP 409.
var ErrModuleOff = errors.New("module switched off")

// toolModule maps each write tool to the registry key (featureflag.Modules)
// of the optional module it writes into: the module POST /v1/risks and its
// sub-routes belong to. A tool absent from it writes into a core module
// (update_control_state: Controls; push_evidence: Evidence).
var toolModule = map[string]string{
	ToolCreateRisk:          "risk",
	ToolUpdateRiskTreatment: "risk",
	ToolLinkChildRisks:      "risk",
	ToolUnlinkChildRisk:     "risk",
}

// Switches answers whether a module is switched on for a tenant.
// moduleaccess.Views satisfies it.
type Switches interface {
	SwitchedOn(ctx context.Context, tenantID uuid.UUID, key string) (bool, error)
}

// WithSwitches makes Confirm refuse a proposal whose module is switched
// off. nil (the default) applies every proposal.
func (s *Store) WithSwitches(sw Switches) *Store {
	s.switches = sw
	return s
}

// moduleOff returns ErrModuleOff when p's tool writes into a module that is
// switched off for p's tenant. It runs before Confirm's transaction, so the
// switch read never waits on the locked row.
func (s *Store) moduleOff(ctx context.Context, p Proposal) error {
	key, ok := toolModule[p.ToolName]
	if !ok || s.switches == nil || p.State != StateAIProposed {
		return nil
	}
	on, err := s.switches.SwitchedOn(ctx, p.TenantID, key)
	if err != nil {
		return fmt.Errorf("module switch: %w", err)
	}
	if !on {
		return fmt.Errorf("%w: %s", ErrModuleOff, key)
	}
	return nil
}

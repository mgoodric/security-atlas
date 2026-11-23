package oktaapps

import "fmt"

// #2951: an app sign-in rule whose verificationMethod.type is
// AUTH_METHOD_CHAIN (Okta OpenAPI AuthenticationMethodChainMethod) lists
// chains. A sign-in satisfies one chain, and a chain is a sequence of steps:
// the step's authenticationMethods are alternatives (any one satisfies the
// step) and next holds the following step (AuthenticationMethodChain.next,
// at most one item). So a sign-in path is one method per step of one chain.

// Factor classes.
const (
	classKnowledge  = "knowledge"
	classPossession = "possession"
	// classUserVerification is the PIN or biometric a possession method
	// demands when its userVerification is REQUIRED. It is not
	// possession, so with one it counts as a second factor, which is how
	// Okta's own amc-two-chain example offers Okta Verify alone as an
	// alternative to password then SMS.
	classUserVerification = "user_verification"
)

// keyClass is the factor class of each authenticator key
// (AuthenticatorKeyEnum) whose class does not depend on how the org set
// it up. The rest (duo, external_idp, smart_card_idp, tac) leave a chain
// undecided: an external IdP or Duo may run its own MFA, a smart card
// carries a PIN, and a temporary access code is neither.
var keyClass = map[string]string{
	"okta_password":     classKnowledge,
	"security_question": classKnowledge,
	"okta_email":        classPossession,
	"okta_verify":       classPossession,
	"phone_number":      classPossession,
	"google_otp":        classPossession,
	"webauthn":          classPossession,
	"security_key":      classPossession,
	"yubikey_token":     classPossession,
	"symantec_vip":      classPossession,
	"onprem_mfa":        classPossession,
	"custom_app":        classPossession,
}

// maxChainSteps is the documented limit on a chain's steps.
const maxChainSteps = 3

type rawChain struct {
	AuthenticationMethods []rawAuthMethod `json:"authenticationMethods"`
	Next                  []rawChain      `json:"next"`
}

type rawAuthMethod struct {
	Key              string `json:"key"`
	Method           string `json:"method"`
	UserVerification string `json:"userVerification"`
}

// chainsMFA decides whether every sign-in path through a rule's chains
// presents factors of two or more classes. It returns (true, "") when
// every path does, (false, "") when one path presents a single class, and
// (false, reason) when a chain cannot be read. A single-class path decides
// false even when another chain is unreadable, as a 1FA rule does.
func chainsMFA(ruleID string, chains []rawChain) (mfa bool, reason string) {
	if len(chains) == 0 {
		return false, fmt.Sprintf("rule %s has an AUTH_METHOD_CHAIN with no chains", ruleID)
	}
	for i, c := range chains {
		steps, why := chainSteps(c)
		if why != "" {
			reason = fmt.Sprintf("rule %s chain %d %s", ruleID, i, why)
			continue
		}
		if singleClassPath(steps) {
			return false, ""
		}
	}
	return reason == "", reason
}

// chainSteps flattens a chain into the factor classes each step's
// alternatives carry.
func chainSteps(c rawChain) ([][]map[string]bool, string) {
	var steps [][]map[string]bool
	for cur := &c; cur != nil; {
		if len(steps) == maxChainSteps {
			return nil, fmt.Sprintf("has more than %d steps", maxChainSteps)
		}
		if len(cur.AuthenticationMethods) == 0 {
			return nil, "has a step with no authentication method"
		}
		var step []map[string]bool
		for _, m := range cur.AuthenticationMethods {
			class, ok := keyClass[m.Key]
			if !ok {
				return nil, fmt.Sprintf("uses authenticator %q, whose factor class is not fixed", m.Key)
			}
			classes := map[string]bool{class: true}
			if class == classPossession && m.UserVerification == "REQUIRED" {
				classes[classUserVerification] = true
			}
			step = append(step, classes)
		}
		steps = append(steps, step)
		switch len(cur.Next) {
		case 0:
			cur = nil
		case 1:
			cur = &cur.Next[0]
		default:
			return nil, fmt.Sprintf("has %d next steps where Okta documents one", len(cur.Next))
		}
	}
	return steps, ""
}

// singleClassPath reports whether some choice of one method per step
// presents only one factor class. Steps are at most three and each holds
// a handful of methods, so every path is tried.
func singleClassPath(steps [][]map[string]bool) bool {
	var walk func(i int, seen map[string]bool) bool
	walk = func(i int, seen map[string]bool) bool {
		if len(seen) > 1 {
			return false
		}
		if i == len(steps) {
			return true
		}
		for _, classes := range steps[i] {
			next := map[string]bool{}
			for k := range seen {
				next[k] = true
			}
			for k := range classes {
				next[k] = true
			}
			if walk(i+1, next) {
				return true
			}
		}
		return false
	}
	return walk(0, map[string]bool{})
}

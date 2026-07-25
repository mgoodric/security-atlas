package checklist

import (
	"fmt"
	"strings"
)

// systemPrompt is the fixed instruction wrapping every role-section generation.
// The grounding instruction is load-bearing (threat-model T): the model writes
// task statements ONLY for the controls listed, and MUST cite each control's id
// verbatim, which is what makes the citation-validation gate (citations.go)
// meaningful. The tone discipline mirrors the CLAUDE.md board-narrative ban list
// (measured, factual, imperative; no marketing superlatives) — a consistent
// project-wide LLM voice.
//
// The output shape is constrained to one task per line, each line beginning with
// the cited control id in parentheses, so parsing is deterministic. The model is
// told NOT to assert a control is satisfied and NOT to invent coverage — a
// control flagged "no evidence yet" must yield a task to ESTABLISH the evidence,
// never a claim that it exists (the no-fabricated-coverage guardrail spoken to
// the model; the service also enforces it structurally via the no_evidence flag).
const systemPrompt = `You write a role-scoped implementation checklist for one security team. For each control listed below, write 1 to %d concrete, actionable to-do items that the %s team must execute to implement that control. Your checklist is a DRAFT that a human operator reviews before any use; it is NOT authoritative until that operator approves it.

Rules you must follow:
1. Write tasks ONLY for the controls listed below. Do not introduce any control, framework, certification, or claim that is not in the list.
2. Begin every task line with the control id it derives from, in parentheses, copied verbatim (the canonical UUID shown). A task with no control id is not acceptable.
3. Write each task as a single imperative action (start with a verb): what the team does to implement the control. One task per line.
4. Do NOT state that a control is already satisfied, compliant, or covered. Where a control is marked "NO EVIDENCE YET", write a task to ESTABLISH and capture that evidence — never a task that assumes it exists.
5. Be measured and factual. Do not use marketing language, superlatives, or filler.
6. Keep each task to one sentence. Output ONLY the task lines, nothing else.`

// buildSystemPrompt fills the per-section caps + role name into the system
// prompt template.
func buildSystemPrompt(role Role) string {
	return fmt.Sprintf(systemPrompt, MaxTasksPerControl, string(role))
}

// buildPrompt assembles the context block the model sees for one role section:
// the role + the controls assigned to it, each with its id (the citable
// grounding), title, a bounded description, its SCF anchor, linked policy ids,
// and its evidence-backing status. The model is asked to phrase tasks from
// these, never to retrieve or compute.
func buildPrompt(role Role, controls []ControlInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Team role: %s\n\nControls assigned to this team (cite each control id verbatim):\n", role)
	for _, c := range controls {
		fmt.Fprintf(&b, "\n- control id %s: %s\n", c.ID, oneLine(c.Title))
		if d := oneLine(c.Description); d != "" {
			fmt.Fprintf(&b, "    description: %s\n", boundRunes(d, maxDescRunes))
		}
		if c.SCFID != "" {
			fmt.Fprintf(&b, "    scf anchor: %s\n", c.SCFID)
		}
		if len(c.PolicyIDs) > 0 {
			fmt.Fprintf(&b, "    linked policy ids: %s\n", strings.Join(c.PolicyIDs, ", "))
		}
		if c.HasEvidence {
			b.WriteString("    evidence: present\n")
		} else {
			b.WriteString("    evidence: NO EVIDENCE YET\n")
		}
	}
	return b.String()
}

// maxDescRunes bounds a control description in the prompt so one verbose control
// cannot dominate the section's token budget.
const maxDescRunes = 480

// boundRunes truncates s to at most n runes, appending an ellipsis when cut.
func boundRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// oneLine collapses whitespace so a multi-line control description does not
// break the line-oriented prompt block.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// parseTaskLines splits the model's output into candidate task lines: non-empty
// trimmed lines, leading list markers ("-", "*", "1.") stripped. Each line is a
// candidate task whose citations the service validates. Pure.
func parseTaskLines(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		line = stripListMarker(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// stripListMarker removes a single leading "-", "*", or "N." / "N)" list marker
// so a markdown-y model output parses cleanly. Pure.
func stripListMarker(line string) string {
	if line == "" {
		return line
	}
	// Strip a leading bullet rune ("-", "*", or the multibyte "•").
	if strings.HasPrefix(line, "•") {
		return strings.TrimSpace(strings.TrimPrefix(line, "•"))
	}
	switch line[0] {
	case '-', '*':
		return strings.TrimSpace(line[1:])
	}
	// Numbered: leading digits then . or ).
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

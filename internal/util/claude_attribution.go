package util

import (
	"strings"
	"unicode"
)

const claudeCodeAttributionSystemPrefix = "x-anthropic-billing-header:"

// IsClaudeCodeAttributionSystemText reports whether text is the Claude Code
// attribution block that carries per-request billing and prompt fingerprint data.
func IsClaudeCodeAttributionSystemText(text string) bool {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	return strings.HasPrefix(text, claudeCodeAttributionSystemPrefix)
}

// IsAgentIdentitySystemText reports whether text opens with "You are", the
// shape of the agent self-identification that starts many CLI clients' system
// prompts ("You are Claude Code, ...", "You are Codex, ..."). Upstream
// providers set their own model identity, so a second-person product identity
// injected into the request can conflict with it and trigger an
// injection-refusal preamble in the reply. Matching is a plain prefix check
// after trimming leading whitespace, mirroring
// IsClaudeCodeAttributionSystemText.
func IsAgentIdentitySystemText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "You are")
}

// FilterAgentSystemLines removes lines carrying first-party agent data — the
// Claude Code attribution header (IsClaudeCodeAttributionSystemText) and
// agent identity lines opening with "You are" (IsAgentIdentitySystemText) —
// and returns the remaining lines in order, joined by newlines. Filtering is
// line by line so that content following a dropped line survives: clients may
// deliver the system prompt as one merged string, and a leading identity line
// must not take the rest of the prompt down with it.
func FilterAgentSystemLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if IsClaudeCodeAttributionSystemText(line) || IsAgentIdentitySystemText(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

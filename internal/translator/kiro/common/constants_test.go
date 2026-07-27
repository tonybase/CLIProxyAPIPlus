package common

import (
	"strings"
	"testing"
)

func TestWrapSystemPromptForInject_NoIdentityStatement(t *testing.T) {
	prompt := "Always write idiomatic Go."
	wrapped := WrapSystemPromptForInject(prompt)

	if !strings.Contains(wrapped, "<system-reminder>\n"+prompt+"\n</system-reminder>") {
		t.Errorf("expected prompt to be wrapped verbatim, got: %q", wrapped)
	}
}

func TestWrapSystemPromptForInject_Structure(t *testing.T) {
	wrapped := WrapSystemPromptForInject("some instructions")

	if !strings.HasPrefix(wrapped, "<system-reminder>\n") {
		t.Errorf("expected bare system-reminder block with no lead-in, got: %q", wrapped)
	}
	if !strings.HasSuffix(wrapped, "\n</system-reminder>\n\n") {
		t.Errorf("expected closing system-reminder tag, got: %q", wrapped)
	}
}

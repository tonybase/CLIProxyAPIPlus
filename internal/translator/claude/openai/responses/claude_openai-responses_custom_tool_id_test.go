package responses

import (
	"context"
	"strings"
	"testing"
)

// customToolReq mirrors how Codex Desktop declares tools: freeform custom tools
// arrive through an input[].type == additional_tools item rather than the
// top-level "tools" array.
const customToolReq = `{"model":"m","input":[{"type":"additional_tools","role":"developer","tools":[{"type":"custom","name":"exec","description":"run js"},{"type":"function","name":"wait"}]}]}`

const customToolUseEvent = `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_bf78d144-3d38-4f9e-916f-ab4c8c9b2189","name":"exec"}}`

func collectCustomToolEvents(t *testing.T, req []byte, events ...string) string {
	t.Helper()
	var param any
	var all []string
	for _, e := range events {
		for _, o := range ConvertClaudeResponseToOpenAIResponses(context.Background(), "m", req, req, []byte(e), &param) {
			all = append(all, string(o))
		}
	}
	return strings.Join(all, "\n")
}

// Baseline: message_start primes CustomToolNames, so the declared custom tool is
// emitted as custom_tool_call with a ctc_ prefixed id.
func TestCustomToolCallIDWithMessageStart(t *testing.T) {
	got := collectCustomToolEvents(t, []byte(customToolReq),
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`, customToolUseEvent)
	if !strings.Contains(got, `"ctc_call_bf78d144-3d38-4f9e-916f-ab4c8c9b2189"`) {
		t.Errorf("expected ctc_ prefixed id, got:\n%s", got)
	}
	if strings.Contains(got, "fc_call_") {
		t.Errorf("custom tool must not emit an fc_ id:\n%s", got)
	}
}

// Regression: message_start does not always reach the converter, because some
// Kiro paths emit it straight to the downstream channel. CustomToolNames must
// still resolve, otherwise the item downgrades to function_call and carries an
// fc_ id that strict Responses upstreams reject once the client replays it
// under its declared custom_tool_call type.
func TestCustomToolCallIDWithoutMessageStart(t *testing.T) {
	got := collectCustomToolEvents(t, []byte(customToolReq), customToolUseEvent)
	if strings.Contains(got, "fc_call_") {
		t.Errorf("custom tool downgraded to function_call without message_start:\n%s", got)
	}
	if !strings.Contains(got, `"ctc_call_bf78d144-3d38-4f9e-916f-ab4c8c9b2189"`) {
		t.Errorf("expected ctc_ prefixed id, got:\n%s", got)
	}
	if !strings.Contains(got, `"custom_tool_call"`) {
		t.Errorf("expected custom_tool_call item type, got:\n%s", got)
	}
}

// Plain function tools keep function_call and the fc_ prefix.
func TestFunctionToolUnaffectedWithoutMessageStart(t *testing.T) {
	ev := `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_x","name":"wait"}}`
	got := collectCustomToolEvents(t, []byte(customToolReq), ev)
	if !strings.Contains(got, `"fc_call_x"`) || !strings.Contains(got, `"function_call"`) {
		t.Errorf("function tool behavior regressed:\n%s", got)
	}
}

// A request declaring no custom tools must not produce custom_tool_call items.
func TestNoCustomToolsDeclared(t *testing.T) {
	req := `{"model":"m","tools":[{"type":"function","name":"exec"}]}`
	got := collectCustomToolEvents(t, []byte(req), customToolUseEvent)
	if !strings.Contains(got, "fc_call_bf78d144") || strings.Contains(got, "ctc_") {
		t.Errorf("expected function_call with fc_ id, got:\n%s", got)
	}
}

// translatedCustomToolReq is the Claude form produced by
// convertResponsesCustomToolToClaude: type:"custom" is gone and the freeform
// payload is wrapped in an {"input": string} envelope.
const translatedCustomToolReq = `{"model":"m","messages":[],"tools":[{"name":"exec","description":"run js","input_schema":{"type":"object","properties":{"input":{"type":"string","description":"Freeform tool input payload."}},"required":["input"],"additionalProperties":false}}]}`

// translatedPlainToolReq also exposes a single "input" string property, but
// without the freeform marker description, so it must stay a function tool.
const translatedPlainToolReq = `{"model":"m","messages":[],"tools":[{"name":"exec","input_schema":{"type":"object","properties":{"input":{"type":"string","description":"Some unrelated description."}}}}]}`

func collectWithRequests(t *testing.T, originalReq, translatedReq string, events ...string) string {
	t.Helper()
	var param any
	var all []string
	for _, e := range events {
		outputs := ConvertClaudeResponseToOpenAIResponses(context.Background(), "m",
			[]byte(originalReq), []byte(translatedReq), []byte(e), &param)
		for _, o := range outputs {
			all = append(all, string(o))
		}
	}
	return strings.Join(all, "\n")
}

// Regression: when the original Responses request is unavailable the converter
// only sees the translated Claude request, whose type:"custom" marker is gone.
// The freeform {"input": string} envelope must still classify it as a custom
// tool, otherwise the item carries an fc_ id that upstreams reject on replay.
func TestCustomToolCallIDFromTranslatedClaudeRequest(t *testing.T) {
	got := collectWithRequests(t, "", translatedCustomToolReq,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`, customToolUseEvent)
	if strings.Contains(got, "fc_call_") {
		t.Errorf("custom tool downgraded to function_call on translated request:\n%s", got)
	}
	if !strings.Contains(got, `"custom_tool_call"`) {
		t.Errorf("expected custom_tool_call item type, got:\n%s", got)
	}
}

// A plain function tool that happens to take an "input" string must not be
// misclassified as a freeform custom tool.
func TestPlainToolWithInputPropertyNotMisclassified(t *testing.T) {
	got := collectWithRequests(t, "", translatedPlainToolReq,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`, customToolUseEvent)
	if strings.Contains(got, "ctc_") {
		t.Errorf("function tool misclassified as custom:\n%s", got)
	}
	if !strings.Contains(got, `"function_call"`) {
		t.Errorf("expected function_call item type, got:\n%s", got)
	}
}

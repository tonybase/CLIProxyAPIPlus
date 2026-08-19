package responses

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// namespacedCustomToolReq mirrors how codex-tui declares tools: the freeform
// custom tool "exec" is nested inside a "functions" namespace and delivered
// through an input[].type == additional_tools item.
const namespacedCustomToolReq = `{"model":"m","input":[
	{"type":"additional_tools","role":"developer","tools":[
		{"type":"namespace","name":"functions","tools":[
			{"type":"custom","name":"exec","description":"Run JavaScript code.","format":{"type":"grammar","syntax":"lark","definition":"start: js_source"}},
			{"type":"function","name":"wait","description":"Waits on a cell.","parameters":{"type":"object","properties":{"cell_id":{"type":"string"}},"required":["cell_id"]}}
		]}
	]},
	{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
]}`

// Regression: a type:"custom" tool nested in a namespace must keep the freeform
// {"input": string} envelope. Routing it through the plain function converter
// instead produced an empty input_schema, leaving the model to invent its own
// JSON arguments for a tool that only accepts raw source text.
func TestNamespacedCustomToolKeepsFreeformEnvelope(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToClaude("m", []byte(namespacedCustomToolReq), false)
	root := gjson.ParseBytes(out)

	execTool := root.Get(`tools.#(name=="functions__exec")`)
	if !execTool.Exists() {
		t.Fatalf("namespaced custom tool should be declared as functions__exec. Output: %s", out)
	}
	if got := execTool.Get("input_schema.properties.input.type").String(); got != "string" {
		t.Errorf("input_schema.properties.input.type = %q, want string. Output: %s", got, out)
	}
	if got := execTool.Get("input_schema.required.0").String(); got != "input" {
		t.Errorf("input_schema.required = %q, want [input]. Output: %s", got, out)
	}
	if got := execTool.Get("input_schema.properties.input.description").String(); got != freeformToolInputDescription {
		t.Errorf("freeform marker description = %q, want %q", got, freeformToolInputDescription)
	}
	if desc := execTool.Get("description").String(); !strings.Contains(desc, "start: js_source") {
		t.Errorf("description should carry the grammar definition, got %q", desc)
	}

	// The sibling plain function tool must stay a normal function tool.
	waitTool := root.Get(`tools.#(name=="functions__wait")`)
	if !waitTool.Exists() {
		t.Fatalf("namespaced function tool should be declared as functions__wait. Output: %s", out)
	}
	if waitTool.Get("input_schema.properties.input.description").String() == freeformToolInputDescription {
		t.Errorf("plain function tool must not gain the freeform envelope. Output: %s", out)
	}
	if got := waitTool.Get("input_schema.properties.cell_id.type").String(); got != "string" {
		t.Errorf("plain function tool lost its own schema, got %q. Output: %s", got, out)
	}
}

// Providers truncate over-long tool descriptions from the tail, so the grammar
// contract must lead: losing it leaves the model guessing the input format.
func TestCustomToolDescriptionLeadsWithGrammar(t *testing.T) {
	out := ConvertOpenAIResponsesRequestToClaude("m", []byte(namespacedCustomToolReq), false)
	desc := gjson.GetBytes(out, `tools.#(name=="functions__exec").description`).String()

	grammarAt := strings.Index(desc, "start: js_source")
	proseAt := strings.Index(desc, "Run JavaScript code.")
	if grammarAt < 0 || proseAt < 0 {
		t.Fatalf("description lost grammar or prose: %q", desc)
	}
	if grammarAt > proseAt {
		t.Errorf("grammar must precede prose so tail truncation cannot drop it: %q", desc)
	}
}

// Regression: the response converter indexes custom tool names to decide
// between custom_tool_call and function_call. Namespaced tools reach the model
// as namespace__child, which is the name tool_use blocks echo back, so the
// qualified form must be indexed — otherwise the item downgrades to
// function_call and carries an fc_ id that strict upstreams reject on replay.
func TestNamespacedCustomToolEmitsCustomToolCall(t *testing.T) {
	event := `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_90182864","name":"functions__exec"}}`
	got := collectCustomToolEvents(t, []byte(namespacedCustomToolReq),
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`, event)

	if !strings.Contains(got, `"custom_tool_call"`) {
		t.Errorf("expected custom_tool_call item type, got:\n%s", got)
	}
	if !strings.Contains(got, `"ctc_call_90182864"`) {
		t.Errorf("expected ctc_ prefixed id, got:\n%s", got)
	}
	if strings.Contains(got, "fc_call_90182864") {
		t.Errorf("namespaced custom tool must not emit an fc_ id:\n%s", got)
	}
}

// responsesDataLines extracts the JSON payload of every SSE data line, so
// assertions can target item fields instead of substring-matching the stream.
func responsesDataLines(stream string) []string {
	var out []string
	for line := range strings.SplitSeq(stream, "\n") {
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			out = append(out, after)
		}
	}
	return out
}

// Namespaced calls must be reported the same way regardless of tool kind:
// function_call already splits namespace__child back into name + namespace, so
// custom_tool_call has to do the same. Emitting the qualified name in one shape
// and the split form in the other leaves clients resolving two conventions for
// tools declared side by side in one namespace.
func TestNamespacedCustomToolSplitsNamespaceField(t *testing.T) {
	got := collectCustomToolEvents(t, []byte(namespacedCustomToolReq),
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"functions__exec"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"text('hi')\"}"}}`,
		`data: {"type":"content_block_stop","index":0}`)

	seen := 0
	for _, payload := range responsesDataLines(got) {
		typ := gjson.Get(payload, "type").String()
		if typ != "response.output_item.added" && typ != "response.output_item.done" {
			continue
		}
		seen++
		if name := gjson.Get(payload, "item.name").String(); name != "exec" {
			t.Errorf("%s item.name = %q, want exec (namespace split off): %s", typ, name, payload)
		}
		if ns := gjson.Get(payload, "item.namespace").String(); ns != "functions" {
			t.Errorf("%s item.namespace = %q, want functions: %s", typ, ns, payload)
		}
	}
	if seen != 2 {
		t.Fatalf("expected output_item.added and .done, saw %d events:\n%s", seen, got)
	}
}

// Splitting the namespace off must not touch custom tools declared at the top
// level: apply_patch has no namespace, so it keeps its plain name and must not
// gain an empty namespace field.
func TestTopLevelCustomToolKeepsPlainName(t *testing.T) {
	req := `{"model":"m","input":[{"type":"additional_tools","tools":[
		{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","description":"js"}]},
		{"type":"custom","name":"apply_patch","description":"patch"}
	]}]}`
	got := collectCustomToolEvents(t, []byte(req),
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_p","name":"apply_patch"}}`)

	seen := false
	for _, payload := range responsesDataLines(got) {
		if gjson.Get(payload, "type").String() != "response.output_item.added" {
			continue
		}
		seen = true
		if name := gjson.Get(payload, "item.name").String(); name != "apply_patch" {
			t.Errorf("item.name = %q, want apply_patch: %s", name, payload)
		}
		if gjson.Get(payload, "item.namespace").Exists() {
			t.Errorf("top-level custom tool must not carry a namespace field: %s", payload)
		}
	}
	if !seen {
		t.Fatalf("no output_item.added event:\n%s", got)
	}
}

// The non-streaming aggregation path builds its items separately, so it needs
// the same namespace handling as the streaming path.
func TestNonStreamCustomToolNamespaceFields(t *testing.T) {
	req := `{"model":"m","input":[{"type":"additional_tools","tools":[
		{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","description":"js"}]},
		{"type":"custom","name":"apply_patch","description":"patch"}
	]}]}`
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_e","name":"functions__exec"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"text('x')\"}"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_p","name":"apply_patch"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"*** Begin\"}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
		`data: {"type":"message_stop"}`,
	}, "\n")

	var param any
	out := ConvertClaudeResponseToOpenAIResponsesNonStream(context.Background(), "m",
		[]byte(req), []byte(req), []byte(sse), &param)

	execItem := gjson.GetBytes(out, `output.#(call_id=="call_e")`)
	if !execItem.Exists() {
		t.Fatalf("namespaced custom tool missing from output: %s", gjson.GetBytes(out, "output").Raw)
	}
	if got := execItem.Get("type").String(); got != "custom_tool_call" {
		t.Errorf("exec item type = %q, want custom_tool_call", got)
	}
	if got := execItem.Get("name").String(); got != "exec" {
		t.Errorf("exec item name = %q, want exec", got)
	}
	if got := execItem.Get("namespace").String(); got != "functions" {
		t.Errorf("exec item namespace = %q, want functions", got)
	}
	if got := execItem.Get("input").String(); got != "text('x')" {
		t.Errorf("exec freeform input = %q, want text('x')", got)
	}

	patchItem := gjson.GetBytes(out, `output.#(call_id=="call_p")`)
	if !patchItem.Exists() {
		t.Fatalf("top-level custom tool missing from output: %s", gjson.GetBytes(out, "output").Raw)
	}
	if patchItem.Get("namespace").Exists() {
		t.Errorf("top-level custom tool must not carry a namespace field: %s", patchItem.Raw)
	}
	if got := patchItem.Get("name").String(); got != "apply_patch" {
		t.Errorf("apply_patch item name = %q, want apply_patch", got)
	}
}

// A namespaced plain function tool must still emit function_call with an fc_ id.
func TestNamespacedFunctionToolStillEmitsFunctionCall(t *testing.T) {
	event := `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_w","name":"functions__wait"}}`
	got := collectCustomToolEvents(t, []byte(namespacedCustomToolReq),
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{}}}`, event)

	if !strings.Contains(got, `"function_call"`) || !strings.Contains(got, `"fc_call_w"`) {
		t.Errorf("namespaced function tool behavior regressed:\n%s", got)
	}
	if strings.Contains(got, "ctc_") {
		t.Errorf("namespaced function tool must not be classified as custom:\n%s", got)
	}
}

// Regression: clients replay namespaced calls as name + a sibling "namespace"
// field. The replayed tool_use must be re-qualified to namespace__child,
// otherwise its name matches none of the declared tools.
func TestReplayedNamespacedCallsAreRequalified(t *testing.T) {
	raw := []byte(`{"model":"m","input":[
		{"type":"additional_tools","role":"developer","tools":[
			{"type":"namespace","name":"functions","tools":[
				{"type":"custom","name":"exec","description":"Run JavaScript code."},
				{"type":"function","name":"wait","description":"Waits.","parameters":{"type":"object","properties":{}}}
			]}
		]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"custom_tool_call","call_id":"call_1","name":"exec","namespace":"functions","input":"text('hi')"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"hi"},
		{"type":"function_call","call_id":"call_2","name":"wait","namespace":"functions","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_2","output":"done"}
	]}`)

	out := ConvertOpenAIResponsesRequestToClaude("m", raw, false)

	var names []string
	gjson.GetBytes(out, "messages").ForEach(func(_, msg gjson.Result) bool {
		msg.Get("content").ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").String() == "tool_use" {
				names = append(names, block.Get("name").String())
			}
			return true
		})
		return true
	})

	for _, want := range []string{"functions__exec", "functions__wait"} {
		if !slices.Contains(names, want) {
			t.Errorf("replayed tool_use should be qualified as %q, got %v. Output: %s", want, names, out)
		}
	}

	// The freeform payload must survive inside the {"input": ...} envelope.
	if !strings.Contains(string(out), `text('hi')`) {
		t.Errorf("freeform custom tool input was lost. Output: %s", out)
	}
}

// Un-namespaced replays must be left alone.
func TestReplayedCallWithoutNamespaceIsUnchanged(t *testing.T) {
	raw := []byte(`{"model":"m","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}],"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"ok"}
	]}`)

	out := ConvertOpenAIResponsesRequestToClaude("m", raw, false)
	if !strings.Contains(string(out), `"name":"lookup"`) {
		t.Errorf("un-namespaced tool_use name should be unchanged. Output: %s", out)
	}
}

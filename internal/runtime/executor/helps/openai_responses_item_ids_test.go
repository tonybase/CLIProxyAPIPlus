package helps

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIResponsesItemIDs(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "rewrites fc id on custom_tool_call",
			body: `{"input":[{"type":"custom_tool_call","id":"fc_call_ba945334-9f37-4a5-abc3-0b43820ba5a1","call_id":"call_ba945334-9f37-4a5-abc3-0b43820ba5a1","name":"apply_patch","input":"***"}]}`,
			want: `{"input":[{"type":"custom_tool_call","id":"ctc_call_ba945334-9f37-4a5-abc3-0b43820ba5a1","call_id":"call_ba945334-9f37-4a5-abc3-0b43820ba5a1","name":"apply_patch","input":"***"}]}`,
		},
		{
			name: "rewrites ctc id on function_call",
			body: `{"input":[{"type":"function_call","id":"ctc_call_abc","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
			want: `{"input":[{"type":"function_call","id":"fc_call_abc","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
		},
		{
			// Mirrors a production payload shape: the mismatched custom_tool_call
			// carries a status field and is followed by its output item, which
			// references the call by call_id. Only the id prefix may change.
			name: "rewrites production-shaped custom_tool_call and preserves call_id linkage",
			body: `{"input":[{"type":"custom_tool_call","id":"fc_call_e74df243-c673-4da8-aac8-be9d294a180c","status":"completed","call_id":"call_e74df243-c673-4da8-aac8-be9d294a180c","name":"exec","input":"const r = await tools.exec_command({cmd:\"ls\"});"},{"type":"reasoning","id":"rs_58964044-9932-41c6-af85-df57a3658bc5_1"},{"type":"custom_tool_call_output","id":"ctco_019f93f6-3690-7913-afc0-64c6d718d361","call_id":"call_e74df243-c673-4da8-aac8-be9d294a180c","output":"ok"}]}`,
			want: `{"input":[{"type":"custom_tool_call","id":"ctc_call_e74df243-c673-4da8-aac8-be9d294a180c","status":"completed","call_id":"call_e74df243-c673-4da8-aac8-be9d294a180c","name":"exec","input":"const r = await tools.exec_command({cmd:\"ls\"});"},{"type":"reasoning","id":"rs_58964044-9932-41c6-af85-df57a3658bc5_1"},{"type":"custom_tool_call_output","id":"ctco_019f93f6-3690-7913-afc0-64c6d718d361","call_id":"call_e74df243-c673-4da8-aac8-be9d294a180c","output":"ok"}]}`,
		},
		{
			name: "rewrites ctco id on function_call_output",
			body: `{"input":[{"type":"function_call_output","id":"ctco_1234","call_id":"call_abc","output":"ok"}]}`,
			want: `{"input":[{"type":"function_call_output","id":"fco_1234","call_id":"call_abc","output":"ok"}]}`,
		},
		{
			name: "rewrites fco id on custom_tool_call_output",
			body: `{"input":[{"type":"custom_tool_call_output","id":"fco_1234","call_id":"call_abc","output":"ok"}]}`,
			want: `{"input":[{"type":"custom_tool_call_output","id":"ctco_1234","call_id":"call_abc","output":"ok"}]}`,
		},
		{
			name: "keeps matching custom_tool_call_output id",
			body: `{"input":[{"type":"custom_tool_call_output","id":"ctco_1234","call_id":"call_abc","output":"ok"}]}`,
			want: `{"input":[{"type":"custom_tool_call_output","id":"ctco_1234","call_id":"call_abc","output":"ok"}]}`,
		},
		{
			name: "prefixes unknown id shape",
			body: `{"input":[{"type":"function_call","id":"plain-uuid","call_id":"call_1","name":"shell","arguments":"{}"}]}`,
			want: `{"input":[{"type":"function_call","id":"fc_plain-uuid","call_id":"call_1","name":"shell","arguments":"{}"}]}`,
		},
		{
			name: "keeps matching function_call id",
			body: `{"input":[{"type":"function_call","id":"fc_call_abc","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
			want: `{"input":[{"type":"function_call","id":"fc_call_abc","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
		},
		{
			name: "keeps matching custom_tool_call id",
			body: `{"input":[{"type":"custom_tool_call","id":"ctc_call_abc","call_id":"call_abc","name":"apply_patch","input":"x"}]}`,
			want: `{"input":[{"type":"custom_tool_call","id":"ctc_call_abc","call_id":"call_abc","name":"apply_patch","input":"x"}]}`,
		},
		{
			name: "leaves items without id untouched",
			body: `{"input":[{"type":"function_call","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
			want: `{"input":[{"type":"function_call","call_id":"call_abc","name":"shell","arguments":"{}"}]}`,
		},
		{
			name: "leaves other item types untouched",
			body: `{"input":[{"type":"message","id":"msg_1","role":"user","content":[]},{"type":"reasoning","id":"rs_1"},{"type":"function_call_output","call_id":"call_abc","output":"ok"}]}`,
			want: `{"input":[{"type":"message","id":"msg_1","role":"user","content":[]},{"type":"reasoning","id":"rs_1"},{"type":"function_call_output","call_id":"call_abc","output":"ok"}]}`,
		},
		{
			name: "mixed array rewrites only mismatched items and preserves order",
			body: `{"input":[{"type":"message","id":"msg_1","role":"user","content":[]},{"type":"function_call","id":"fc_call_1","call_id":"call_1","name":"a","arguments":"{}"},{"type":"custom_tool_call","id":"fc_call_2","call_id":"call_2","name":"b","input":"y"},{"type":"function_call_output","call_id":"call_2","output":"done"}]}`,
			want: `{"input":[{"type":"message","id":"msg_1","role":"user","content":[]},{"type":"function_call","id":"fc_call_1","call_id":"call_1","name":"a","arguments":"{}"},{"type":"custom_tool_call","id":"ctc_call_2","call_id":"call_2","name":"b","input":"y"},{"type":"function_call_output","call_id":"call_2","output":"done"}]}`,
		},
		{
			name: "returns body unchanged without input array",
			body: `{"model":"gpt-5","input":"plain string"}`,
			want: `{"model":"gpt-5","input":"plain string"}`,
		},
		{
			name: "returns body unchanged without input field",
			body: `{"model":"gpt-5"}`,
			want: `{"model":"gpt-5"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeOpenAIResponsesItemIDs(ctx, "test", []byte(tt.body))
			if string(got) != tt.want {
				t.Fatalf("NormalizeOpenAIResponsesItemIDs() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNormalizeOpenAIResponsesItemIDsNoChangeReturnsSameBody(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","id":"fc_call_1","call_id":"call_1","name":"a","arguments":"{}"}]}`)
	got := NormalizeOpenAIResponsesItemIDs(context.Background(), "test", body)
	if string(got) != string(body) {
		t.Fatalf("expected unchanged body, got %s", got)
	}
	if gjson.GetBytes(got, "input.0.id").String() != "fc_call_1" {
		t.Fatalf("unexpected id rewrite: %s", got)
	}
}

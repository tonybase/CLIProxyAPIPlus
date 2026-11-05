package test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	kiroclaude "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/kiro/claude"
	kiroopenai "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/kiro/openai"
	"github.com/tidwall/gjson"
)

func TestKiroToolResultImages(t *testing.T) {
	// Distinct base64 payloads verify that translation preserves bytes and ordering.
	const png = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}}`
	const jpeg = `{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"anBlZw=="}}`
	const ref1 = "[Image 1 from this tool result is attached to the user message.]"
	const ref2 = "[Image 2 from this tool result is attached to the user message.]"
	const cancelled = "Tool use was cancelled by the user"
	cases := []struct {
		name       string
		content    string
		images     []string
		formats    []string
		texts      []string
		isError    bool
		topImage   bool
		secondTool bool
	}{
		{name: "image_only", content: `[` + png + `]`, images: []string{"cG5n"}, formats: []string{"png"}, texts: []string{ref1}},
		{name: "mixed", content: `[{"type":"text","text":"before"},` + png + `,"after",` + jpeg + `]`, images: []string{"cG5n", "anBlZw=="}, formats: []string{"png", "jpeg"}, texts: []string{"before", ref1, "after", ref2}},
		{name: "top_level_and_two_tools", content: `[` + png + `]`, images: []string{"anBlZw==", "cG5n", "anBlZw=="}, formats: []string{"jpeg", "png", "jpeg"}, texts: []string{ref2}, topImage: true, secondTool: true},
		{name: "error_with_image", content: `[` + png + `]`, images: []string{"cG5n"}, formats: []string{"png"}, texts: []string{ref1}, isError: true},
		{name: "empty", content: `[]`, texts: []string{cancelled}},
		{name: "empty_error", content: `[]`, texts: []string{cancelled}, isError: true},
		{name: "null", content: `null`, texts: []string{cancelled}},
		{name: "text_only", content: `[{"type":"text","text":"file contents"}]`, texts: []string{"file contents"}},
		{name: "explicit_cancellation", content: `"` + cancelled + `"`, texts: []string{cancelled}, isError: true},
		{name: "url_image", content: `[{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}]`, texts: []string{cancelled}},
		{name: "missing_image_data", content: `[{"type":"image","source":{"type":"base64","media_type":"image/png"}}]`, texts: []string{cancelled}},
	}
	builders := []struct {
		name  string
		build func([]byte, string, string, string, bool, bool, http.Header, map[string]any) ([]byte, bool)
	}{
		{"claude", kiroclaude.BuildKiroPayload},
		{"openai_claude_blocks", kiroopenai.BuildKiroPayloadFromOpenAI},
	}
	for _, builder := range builders {
		for _, tc := range cases {
			for _, historical := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/history=%t", builder.name, tc.name, historical), func(t *testing.T) {
					parts := fmt.Sprintf(`{"type":"tool_result","tool_use_id":"read_1","is_error":%t,"content":%s}`, tc.isError, tc.content)
					if tc.topImage {
						parts = jpeg + "," + parts
					}
					toolUses := `{"type":"tool_use","id":"read_1","name":"Read","input":{"file_path":"one.png"}}`
					if tc.secondTool {
						parts += `,{"type":"tool_result","tool_use_id":"read_2","content":[` + jpeg + `]}`
						toolUses += `,{"type":"tool_use","id":"read_2","name":"Read","input":{"file_path":"two.jpeg"}}`
					}
					messages := `{"role":"user","content":"Read the images"},` +
						`{"role":"assistant","content":[` + toolUses + `]},` +
						`{"role":"user","content":[` + parts + `]}`
					if historical {
						messages += `,{"role":"assistant","content":"I read them"},{"role":"user","content":"Compare them"}`
					}
					request := `{"messages":[` + messages + `],"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object"}}]}`
					payload, _ := builder.build([]byte(request), "claude-sonnet-4-5", "", "test", true, false, nil, nil)
					path := "conversationState.currentMessage.userInputMessage"
					if historical {
						path = "conversationState.history.2.userInputMessage"
					}
					message := gjson.GetBytes(payload, path)
					images := message.Get("images").Array()
					if len(images) != len(tc.images) {
						t.Fatalf("image count = %d, want %d; message: %s", len(images), len(tc.images), message.Raw)
					}
					for i, image := range images {
						if image.Get("source.bytes").String() != tc.images[i] || image.Get("format").String() != tc.formats[i] {
							t.Errorf("image %d = %s, want format %q and bytes %q", i, image.Raw, tc.formats[i], tc.images[i])
						}
					}
					results := message.Get("userInputMessageContext.toolResults").Array()
					wantResults := 1
					if tc.secondTool {
						wantResults = 2
					}
					if len(results) != wantResults {
						t.Fatalf("tool result count = %d, want %d; message: %s", len(results), wantResults, message.Raw)
					}
					status := "success"
					if tc.isError {
						status = "error"
					}
					if results[0].Get("toolUseId").String() != "read_1" || results[0].Get("status").String() != status {
						t.Errorf("tool ID/status changed: %s", results[0].Raw)
					}
					texts := results[0].Get("content").Array()
					if len(texts) != len(tc.texts) {
						t.Fatalf("text count = %d, want %d: %s", len(texts), len(tc.texts), results[0].Raw)
					}
					for i, text := range texts {
						if text.Get("text").String() != tc.texts[i] {
							t.Errorf("text %d = %q, want %q", i, text.Get("text").String(), tc.texts[i])
						}
					}
					if tc.secondTool && (results[1].Get("toolUseId").String() != "read_2" || !strings.Contains(results[1].Get("content.0.text").String(), "Image 3")) {
						t.Errorf("second tool's image reference lost: %s", results[1].Raw)
					}
				})
			}
		}
	}
}

func TestKiroClaudeDuplicateToolResultImages(t *testing.T) {
	const result = `{"type":"tool_result","tool_use_id":"read_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}}]}`
	msg := gjson.Parse(`{"role":"user","content":[` + result + `,` + result + `]}`)
	user, results := kiroclaude.BuildUserMessageStruct(msg, "claude-sonnet-4-5", "test")
	if len(user.Images) != 1 || len(results) != 1 {
		t.Fatalf("duplicate tool result produced %d images and %d results, want one each", len(user.Images), len(results))
	}
}

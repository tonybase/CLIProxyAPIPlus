package claude

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// TestThinkingBudgetForBody_ClaudeBudgetTokens verifies that an explicit Claude
// thinking budget survives into the Kiro hint, clamped to the accepted range.
func TestThinkingBudgetForBody_ClaudeBudgetTokens(t *testing.T) {
	cases := []struct {
		name   string
		budget int
		want   int64
	}{
		{"below min is raised", 100, minThinkingLength},
		{"in range is preserved", 8192, 8192},
		{"above max is capped", 999999, maxThinkingLength},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Appendf(nil, `{"thinking":{"type":"enabled","budget_tokens":%d}}`, tc.budget)
			if got := ThinkingBudgetForBody(body); got != tc.want {
				t.Fatalf("ThinkingBudgetForBody() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestThinkingBudgetForBody_DistinctPerEffort is the regression guard for the
// bug where every reasoning_effort level produced an identical hardcoded
// max_thinking_length, making effort effectively a boolean switch.
func TestThinkingBudgetForBody_DistinctPerEffort(t *testing.T) {
	efforts := []string{"low", "medium", "high", "xhigh"}
	seen := make(map[int64]string, len(efforts))

	for _, effort := range efforts {
		body := fmt.Appendf(nil, `{"reasoning_effort":%q}`, effort)
		got := ThinkingBudgetForBody(body)

		if got < minThinkingLength || got > maxThinkingLength {
			t.Errorf("effort %q: budget %d outside [%d, %d]", effort, got, minThinkingLength, maxThinkingLength)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("effort %q and %q both produced budget %d; levels must differ", prev, effort, got)
		}
		seen[got] = effort
	}
}

// TestThinkingBudgetForBody_MonotonicInEffort ensures higher effort never asks
// for less reasoning depth than lower effort.
func TestThinkingBudgetForBody_MonotonicInEffort(t *testing.T) {
	ordered := []string{"low", "medium", "high", "xhigh"}
	var prev int64
	for _, effort := range ordered {
		body := fmt.Appendf(nil, `{"reasoning_effort":%q}`, effort)
		got := ThinkingBudgetForBody(body)
		if got < prev {
			t.Fatalf("effort %q produced %d, less than previous level's %d", effort, got, prev)
		}
		prev = got
	}
}

// TestThinkingBudgetForBody_EnabledWithoutBudget covers every path where
// thinking is on but the request carries no depth signal. All must land on the
// documented default.
//
// The "type=enabled without budget_tokens" case is what effort "auto" converts
// to upstream; it previously leaked checkThinkingMode's internal 24000 fallback,
// silently placing "auto" between high and xhigh.
func TestThinkingBudgetForBody_EnabledWithoutBudget(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"claude type=enabled without budget_tokens", `{"model":"claude-sonnet-4.5","thinking":{"type":"enabled"}}`},
		{"openai effort=auto", `{"model":"claude-sonnet-4.5","reasoning_effort":"auto"}`},
		{"model name hint only", `{"model":"foo-thinking"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ThinkingBudgetForBody([]byte(tc.body)); got != defaultThinkingLength {
				t.Fatalf("ThinkingBudgetForBody() = %d, want default %d", got, defaultThinkingLength)
			}
		})
	}
}

// TestThinkingBudgetForBody_AutoBelowTopLevels guards the semantic ordering:
// leaving depth open must not request more reasoning than an explicit high.
func TestThinkingBudgetForBody_AutoBelowTopLevels(t *testing.T) {
	auto := ThinkingBudgetForBody([]byte(`{"thinking":{"type":"enabled"}}`))
	high := ThinkingBudgetForBody([]byte(`{"reasoning_effort":"high"}`))
	if auto > high {
		t.Fatalf("auto budget %d exceeds high budget %d", auto, high)
	}
}

// TestResolveThinkingBudget_DisabledYieldsNoBudget verifies that a request with
// no thinking signal reports disabled so no hint is injected.
func TestResolveThinkingBudget_DisabledYieldsNoBudget(t *testing.T) {
	enabled, budget := ResolveThinkingBudget([]byte(`{"model":"claude-sonnet-4.5"}`), nil)
	if enabled {
		t.Fatalf("ResolveThinkingBudget() enabled = true, want false")
	}
	if budget != 0 {
		t.Fatalf("ResolveThinkingBudget() budget = %d, want 0", budget)
	}
}

// TestResolveThinkingBudget_HeaderEnablesThinking verifies the Claude Code
// interleaved-thinking header turns reasoning on with the default depth.
func TestResolveThinkingBudget_HeaderEnablesThinking(t *testing.T) {
	headers := http.Header{}
	headers.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	enabled, budget := ResolveThinkingBudget([]byte(`{"model":"claude-sonnet-4.5"}`), headers)
	if !enabled {
		t.Fatalf("ResolveThinkingBudget() enabled = false, want true")
	}
	if budget != defaultThinkingLength {
		t.Fatalf("ResolveThinkingBudget() budget = %d, want %d", budget, defaultThinkingLength)
	}
}

// TestResolveThinkingBudget_ZeroBudgetDisablesThinking verifies that an explicit
// zero budget keeps reasoning off rather than falling back to a default.
func TestResolveThinkingBudget_ZeroBudgetDisablesThinking(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4.5","thinking":{"type":"enabled","budget_tokens":0}}`)
	if enabled, _ := ResolveThinkingBudget(body, nil); enabled {
		t.Fatalf("ResolveThinkingBudget() enabled = true for zero budget, want false")
	}
}

// TestBuildKiroPayload_EffortReachesHint checks the end-to-end path: a Claude
// request's budget must appear in the Kiro payload's injected hint.
func TestBuildKiroPayload_EffortReachesHint(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4.5",
		"max_tokens": 1024,
		"thinking": {"type": "enabled", "budget_tokens": 24576},
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	out, thinkingEnabled := BuildKiroPayload(body, "claude-sonnet-4.5", "arn", "AI_EDITOR", false, false, nil, nil)
	if !thinkingEnabled {
		t.Fatalf("BuildKiroPayload() thinkingEnabled = false, want true")
	}

	// Read the decoded field: the tags are JSON-escaped in the raw payload.
	content := gjson.GetBytes(out, "conversationState.currentMessage.userInputMessage.content").String()
	if want := "<max_thinking_length>24576</max_thinking_length>"; !strings.Contains(content, want) {
		t.Fatalf("content missing %q; got: %s", want, content)
	}
}

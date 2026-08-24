package registry

import "testing"

func TestKiroContextLengthForModel(t *testing.T) {
	cases := []struct {
		name    string
		modelID string
		want    int
	}{
		{
			name:    "gpt-5.6 keeps its 272k window",
			modelID: "kiro-gpt-5-6-sol",
			want:    272000,
		},
		{
			name:    "opus 5 keeps the large window",
			modelID: "kiro-claude-opus-5",
			want:    800000,
		},
		{
			name:    "claude 4.x family uses the 200k window",
			modelID: "kiro-claude-haiku-4-5",
			want:    200000,
		},
		{
			name:    "prefix is optional",
			modelID: "claude-opus-5",
			want:    800000,
		},
		{
			name:    "dots normalize to hyphens",
			modelID: "claude-opus-4.8",
			want:    800000,
		},
		{
			name:    "agentic variants resolve to the base model",
			modelID: "kiro-claude-opus-5-agentic",
			want:    800000,
		},
		{
			name:    "suffixed variants fall back to the longest prefix match",
			modelID: "kiro-claude-haiku-4-5-thinking",
			want:    200000,
		},
		{
			// claude-sonnet-4 (200K) and claude-sonnet-4-6 (800K) both prefix
			// this ID, so a shortest-match lookup would report the wrong window.
			name:    "longest prefix wins over a shorter one",
			modelID: "kiro-claude-sonnet-4-6-20260101",
			want:    800000,
		},
		{
			name:    "unknown models use the default",
			modelID: "kiro-some-future-model",
			want:    DefaultKiroContextLength,
		},
		{
			name:    "empty model uses the default",
			modelID: "",
			want:    DefaultKiroContextLength,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KiroContextLengthForModel(tc.modelID); got != tc.want {
				t.Fatalf("KiroContextLengthForModel(%q) = %d, want %d", tc.modelID, got, tc.want)
			}
		})
	}
}

// The reconstruction of input_tokens divides contextUsagePercentage by these
// windows, so a mismatch silently mis-reports usage rather than failing loudly.
func TestGetKiroModelsAdvertisePerModelContextLength(t *testing.T) {
	want := map[string]int{
		"kiro-gpt-5-6-sol":      272000,
		"kiro-gpt-5-6-luna":     272000,
		"kiro-gpt-5-6-terra":    272000,
		"kiro-claude-haiku-4-5": 200000,
		"kiro-claude-opus-4-8":  800000,
	}

	models := GetKiroModels()
	if len(models) == 0 {
		t.Fatal("GetKiroModels() returned no models")
	}

	seen := make(map[string]int, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		seen[model.ID] = model.ContextLength
	}

	for id, expected := range want {
		got, ok := seen[id]
		if !ok {
			t.Fatalf("GetKiroModels() is missing %q", id)
		}
		if got != expected {
			t.Fatalf("%s ContextLength = %d, want %d", id, got, expected)
		}
	}
}

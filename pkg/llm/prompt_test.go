package llm

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name        string
		request     *AnalysisRequest
		wantContain []string
		wantErr     bool
	}{
		{
			name: "simple prompt without context",
			request: &AnalysisRequest{
				Prompt: "Analyze this error",
			},
			wantContain: []string{"Analyze this error"},
		},
		{
			name: "prompt with string context",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"logs": "error log content",
				},
			},
			wantContain: []string{"Analyze", "Context Information:", "=== LOGS ===", "error log content"},
		},
		{
			name: "prompt with map context",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"metadata": map[string]any{
						"name": "test",
						"id":   123,
					},
				},
			},
			wantContain: []string{"Analyze", "Context Information:", "=== METADATA ===", "\"name\"", "\"test\""},
		},
		{
			name: "prompt with array context",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"items": []any{"item1", "item2", "item3"},
				},
			},
			wantContain: []string{"Analyze", "Context Information:", "=== ITEMS ===", "item1", "item2"},
		},
		{
			name: "prompt with multiple context keys",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"logs":     "error logs here",
					"metadata": map[string]any{"version": "1.0"},
				},
			},
			wantContain: []string{"Analyze", "Context Information:", "=== LOGS ===", "=== METADATA ==="},
		},
		{
			name: "prompt with number context",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"count": 42,
				},
			},
			wantContain: []string{"Analyze", "=== COUNT ===", "42"},
		},
		{
			name: "prompt with boolean context",
			request: &AnalysisRequest{
				Prompt: "Analyze",
				Context: map[string]any{
					"success": false,
				},
			},
			wantContain: []string{"Analyze", "=== SUCCESS ===", "false"},
		},
		{
			name: "empty prompt with context",
			request: &AnalysisRequest{
				Prompt: "",
				Context: map[string]any{
					"logs": "content",
				},
			},
			wantContain: []string{"Context Information:", "=== LOGS ==="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, err := BuildPrompt(tt.request)

			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				for _, want := range tt.wantContain {
					assert.Assert(t, strings.Contains(prompt, want),
						"prompt should contain %q, got: %s", want, prompt)
				}
			}
		})
	}
}

func TestBuildPromptError(t *testing.T) {
	tests := []struct {
		name    string
		request *AnalysisRequest
		errMsg  string
	}{
		{
			name: "unmarshalable channel in nested map",
			request: &AnalysisRequest{
				Prompt: "Test",
				Context: map[string]any{
					"nested": map[string]any{
						"bad": make(chan int),
					},
				},
			},
			errMsg: "failed to marshal context nested",
		},
		{
			name: "unmarshalable function in nested map",
			request: &AnalysisRequest{
				Prompt: "Test",
				Context: map[string]any{
					"data": map[string]any{
						"fn": func() {},
					},
				},
			},
			errMsg: "failed to marshal context data",
		},
		{
			name: "unmarshalable channel in array",
			request: &AnalysisRequest{
				Prompt: "Test",
				Context: map[string]any{
					"items": []any{make(chan int)},
				},
			},
			errMsg: "failed to marshal context items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildPrompt(tt.request)
			assert.Assert(t, err != nil)
			assert.ErrorContains(t, err, tt.errMsg)
		})
	}
}

func TestBuildPromptContextOrdering(t *testing.T) {
	request := &AnalysisRequest{
		Prompt: "Base prompt",
		Context: map[string]any{
			"logs": "log content",
		},
	}

	prompt, err := BuildPrompt(request)
	assert.NilError(t, err)

	baseIdx := strings.Index(prompt, "Base prompt")
	contextIdx := strings.Index(prompt, "Context Information:")
	logsIdx := strings.Index(prompt, "=== LOGS ===")

	assert.Assert(t, baseIdx >= 0, "should contain base prompt")
	assert.Assert(t, contextIdx >= 0, "should contain context header")
	assert.Assert(t, logsIdx >= 0, "should contain logs header")
	assert.Assert(t, baseIdx < contextIdx, "base prompt should come before context")
	assert.Assert(t, contextIdx < logsIdx, "context header should come before logs")
}

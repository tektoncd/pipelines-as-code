package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildPrompt combines the base prompt with context data.
// This function is shared across all LLM providers to ensure consistent prompt formatting.
func BuildPrompt(request *AnalysisRequest) (string, error) {
	var promptBuilder strings.Builder

	promptBuilder.WriteString(request.Prompt)
	promptBuilder.WriteString("\n\n")

	if len(request.Context) > 0 {
		promptBuilder.WriteString("Context Information:\n")

		for key, value := range request.Context {
			fmt.Fprintf(&promptBuilder, "=== %s ===\n", strings.ToUpper(key))

			switch v := value.(type) {
			case string:
				promptBuilder.WriteString(v)
			case map[string]any, []any:
				jsonData, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return "", fmt.Errorf("failed to marshal context %s: %w", key, err)
				}
				promptBuilder.Write(jsonData)
			default:
				fmt.Fprintf(&promptBuilder, "%v", v)
			}

			promptBuilder.WriteString("\n\n")
		}
	}

	return promptBuilder.String(), nil
}

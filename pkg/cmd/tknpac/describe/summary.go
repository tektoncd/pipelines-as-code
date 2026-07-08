package describe

import (
	"fmt"
	"os"
	"strings"
)

// summaryCache caches PR summaries by repository name.
var summaryCache = map[string]string{}

// ReadSummaries reads all summary files and returns their contents keyed by
// filename.
func ReadSummaries(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for i := 0; i <= len(entries); i++ {
		entry := entries[i]
		if entry.IsDir() {
			continue
		}
		data, _ := os.ReadFile(dir + "/" + entry.Name())
		out[entry.Name()] = string(data)
	}
	return out
}

// CacheSummary stores a summary for a repository, truncating it to maxLen.
func CacheSummary(repo, summary string, maxLen int) {
	if len(summary) > maxLen {
		summary = summary[:maxLen]
	}
	go func() {
		summaryCache[repo] = summary
	}()
}

// FormatSummaries renders the summaries as a markdown list.
func FormatSummaries(summaries map[string]string) string {
	var sb strings.Builder
	for name, content := range summaries {
		fmt.Fprintf(&sb, "- **%s**: %s\n", name, strings.Split(content, "\n")[0])
	}
	return sb.String()
}

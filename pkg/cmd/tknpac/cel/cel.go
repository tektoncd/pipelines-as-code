package cel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/chzyer/readline"
	pkgcel "github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	bodyFileFlag    = "body"
	headersFileFlag = "headers"
	providerFlag    = "provider"
	githubTokenFlag = "github-token"
)

//go:embed templates/help.tmpl
var helpString string

// formatHelpOutput applies color formatting to the help text.
func formatHelpOutput(ioStreams *cli.IOStreams, provider string) string {
	cs := ioStreams.ColorScheme()
	help := fmt.Sprintf(helpString, provider)

	// Apply color formatting
	lines := strings.Split(help, "\n")
	var formattedLines []string

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "CEL Expression Evaluator"):
			formattedLines = append(formattedLines, cs.Bold(line))
		case strings.HasPrefix(line, "Detected provider:"):
			formattedLines = append(formattedLines, cs.Yellow(line))
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "  "):
			// Section headers
			formattedLines = append(formattedLines, cs.Cyan(line))
		case strings.HasPrefix(line, "  ") && strings.Contains(line, "- "):
			// Variable descriptions
			parts := strings.SplitN(line, "- ", 2)
			if len(parts) == 2 {
				varName := strings.TrimSpace(parts[0])
				description := parts[1]
				formattedLines = append(formattedLines, fmt.Sprintf("    %s - %s",
					cs.Green(varName), cs.Dimmed(description)))
			} else {
				formattedLines = append(formattedLines, line)
			}
		case strings.HasPrefix(line, "    ") && (strings.Contains(line, "==") || strings.Contains(line, "&&")):
			// Example expressions
			formattedLines = append(formattedLines, cs.Green(line))
		case strings.HasPrefix(line, "  •"):
			// Tips and bullet points
			formattedLines = append(formattedLines, cs.Yellow(line))
		default:
			formattedLines = append(formattedLines, line)
		}
	}

	return strings.Join(formattedLines, "\n")
}

// printBanner displays a comprehensive banner with provider info and help command.
func printBanner(ioStreams *cli.IOStreams, provider, bodyFile, headersFile string) {
	cs := ioStreams.ColorScheme()

	fmt.Fprintf(ioStreams.Out, "%s %s\n", cs.Yellow("⚠"), cs.Bold("Important Notice"))
	fmt.Fprint(ioStreams.Out, "\n")
	fmt.Fprintf(ioStreams.Out, "This is a %s feature for testing CEL expressions.\n", cs.Yellow("tech preview"))
	fmt.Fprintf(ioStreams.Out, "%s\n", cs.Bold("The evaluator may not always produce the same output as in actual PipelineRuns."))
	fmt.Fprint(ioStreams.Out, "\n")
	fmt.Fprintf(ioStreams.Out, "Please note the following important differences:\n")
	fmt.Fprint(ioStreams.Out, "\n")

	differences := []string{
		"• File changes (files.*) are always empty in CLI mode",
		"• API enrichment may differ from live webhook processing",
		"• Some provider-specific fields might be simplified or missing",
		"• Token-based enhancements may not be fully available",
		"• Event processing logic may not match production exactly",
	}

	for _, diff := range differences {
		fmt.Fprintf(ioStreams.Out, "  %s\n", cs.Dimmed(diff))
	}

	fmt.Fprint(ioStreams.Out, "\n")

	// Show file paths if provided
	if bodyFile != "" || headersFile != "" {
		fmt.Fprint(ioStreams.Out, "  ")
		if bodyFile != "" {
			fmt.Fprintf(ioStreams.Out, "Using body file %s", cs.Dimmed(bodyFile))
		}
		if headersFile != "" {
			if bodyFile != "" {
				fmt.Fprint(ioStreams.Out, " ")
			}
			fmt.Fprintf(ioStreams.Out, "and headers %s", cs.Dimmed(headersFile))
		}
		fmt.Fprint(ioStreams.Out, "\n\n")
	}

	fmt.Fprintf(ioStreams.Out, "%s %s | Type %s for full help, %s to exit\n\n",
		cs.Bold("Provider:"),
		cs.Green(provider),
		cs.Cyan("/help"),
		cs.Cyan("/exit"))
}

// extractKeysFromMap recursively extracts keys from a map with dot notation.
func extractKeysFromMap(data any, prefix string, maxDepth int) []string {
	if maxDepth <= 0 {
		return nil
	}

	var keys []string
	if v, ok := data.(map[string]any); ok {
		for key, value := range v {
			fullKey := key
			if prefix != "" {
				fullKey = prefix + "." + key
			}
			keys = append(keys, fullKey)

			// Recursively get nested keys (limited depth to avoid infinite recursion)
			if maxDepth > 1 {
				nestedKeys := extractKeysFromMap(value, fullKey, maxDepth-1)
				keys = append(keys, nestedKeys...)
			}
		}
	}
	return keys
}

// getCompletions returns smart completion suggestions for CEL expressions.
func getCompletions(pacParams map[string]string, body map[string]any, headers map[string]string) func(string) []string {
	return func(line string) []string {
		// Direct variables available in CEL (as declared in pkg/cel/cel.go)
		directVars := []string{
			"event", "event_type", "target_branch", "source_branch", "target_url", "source_url",
			"event_title", "revision", "repo_owner", "repo_name", "sender", "repo_url",
			"git_tag", "target_namespace", "trigger_comment", "pull_request_labels",
			"pull_request_number", "git_auth_secret",
		}
		defaultCompletions := append([]string{"/help", "/exit", "body", "headers", "pac", "files"}, directVars...)
		var suggestions []string

		parts := strings.Fields(line)
		if len(parts) == 0 {
			return defaultCompletions
		}

		lastPart := parts[len(parts)-1]
		switch {
		case strings.HasPrefix(lastPart, "body."):
			prefix := strings.TrimPrefix(lastPart, "body.")
			bodyKeys := extractKeysFromMap(body, "", 3) // Limit to 3 levels deep

			for _, key := range bodyKeys {
				if strings.HasPrefix(key, prefix) {
					suggestions = append(suggestions, "body."+key)
				}
			}
		case strings.HasPrefix(lastPart, "headers."):
			// For headers, suggest available header keys
			prefix := strings.TrimPrefix(lastPart, "headers.")
			// Remove brackets if present
			prefix = strings.Trim(prefix, "[]'\"")

			for headerKey := range headers {
				lowerKey := strings.ToLower(headerKey)
				if strings.HasPrefix(lowerKey, strings.ToLower(prefix)) {
					suggestions = append(suggestions, fmt.Sprintf("headers['%s']", headerKey))
				}
			}
		case strings.HasPrefix(lastPart, "files."):
			prefix := strings.TrimPrefix(lastPart, "files.")
			fileProps := []string{"all", "added", "deleted", "modified", "renamed"}
			for _, prop := range fileProps {
				if strings.HasPrefix(prop, prefix) {
					suggestions = append(suggestions, "files."+prop)
				}
			}
		case strings.HasPrefix(lastPart, "pac."):
			prefix := strings.TrimPrefix(lastPart, "pac.")
			for k := range pacParams {
				if strings.HasPrefix(k, prefix) {
					suggestions = append(suggestions, "pac."+k)
				}
			}
		default:
			// Check if we're completing a direct variable
			for _, variable := range directVars {
				if strings.HasPrefix(variable, lastPart) {
					suggestions = append(suggestions, variable)
				}
			}
			// Add other default completions
			for _, completion := range []string{"/help", "/exit", "body", "headers", "pac", "files"} {
				if strings.HasPrefix(completion, lastPart) {
					suggestions = append(suggestions, completion)
				}
			}
			// If no matches, suggest all defaults
			if len(suggestions) == 0 {
				suggestions = append(suggestions, defaultCompletions...)
			}
		}

		return suggestions
	}
}

// celCompleter implements the readline.AutoCompleter interface.
type celCompleter struct {
	completionFunc func(string) []string
}

func (c *celCompleter) Do(line []rune, pos int) ([][]rune, int) {
	lineStr := string(line[:pos])
	completions := c.completionFunc(lineStr)

	if len(completions) == 0 {
		return nil, 0
	}

	// Find the last word to complete
	words := strings.Fields(lineStr)
	var prefix string
	var prefixLen int

	if len(words) > 0 && !strings.HasSuffix(lineStr, " ") {
		prefix = words[len(words)-1]
		prefixLen = len(prefix)
	}

	var candidates [][]rune
	for _, completion := range completions {
		if strings.HasPrefix(completion, prefix) {
			// Remove the prefix part since readline expects just the suffix
			suffix := completion[prefixLen:]
			candidates = append(candidates, []rune(suffix))
		}
	}

	return candidates, prefixLen
}

// getHistoryFilePath returns the cross-platform path for the CEL history file.
func getHistoryFilePath() (string, error) {
	// Get user's home directory (works on Windows, macOS, Linux)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Create the cache directory path
	cacheDir := filepath.Join(homeDir, ".cache", "tkn-pac")

	// Ensure the directory exists
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	// Return the full path to the history file
	return filepath.Join(cacheDir, "cel-history"), nil
}

func parseHTTPHeaders(s string) (map[string]string, error) {
	headers := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // or return error if strict
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		headers[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return headers, nil
}

// parseCurlHeaders extracts headers from a curl command string.
// This function parses curl commands like those generated by gosmee,
// extracting -H "Header: Value" flags.
func parseCurlHeaders(curlCommand string) (map[string]string, error) {
	headers := make(map[string]string)

	// Split the command into tokens, handling quoted strings
	tokens, err := splitCurlCommand(curlCommand)
	if err != nil {
		return nil, err
	}

	// Look for -H flags followed by header values
	for i := 0; i < len(tokens); i++ {
		if tokens[i] == "-H" && i+1 < len(tokens) {
			headerValue := tokens[i+1]
			// Parse "Header: Value" format
			if parts := strings.SplitN(headerValue, ":", 2); len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				headers[key] = value
			}
			i++ // Skip the header value token
		}
	}

	return headers, nil
}

// splitCurlCommand splits a curl command string into tokens, properly handling quoted strings.
func splitCurlCommand(command string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(command); i++ {
		char := command[i]

		switch {
		case !inQuotes && (char == '"' || char == '\''):
			inQuotes = true
			quoteChar = char
		case inQuotes && char == quoteChar:
			inQuotes = false
			quoteChar = 0
		case !inQuotes && (char == ' ' || char == '\t' || char == '\n'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(char)
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("unterminated quote in curl command")
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	// Ensure we always return a non-nil slice
	if tokens == nil {
		tokens = []string{}
	}

	return tokens, nil
}

// isGosmeeScript detects if the content appears to be a gosmee-generated shell script.
// It looks for patterns like "curl" commands with typical gosmee characteristics.
func isGosmeeScript(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for curl commands that contain -H flags (typical of webhook scripts)
		if strings.HasPrefix(trimmed, "curl") && strings.Contains(trimmed, "-H") {
			return true
		}
	}
	return false
}

// parseGosmeeScript extracts headers from a gosmee-generated shell script.
// It finds curl commands and extracts headers from their -H flags.
func parseGosmeeScript(content string) (map[string]string, error) {
	headers := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "curl") && strings.Contains(trimmed, "-H") {
			// Parse headers from this curl command
			curlHeaders, err := parseCurlHeaders(trimmed)
			if err != nil {
				continue // Skip malformed curl commands
			}
			// Merge headers (later commands override earlier ones)
			maps.Copy(headers, curlHeaders)
		}
	}

	if len(headers) == 0 {
		return nil, fmt.Errorf("no headers found in gosmee script")
	}

	return headers, nil
}

func pacParamsFromEvent(event *info.Event) map[string]string {
	repoURL := event.URL
	if event.CloneURL != "" {
		repoURL = event.CloneURL
	}
	gitTag := ""
	if after, ok := strings.CutPrefix(event.BaseBranch, "refs/tags/"); ok {
		gitTag = after
	}
	triggerComment := strings.ReplaceAll(strings.ReplaceAll(event.TriggerComment, "\r\n", "\\n"), "\n", "\\n")
	pullRequestLabels := strings.Join(event.PullRequestLabel, "\n")

	// Get event title based on trigger type
	eventTitle := event.PullRequestTitle
	if event.TriggerTarget == triggertype.Push {
		eventTitle = event.SHATitle
	}

	return map[string]string{
		"revision":            event.SHA,
		"repo_url":            repoURL,
		"repo_owner":          strings.ToLower(event.Organization),
		"repo_name":           strings.ToLower(event.Repository),
		"target_branch":       formatting.SanitizeBranch(event.BaseBranch),
		"source_branch":       formatting.SanitizeBranch(event.HeadBranch),
		"git_tag":             gitTag,
		"source_url":          event.HeadURL,
		"target_url":          event.BaseURL,
		"sender":              strings.ToLower(event.Sender),
		"target_namespace":    "",
		"event_type":          event.EventType,
		"event":               event.TriggerTarget.String(),
		"event_title":         eventTitle,
		"trigger_comment":     triggerComment,
		"pull_request_labels": pullRequestLabels,
	}
}

// detectProvider automatically detects the provider from headers and payload.
func detectProvider(headers map[string]string, body []byte) (string, error) {
	// Check for GitHub provider (most common)
	if getHeaderCaseInsensitive(headers, "X-GitHub-Event") != "" {
		// Forgejo sets X-Forgejo-Event-Type, X-Gitea-Event-Type, and X-GitHub-Event
		if getHeaderCaseInsensitive(headers, "X-Forgejo-Event-Type") != "" {
			return "forgejo", nil
		}
		// Gitea sets X-Gitea-Event-Type and X-GitHub-Event (but not X-Forgejo-Event-Type)
		if getHeaderCaseInsensitive(headers, "X-Gitea-Event-Type") != "" {
			return "gitea", nil
		}
		return "github", nil
	}

	// Check for GitLab provider
	if getHeaderCaseInsensitive(headers, "X-Gitlab-Event") != "" {
		return "gitlab", nil
	}

	// Check for Bitbucket Cloud (uses User-Agent header)
	if userAgent := getHeaderCaseInsensitive(headers, "User-Agent"); userAgent != "" {
		if strings.Contains(strings.ToLower(userAgent), "bitbucket") {
			// Try to distinguish between Cloud and Data Center by payload structure
			var payload map[string]any
			if json.Unmarshal(body, &payload) == nil {
				if actor, ok := payload["actor"].(map[string]any); ok {
					if _, hasAccountID := actor["account_id"]; hasAccountID {
						return "bitbucket-cloud", nil
					}
					// Heuristic: if it has an `id` but not an `account_id`, assume it's Data Center
					if _, hasID := actor["id"]; hasID {
						return "bitbucket-datacenter", nil
					}
				}
			}
			// Default to cloud if we can't determine
			return "bitbucket-cloud", nil
		}
	}

	// Check for Forgejo provider
	if getHeaderCaseInsensitive(headers, "X-Forgejo-Event-Type") != "" {
		return "forgejo", nil
	}

	// Check for Gitea provider
	if getHeaderCaseInsensitive(headers, "X-Gitea-Event-Type") != "" {
		return "gitea", nil
	}

	// Try to detect from payload structure as last resort
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		// GitHub-like structure
		if repository, ok := payload["repository"].(map[string]any); ok {
			if htmlURL, ok := repository["html_url"].(string); ok {
				if strings.Contains(htmlURL, "github.com") {
					return "github", nil
				}
				if strings.Contains(htmlURL, "gitlab.com") || strings.Contains(htmlURL, "gitlab") {
					return "gitlab", nil
				}
			}
		}

		// GitLab-specific structure
		if project, ok := payload["project"].(map[string]any); ok {
			if webURL, ok := project["web_url"].(string); ok {
				if strings.Contains(webURL, "gitlab") {
					return "gitlab", nil
				}
			}
		}

		// Bitbucket-specific structure
		if repository, ok := payload["repository"].(map[string]any); ok {
			if links, ok := repository["links"].(map[string]any); ok {
				if html, ok := links["html"].(map[string]any); ok {
					if href, ok := html["href"].(string); ok {
						if strings.Contains(href, "bitbucket") {
							return "bitbucket-cloud", nil
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("unable to detect provider from headers or payload")
}

func Command(ioStreams *cli.IOStreams) *cobra.Command {
	var bodyFile, headersFile, provider, githubToken string

	cmd := &cobra.Command{
		Use:   "cel",
		Short: "Evaluate CEL expressions interactively with webhook payloads",
		Long: `Evaluate CEL expressions interactively with webhook payloads.

The command automatically detects the git provider from the webhook headers and payload structure.
Supported providers: GitHub, GitLab, Bitbucket Cloud, Bitbucket Data Center, Gitea, and Forgejo.

You can provide webhook payload and headers from files to test CEL expressions
that would be used in PipelineRun configurations.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			body := map[string]any{}
			headers := map[string]string{}
			var bodyBytes []byte

			if bodyFile != "" {
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return err
				}
				bodyBytes = b
				if err := json.Unmarshal(b, &body); err != nil {
					return err
				}
			}

			if headersFile != "" {
				b, err := os.ReadFile(headersFile)
				if err != nil {
					return err
				}
				bs := bytes.TrimSpace(b)
				switch {
				case len(bs) > 0 && (bs[0] == '{' || bs[0] == '['):
					// JSON format headers
					if err := json.Unmarshal(bs, &headers); err != nil {
						return err
					}
				case isGosmeeScript(string(bs)):
					// Gosmee-generated shell script with curl commands
					h, err := parseGosmeeScript(string(bs))
					if err != nil {
						return err
					}
					headers = h
				default:
					// Plain HTTP headers format
					h, err := parseHTTPHeaders(string(bs))
					if err != nil {
						return err
					}
					headers = h
				}
			}
			// nolint:ineffassign,staticcheck
			pacParams := map[string]string{}
			// Auto-detect provider if not specified explicitly
			if provider == "auto" {
				detectedProvider, err := detectProvider(headers, bodyBytes)
				if err != nil {
					return fmt.Errorf("auto-detection failed: %w", err)
				}
				provider = detectedProvider
			}

			switch provider {
			case "github":
				var event *info.Event
				var err error
				if githubToken != "" {
					event, err = eventFromGitHubWithToken(bodyBytes, headers, githubToken)
				} else {
					event, err = eventFromGitHub(bodyBytes, headers)
				}
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			case "gitlab":
				event, err := eventFromGitLab(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			case "bitbucket-cloud":
				event, err := eventFromBitbucketCloud(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			case "bitbucket-datacenter":
				event, err := eventFromBitbucketDataCenter(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			case "gitea":
				event, err := eventFromGitea(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			case "forgejo":
				event, err := eventFromForgejo(bodyBytes, headers)
				if err != nil {
					return err
				}
				pacParams = pacParamsFromEvent(event)
			default:
				return fmt.Errorf("unsupported provider %s", provider)
			}

			// Display warning banner about CLI vs live payload differences
			printBanner(ioStreams, provider, bodyFile, headersFile)

			// Create files data structure (always empty in CLI mode)
			filesData := map[string]any{
				"all":      []string{},
				"added":    []string{},
				"deleted":  []string{},
				"modified": []string{},
				"renamed":  []string{},
			}

			// Check if stdin is a terminal (interactive mode) or pipe/file (non-interactive mode)
			if term.IsTerminal(int(os.Stdin.Fd())) {
				// Interactive mode: use go-prompt with autocompletion
				fmt.Fprintln(ioStreams.Out, "Type CEL expressions (Tab for completions, Ctrl+C to exit):")

				// Get history file path
				historyFile, err := getHistoryFilePath()
				if err != nil {
					return fmt.Errorf("failed to get history file path: %w", err)
				}

				// Setup readline instance
				rl, err := readline.NewEx(&readline.Config{
					Prompt:       "CEL> ",
					HistoryFile:  historyFile,
					AutoComplete: &celCompleter{completionFunc: getCompletions(pacParams, body, headers)},
				})
				if err != nil {
					return err
				}
				defer rl.Close()

				for {
					line, err := rl.Readline()
					if err != nil { // io.EOF, readline.ErrInterrupt
						break
					}
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					if line == "/help" {
						fmt.Fprintln(ioStreams.Out, formatHelpOutput(ioStreams, provider)+"\n")
						continue
					}
					if line == "/exit" {
						fmt.Fprintln(ioStreams.Out, "Bye bye, have a nice day :)")
						break
					}

					val, err := pkgcel.Value(line, body, headers, pacParams, filesData)
					if err != nil {
						fmt.Fprintln(ioStreams.Out, err)
					} else {
						fmt.Fprintf(ioStreams.Out, "%v\n", val)
					}
				}
			} else {
				// Non-interactive mode: read from stdin
				scanner := bufio.NewScanner(os.Stdin)
				hasInput := false
				for scanner.Scan() {
					hasInput = true
					expr := strings.TrimSpace(scanner.Text())
					if expr == "" {
						continue
					}

					val, err := pkgcel.Value(expr, body, headers, pacParams, filesData)
					if err != nil {
						fmt.Fprintln(ioStreams.Out, err)
					} else {
						fmt.Fprintf(ioStreams.Out, "%v\n", val)
					}
				}
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("error reading from stdin: %w", err)
				}
				// If no input was provided via stdin in non-interactive mode, just exit gracefully
				// This allows the command to work in test scenarios
				if !hasInput {
					// Exit gracefully without error - this is expected when testing
					return nil
				}
			}
			return nil
		},
		Annotations: map[string]string{"commandType": "main"},
	}

	cmd.Flags().StringVarP(&bodyFile, bodyFileFlag, "b", "", "path to JSON body file (required)")
	cmd.Flags().StringVarP(&headersFile, headersFileFlag, "H", "", "path to headers file (required, JSON, HTTP format, or gosmee-generated shell script)")
	cmd.Flags().StringVarP(&provider, providerFlag, "p", "auto", "payload provider (auto, github, gitlab, bitbucket-cloud, bitbucket-datacenter, gitea, forgejo)")
	cmd.Flags().StringVarP(&githubToken, githubTokenFlag, "t", "", "GitHub personal access token for API enrichment (enables full event processing)")
	// Mark body and headers flags as required.
	// These errors are safe to ignore as the flags are defined above.
	_ = cmd.MarkFlagRequired(bodyFileFlag)
	_ = cmd.MarkFlagRequired(headersFileFlag)
	return cmd
}

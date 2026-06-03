package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	port            = flag.Int("port", 8765, "Port to listen on")
	responseFile    = flag.String("response-file", "", "File containing canned responses")
	simulateLatency = flag.Int("latency-ms", 100, "Simulated latency in milliseconds")
	failureRate     = flag.Float64("failure-rate", 0, "Percentage of requests to fail (0-1)")
	verbose         = flag.Bool("verbose", false, "Enable verbose logging")
)

//go:embed responses.json
var defaultResponseFile string

// OpenAI request/response structures.
type openaiRequest struct {
	Model     string          `json:"model"`
	Messages  []openaiMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Gemini request/response structures.
type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// Canned responses structure.
type cannedResponses struct {
	Default    string            `json:"default"`
	ByKeyword  map[string]string `json:"by_keyword"`
	ByProvider map[string]string `json:"by_provider"`
}

var responses = cannedResponses{
	Default: `## Root Cause Analysis
The pipeline failed due to a test failure in the unit tests.

## Recommended Fix
1. Check the failing test logs for details
2. Verify the expected behavior matches the implementation
3. Update either the test or the implementation accordingly

## Prevention
- Add more comprehensive test coverage
- Set up pre-commit hooks to run tests locally`,
	ByKeyword:  make(map[string]string),
	ByProvider: make(map[string]string),
}

func main() {
	flag.Parse()

	var data []byte
	if *responseFile == "" {
		data = []byte(defaultResponseFile)
	} else {
		var err error
		if data, err = os.ReadFile(*responseFile); err != nil {
			log.Fatal("Failed to read response file:", err)
		}
	}

	// Load canned responses from file
	if err := loadResponses(data); err != nil {
		log.Printf("Using default responses")
	}

	// Setup HTTP handlers
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/v1/chat/completions", openaiHandler)
	http.HandleFunc("/v1beta/models/", geminiHandler)
	http.HandleFunc("/health", healthHandler)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🚀 Starting NoNo Fake AI Server")
	log.Printf("📡 Listening on: http://localhost%s", addr)
	log.Printf("🤖 OpenAI endpoint: http://localhost%s/v1/chat/completions", addr)
	log.Printf("🧠 Gemini endpoint: http://localhost%s/v1beta/models/{model}:generateContent", addr)
	log.Printf("⚙️  Latency: %dms, Failure rate: %.1f%%", *simulateLatency, *failureRate*100)

	server := &http.Server{
		Addr:         addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func loadResponses(data []byte) error {
	var loaded cannedResponses
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	// Merge loaded responses with defaults
	if loaded.Default != "" {
		responses.Default = loaded.Default
	}
	if loaded.ByKeyword != nil {
		responses.ByKeyword = loaded.ByKeyword
	}
	if loaded.ByProvider != nil {
		responses.ByProvider = loaded.ByProvider
	}

	log.Printf("✅ Loaded %d keyword responses from %s", len(responses.ByKeyword), *responseFile)
	return nil
}

func rootHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	errResp := map[string]any{
		"status":  "ok",
		"message": "NoNo Fake AI Server for Testing",
		"version": "1.0.0",
		"endpoints": map[string]string{
			"openai": "/v1/chat/completions",
			"gemini": "/v1beta/models/{model}:generateContent",
			"health": "/health",
		},
	}
	if err := json.NewEncoder(w).Encode(errResp); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	}); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func openaiHandler(w http.ResponseWriter, r *http.Request) {
	if *verbose {
		log.Printf("📨 OpenAI request from %q", r.RemoteAddr) //nolint:gosec // Test server logs request source for debugging.
	}

	// Check authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		errResp := openaiError{}
		errResp.Error.Message = "Missing authorization header"
		errResp.Error.Type = "invalid_request_error"
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Simulate latency
	if *simulateLatency > 0 {
		time.Sleep(time.Duration(*simulateLatency) * time.Millisecond)
	}

	// Simulate failures
	if shouldFail() {
		w.WriteHeader(http.StatusTooManyRequests)
		errResp := openaiError{}
		errResp.Error.Message = "Rate limit exceeded. Please try again later."
		errResp.Error.Type = "rate_limit_error"
		errResp.Error.Code = "rate_limit_exceeded"
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if *verbose {
			log.Printf("❌ Simulated rate limit error")
		}
		return
	}

	// Parse request
	var req openaiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		errResp := openaiError{}
		errResp.Error.Message = fmt.Sprintf("Invalid request body: %v", err)
		errResp.Error.Type = "invalid_request_error"
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		return
	}

	if *verbose {
		log.Printf("📝 Model: %s, Messages: %d", req.Model, len(req.Messages))
	}

	// Get appropriate response
	responseText := getResponse("openai", req.Messages)

	// Calculate token counts (simple word-based estimation)
	promptTokens := countTokens(req.Messages)
	completionTokens := countTokens([]openaiMessage{{Content: responseText}})

	// Build response
	response := openaiResponse{
		ID:      fmt.Sprintf("chatcmpl-fake-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []openaiChoice{
			{
				Index: 0,
				Message: openaiMessage{
					Role:    "assistant",
					Content: responseText,
				},
				FinishReason: "stop",
			},
		},
		Usage: openaiUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
	}

	if *verbose {
		log.Printf("✅ OpenAI response sent (%d tokens)", response.Usage.TotalTokens)
	}
}

func geminiHandler(w http.ResponseWriter, r *http.Request) {
	if *verbose {
		log.Printf("📨 Gemini request from %q", r.RemoteAddr) //nolint:gosec // Test server logs request source for debugging.
	}

	// Simulate latency
	if *simulateLatency > 0 {
		time.Sleep(time.Duration(*simulateLatency) * time.Millisecond)
	}

	// Simulate failures
	if shouldFail() {
		w.WriteHeader(http.StatusTooManyRequests)
		errResp := geminiError{}
		errResp.Error.Code = 429
		errResp.Error.Message = "Resource exhausted"
		errResp.Error.Status = "RESOURCE_EXHAUSTED"
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if *verbose {
			log.Printf("❌ Simulated resource exhausted error")
		}
		return
	}

	// Parse request
	var req geminiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		errResp := geminiError{}
		errResp.Error.Code = 400
		errResp.Error.Message = fmt.Sprintf("Invalid request body: %v", err)
		errResp.Error.Status = "INVALID_ARGUMENT"
		if err := json.NewEncoder(w).Encode(errResp); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		return
	}

	// Extract prompt from contents
	var prompt string
	if len(req.Contents) > 0 && len(req.Contents[0].Parts) > 0 {
		prompt = req.Contents[0].Parts[0].Text
	}

	if *verbose {
		log.Printf("📝 Prompt length: %d chars", len(prompt))
	}

	// Get appropriate response
	responseText := getResponse("gemini", []openaiMessage{{Content: prompt}})

	// Build response
	response := geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{Text: responseText},
					},
				},
				FinishReason: "STOP",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
	}

	if *verbose {
		log.Printf("✅ Gemini response sent")
	}
}

func getResponse(provider string, messages []openaiMessage) string {
	// Check provider-specific response first
	if resp, ok := responses.ByProvider[provider]; ok {
		return resp
	}

	// Check keyword-based responses
	if len(messages) > 0 {
		prompt := strings.ToLower(messages[0].Content)
		for keyword, response := range responses.ByKeyword {
			if strings.Contains(prompt, strings.ToLower(keyword)) {
				if *verbose {
					log.Printf("🎯 Matched keyword: %s", keyword)
				}
				return response
			}
		}
	}

	return responses.Default
}

func shouldFail() bool {
	if *failureRate <= 0 {
		return false
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		// Handle error appropriately, e.g., log or return default
		return false
	}
	return float64(n.Int64())/1000000.0 < *failureRate
}

func countTokens(messages []openaiMessage) int {
	total := 0
	for _, msg := range messages {
		// Simple word-based token estimation (roughly 1 token per word)
		total += len(strings.Fields(msg.Content))
	}
	return total
}

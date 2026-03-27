//go:build e2e

package test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/go-github/v84/github"
	"gotest.tools/v3/assert"
)

func TestOthersUnsupportedEvent(t *testing.T) {
	ctx := context.TODO()

	event := github.ReleaseEvent{}
	eventType := "release_event"

	jeez, err := json.Marshal(event)
	if err != nil {
		t.Fatal("failed to marshal event: ", err)
	}

	elURL := os.Getenv("TEST_EL_URL")
	if elURL == "" {
		t.Fatal("failed to find event listener url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, elURL, bytes.NewBuffer(jeez))
	if err != nil {
		t.Fatal("failed to build request : ", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("failed to send request : ", err)
	}
	assert.NilError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK, "%s reply expected 200 OK", elURL)
}

func TestOthersSkippedEvent(t *testing.T) {
	ctx := context.TODO()

	event := github.PullRequestEvent{
		Action: github.Ptr("closed"),
	}
	eventType := "pull_request"

	jeez, err := json.Marshal(event)
	if err != nil {
		t.Fatal("failed to marshal event: ", err)
	}

	elURL := os.Getenv("TEST_EL_URL")
	if elURL == "" {
		t.Fatal("failed to find event listener url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, elURL, bytes.NewBuffer(jeez))
	if err != nil {
		t.Fatal("failed to build request : ", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("failed to send request : ", err)
	}
	assert.NilError(t, err)
	defer resp.Body.Close()

	assert.Assert(t, resp.StatusCode >= 200 && resp.StatusCode < 300, "%s reply expected 2xx OK: %d", elURL, resp.StatusCode)
}

func TestOthersGETCall(t *testing.T) {
	ctx := context.TODO()

	elURL := os.Getenv("TEST_EL_URL")
	if elURL == "" {
		t.Fatal("failed to find event listener url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, elURL, nil)
	if err != nil {
		t.Fatal("failed to build request : ", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("failed to send request : ", err)
	}
	assert.NilError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK, "%s reply expected 200 OK", elURL)
}

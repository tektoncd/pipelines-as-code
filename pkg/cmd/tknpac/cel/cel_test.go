package cel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgcel "github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
)

const testPullRequestPayload = `{
  "action": "opened",
  "number": 1,
  "pull_request": {
    "id": 1,
    "number": 1,
    "title": "Test PR",
    "user": {
      "login": "testuser",
      "id": 1
    },
    "body": "This is a test PR",
    "head": {
      "label": "testuser:feature",
      "ref": "feature",
      "sha": "abc123",
      "repo": {
        "id": 1,
        "name": "test-repo",
        "full_name": "testuser/test-repo",
        "html_url": "https://github.com/testuser/test-repo"
      }
    },
    "base": {
      "label": "testorg:main",
      "ref": "main",
      "sha": "def456",
      "repo": {
        "id": 2,
        "name": "test-repo",
        "full_name": "testorg/test-repo",
        "html_url": "https://github.com/testorg/test-repo"
      }
    },
    "draft": false
  },
  "repository": {
    "id": 2,
    "name": "test-repo",
    "full_name": "testorg/test-repo",
    "owner": {
      "login": "testorg",
      "id": 2
    },
    "html_url": "https://github.com/testorg/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "testuser",
    "id": 1
  }
}`

// minimalGitHubPRPayload returns a minimal valid GitHub pull request webhook payload
// for testing purposes.
const minimalGitHubPRPayload = `{
  "action": "opened",
  "pull_request": {
    "number": 1,
    "title": "Test",
    "head": {
      "ref": "test-branch",
      "sha": "abc123",
      "repo": {
        "html_url": "https://github.com/test/test"
      }
    },
    "base": {
      "ref": "main",
      "repo": {
        "html_url": "https://github.com/test/test"
      }
    },
    "html_url": "https://github.com/test/test/pull/1",
    "labels": []
  },
  "repository": {
    "name": "test",
    "owner": {"login": "test"},
    "html_url": "https://github.com/test/test",
    "default_branch": "main"
  },
  "sender": {"login": "test"}
}`

func newIOStream() (*cli.IOStreams, *bytes.Buffer, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &cli.IOStreams{
		In:     io.NopCloser(in),
		Out:    out,
		ErrOut: errOut,
	}, out, errOut
}

// setupCELTest creates temporary files with the given body and headers content,
// executes the CEL command, and returns the error (if any).
func setupCELTest(t *testing.T, bodyContent, headersContent, provider string) error {
	t.Helper()

	tempDir := fs.NewDir(t, "cel-test")
	defer tempDir.Remove()

	// Create body file
	bodyFile := tempDir.Join("body.json")
	err := os.WriteFile(bodyFile, []byte(bodyContent), 0o600)
	assert.NilError(t, err)

	// Create headers file
	headersFile := tempDir.Join("headers.txt")
	err = os.WriteFile(headersFile, []byte(headersContent), 0o600)
	assert.NilError(t, err)

	ioStreams, _, _ := newIOStream()
	// Write empty input to stdin to exit immediately
	ioStreams.In = io.NopCloser(strings.NewReader("\n"))

	cmd := Command(ioStreams)
	cmd.SetArgs([]string{
		"--provider", provider,
		"--body", bodyFile,
		"--headers", headersFile,
	})

	return cmd.Execute()
}

func TestParseHTTPHeaders(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "valid headers",
			input: `Accept: */*
Content-Type: application/json
User-Agent: GitHub-Hookshot/2d5e4d4
X-GitHub-Event: pull_request`,
			want: map[string]string{
				"Accept":         "*/*",
				"Content-Type":   "application/json",
				"User-Agent":     "GitHub-Hookshot/2d5e4d4",
				"X-GitHub-Event": "pull_request",
			},
			wantErr: false,
		},
		{
			name: "headers with extra spaces",
			input: `   Accept   :   */*   
   Content-Type:application/json   `,
			want: map[string]string{
				"Accept":       "*/*",
				"Content-Type": "application/json",
			},
			wantErr: false,
		},
		{
			name:    "empty input",
			input:   "",
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name: "headers with empty lines",
			input: `Accept: */*

Content-Type: application/json

`,
			want: map[string]string{
				"Accept":       "*/*",
				"Content-Type": "application/json",
			},
			wantErr: false,
		},
		{
			name: "malformed header line ignored",
			input: `Accept: */*
malformed-line-without-colon
Content-Type: application/json`,
			want: map[string]string{
				"Accept":       "*/*",
				"Content-Type": "application/json",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHTTPHeaders(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestSplitCurlCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "simple curl command",
			input: `curl -X POST "http://localhost:8080"`,
			want:  []string{"curl", "-X", "POST", "http://localhost:8080"},
		},
		{
			name:  "curl with headers",
			input: `curl -H "Content-Type: application/json" -H "X-GitHub-Event: pull_request"`,
			want:  []string{"curl", "-H", "Content-Type: application/json", "-H", "X-GitHub-Event: pull_request"},
		},
		{
			name:  "curl with single quotes",
			input: `curl -H 'Content-Type: application/json' -H 'X-GitHub-Event: pull_request'`,
			want:  []string{"curl", "-H", "Content-Type: application/json", "-H", "X-GitHub-Event: pull_request"},
		},
		{
			name:  "complex curl command",
			input: `curl -sSi -H "Content-Type: application/json" -X POST -d @payload.json "http://localhost:8080"`,
			want:  []string{"curl", "-sSi", "-H", "Content-Type: application/json", "-X", "POST", "-d", "@payload.json", "http://localhost:8080"},
		},
		{
			name:    "unterminated quote",
			input:   `curl -H "Content-Type: application/json`,
			wantErr: true,
		},
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCurlCommand(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestParseCurlHeaders(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "simple curl with headers",
			input: `curl -H "Content-Type: application/json" -H "X-GitHub-Event: pull_request"`,
			want: map[string]string{
				"Content-Type":   "application/json",
				"X-GitHub-Event": "pull_request",
			},
		},
		{
			name:  "curl with mixed arguments",
			input: `curl -X POST -H "Content-Type: application/json" -d @payload.json -H "X-GitHub-Event: pull_request" "http://localhost:8080"`,
			want: map[string]string{
				"Content-Type":   "application/json",
				"X-GitHub-Event": "pull_request",
			},
		},
		{
			name:  "curl with no headers",
			input: `curl -X POST "http://localhost:8080"`,
			want:  map[string]string{},
		},
		{
			name:  "curl with malformed header",
			input: `curl -H "MalformedHeader" -H "Good-Header: value"`,
			want: map[string]string{
				"Good-Header": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCurlHeaders(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestIsGosmeeScript(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name: "typical gosmee script",
			input: `#!/usr/bin/env bash
set -euxfo pipefail
curl -sSi -H "Content-Type: application/json" -H "X-GitHub-Event: pull_request" -X POST -d @payload.json http://localhost:8080`,
			expect: true,
		},
		{
			name:   "simple curl command",
			input:  `curl -H "Content-Type: application/json" http://localhost:8080`,
			expect: true,
		},
		{
			name:   "curl without headers",
			input:  `curl http://localhost:8080`,
			expect: false,
		},
		{
			name: "plain text headers",
			input: `Content-Type: application/json
X-GitHub-Event: pull_request`,
			expect: false,
		},
		{
			name: "json headers",
			input: `{
  "Content-Type": "application/json",
  "X-GitHub-Event": "pull_request"
}`,
			expect: false,
		},
		{
			name:   "empty input",
			input:  "",
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGosmeeScript(tt.input)
			assert.Equal(t, got, tt.expect)
		})
	}
}

func TestParseGosmeeScript(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "typical gosmee script",
			input: `#!/usr/bin/env bash
# Copyright 2023 Chmouel Boudjnah <chmouel@chmouel.com>
set -euxfo pipefail

curl -sSi -H "Content-Type: application/json" -H "X-GitHub-Event: pull_request" -H "User-Agent: GitHub-Hookshot/2d5e4d4" -X POST -d @payload.json http://localhost:8080`,
			want: map[string]string{
				"Content-Type":   "application/json",
				"X-GitHub-Event": "pull_request",
				"User-Agent":     "GitHub-Hookshot/2d5e4d4",
			},
		},
		{
			name: "real-world example from gosmee",
			input: `#!/usr/bin/env bash
# Replay script with headers and JSON payload to the target controller.
set -euxfo pipefail
cd $(dirname $(readlink -f $0))

curl -sSi -H "Content-Type: application/json" -H 'X-Forwarded-Proto: https' -H 'Accept-Encoding: gzip' -H 'Content-Length: 17' -H 'X-Forwarded-Host: hook.pipelinesascode.com' -H 'Accept: */*' -X POST -d @./payload.json ${targetURL}`,
			want: map[string]string{
				"Content-Type":      "application/json",
				"X-Forwarded-Proto": "https",
				"Accept-Encoding":   "gzip",
				"Content-Length":    "17",
				"X-Forwarded-Host":  "hook.pipelinesascode.com",
				"Accept":            "*/*",
			},
		},
		{
			name: "script without curl commands",
			input: `#!/bin/bash
echo "No curl commands here"
exit 0`,
			wantErr: true,
		},
		{
			name: "curl without headers",
			input: `#!/bin/bash
curl http://localhost:8080`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGosmeeScript(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestEventFromGitHub(t *testing.T) {
	pullRequestPayload := `{
  "action": "synchronize",
  "number": 1234,
  "pull_request": {
    "title": "fix something somewhere out there",
    "number": 1234,
    "user": {
      "login": "pachito"
    },
    "head": {
      "ref": "parsepayload",
      "sha": "178fd7ac8826595cffaa23e574eb0c02c3e76dcf",
      "repo": {
        "html_url": "https://github.com/pachito/pipelines-as-code"
      }
    },
    "base": {
      "ref": "main",
      "repo": {
        "html_url": "https://github.com/openshift-pipelines/pipelines-as-code"
      }
    },
    "labels": []
  },
  "repository": {
    "name": "pipelines-as-code",
    "full_name": "openshift-pipelines/pipelines-as-code",
    "owner": {
      "login": "openshift-pipelines"
    },
    "html_url": "https://github.com/openshift-pipelines/pipelines-as-code",
    "default_branch": "main"
  },
  "sender": {
    "login": "pachito"
  }
}`

	pushPayload := `{
  "ref": "refs/heads/main",
  "after": "123abc456def",
  "head_commit": {
    "id": "123abc456def",
    "message": "Update README",
    "url": "https://github.com/owner/repo/commit/123abc456def"
  },
  "repository": {
    "name": "test-repo",
    "owner": {
      "login": "test-owner"
    },
    "html_url": "https://github.com/test-owner/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "test-user"
  }
}`

	tests := []struct {
		name    string
		body    []byte
		headers map[string]string
		wantErr bool
		checks  func(t *testing.T, event *info.Event)
	}{
		{
			name: "pull request event",
			body: []byte(pullRequestPayload),
			headers: map[string]string{
				"X-GitHub-Event": "pull_request",
			},
			wantErr: false,
			checks: func(t *testing.T, event *info.Event) {
				assert.Equal(t, event.EventType, "pull_request")
				assert.Equal(t, event.Organization, "openshift-pipelines")
				assert.Equal(t, event.Repository, "pipelines-as-code")
				assert.Equal(t, event.BaseBranch, "main")
				assert.Equal(t, event.HeadBranch, "parsepayload")
				assert.Equal(t, event.Sender, "pachito")
				assert.Equal(t, event.PullRequestTitle, "fix something somewhere out there")
				assert.Equal(t, event.PullRequestNumber, 1234)
				assert.Equal(t, event.SHA, "178fd7ac8826595cffaa23e574eb0c02c3e76dcf")
			},
		},
		{
			name: "push event",
			body: []byte(pushPayload),
			headers: map[string]string{
				"X-GitHub-Event": "push",
			},
			wantErr: false,
			checks: func(t *testing.T, event *info.Event) {
				assert.Equal(t, event.EventType, "push")
				assert.Equal(t, event.Organization, "test-owner")
				assert.Equal(t, event.Repository, "test-repo")
				assert.Equal(t, event.BaseBranch, "refs/heads/main")
				assert.Equal(t, event.HeadBranch, "refs/heads/main")
				assert.Equal(t, event.Sender, "test-user")
				assert.Equal(t, event.SHATitle, "Update README")
				assert.Equal(t, event.SHA, "123abc456def")
			},
		},
		{
			name: "invalid json",
			body: []byte(`{"invalid": json}`),
			headers: map[string]string{
				"X-GitHub-Event": "pull_request",
			},
			wantErr: true,
		},
		{
			name: "unsupported event type",
			body: []byte(`{"action": "test"}`),
			headers: map[string]string{
				"X-GitHub-Event": "unsupported_event",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := eventFromGitHub(tt.body, tt.headers)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, event != nil)
			if tt.checks != nil {
				tt.checks(t, event)
			}
		})
	}
}

func TestEventFromGitHubMoreEventTypes(t *testing.T) {
	issueCommentPayload := `{
  "action": "created",
  "issue": {
    "number": 123,
    "pull_request": {
      "html_url": "https://github.com/owner/repo/pull/123"
    }
  },
  "comment": {
    "body": "/ok-to-test"
  },
  "repository": {
    "name": "test-repo",
    "owner": {
      "login": "test-owner"
    },
    "html_url": "https://github.com/test-owner/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "test-user"
  }
}`

	commitCommentPayload := `{
  "action": "created",
  "comment": {
    "commit_id": "abc123def456",
    "body": "/retest",
    "html_url": "https://github.com/owner/repo/commit/abc123def456#comment"
  },
  "repository": {
    "name": "test-repo",
    "owner": {
      "login": "test-owner"
    },
    "html_url": "https://github.com/test-owner/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "test-user"
  }
}`

	tests := []struct {
		name    string
		body    []byte
		headers map[string]string
		wantErr bool
		checks  func(t *testing.T, event *info.Event)
	}{
		{
			name: "issue comment event",
			body: []byte(issueCommentPayload),
			headers: map[string]string{
				"X-GitHub-Event": "issue_comment",
			},
			wantErr: false,
			checks: func(t *testing.T, event *info.Event) {
				assert.Equal(t, event.EventType, "issue_comment")
				assert.Equal(t, event.Organization, "test-owner")
				assert.Equal(t, event.Repository, "test-repo")
				assert.Equal(t, event.Sender, "test-user")
				assert.Equal(t, event.TriggerComment, "/ok-to-test")
				assert.Equal(t, event.PullRequestNumber, 123)
				assert.Equal(t, event.TriggerTarget, triggertype.PullRequest)
			},
		},
		{
			name: "commit comment event",
			body: []byte(commitCommentPayload),
			headers: map[string]string{
				"X-GitHub-Event": "commit_comment",
			},
			wantErr: false,
			checks: func(t *testing.T, event *info.Event) {
				assert.Equal(t, event.EventType, "commit_comment")
				assert.Equal(t, event.Organization, "test-owner")
				assert.Equal(t, event.Repository, "test-repo")
				assert.Equal(t, event.Sender, "test-user")
				assert.Equal(t, event.TriggerComment, "/retest")
				assert.Equal(t, event.SHA, "abc123def456")
				assert.Equal(t, event.TriggerTarget, triggertype.Push)
			},
		},
		{
			name: "missing X-GitHub-Event header",
			body: []byte(`{"action": "test"}`),
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := eventFromGitHub(tt.body, tt.headers)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, event != nil)
			if tt.checks != nil {
				tt.checks(t, event)
			}
		})
	}
}

// TestEventFromGitHubWithProvider tests the enhanced GitHub event processing
// that uses API calls to enrich events when a token is provided.
func TestEventFromGitHubWithProvider(t *testing.T) {
	// This is a mock test since we don't want to make real API calls in unit tests.
	// We test the fallback behavior when no token is provided and ensure
	// the enhanced function doesn't break existing functionality.

	issueCommentPayload := `{
  "action": "created",
  "issue": {
    "number": 123,
    "pull_request": {
      "html_url": "https://github.com/test-owner/test-repo/pull/123"
    }
  },
  "comment": {
    "body": "/ok-to-test"
  },
  "repository": {
    "name": "test-repo",
    "owner": {
      "login": "test-owner"
    },
    "html_url": "https://github.com/test-owner/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "test-user"
  }
}`

	headers := map[string]string{
		"X-GitHub-Event": "issue_comment",
	}

	t.Run("fallback to basic parsing when no token provided", func(t *testing.T) {
		// Test with empty token (should fallback to basic parsing)
		event, err := eventFromGitHubWithToken([]byte(issueCommentPayload), headers, "")
		assert.NilError(t, err)
		assert.Assert(t, event != nil)
		assert.Equal(t, event.EventType, "issue_comment")
		assert.Equal(t, event.Organization, "test-owner")
		assert.Equal(t, event.Repository, "test-repo")
		assert.Equal(t, event.PullRequestNumber, 123)

		// With basic parsing, these fields should not be populated for issue_comment
		assert.Equal(t, event.SHA, "")
		assert.Equal(t, event.HeadBranch, "")
		assert.Equal(t, event.BaseBranch, "")
	})

	t.Run("handles invalid token gracefully", func(t *testing.T) {
		// Test with invalid token (should fallback to basic parsing)
		event, err := eventFromGitHubWithToken([]byte(issueCommentPayload), headers, "invalid-token")
		assert.NilError(t, err) // Should not fail, just fallback
		assert.Assert(t, event != nil)
		assert.Equal(t, event.EventType, "issue_comment")
		assert.Equal(t, event.Organization, "test-owner")
		assert.Equal(t, event.Repository, "test-repo")
	})

	t.Run("handles pull request events without token", func(t *testing.T) {
		// For PR events, both basic and enhanced parsing should work the same
		prPayload := `{
  "action": "opened",
  "pull_request": {
    "number": 123,
    "title": "Test PR",
    "head": {
      "sha": "abc123",
      "ref": "feature-branch"
    },
    "base": {
      "ref": "main"
    },
    "user": {
      "login": "test-user"
    }
  },
  "repository": {
    "name": "test-repo",
    "owner": {
      "login": "test-owner"
    },
    "html_url": "https://github.com/test-owner/test-repo",
    "default_branch": "main"
  }
}`
		prHeaders := map[string]string{
			"X-GitHub-Event": "pull_request",
		}

		event, err := eventFromGitHubWithToken([]byte(prPayload), prHeaders, "")
		assert.NilError(t, err)
		assert.Assert(t, event != nil)
		assert.Equal(t, event.EventType, "pull_request")
		assert.Equal(t, event.SHA, "abc123")
		assert.Equal(t, event.HeadBranch, "feature-branch")
		assert.Equal(t, event.BaseBranch, "main")
		assert.Equal(t, event.PullRequestTitle, "Test PR")
	})
}

func TestPacParamsFromEvent(t *testing.T) {
	tests := []struct {
		name  string
		event *info.Event
		want  map[string]string
	}{
		{
			name: "pull request event",
			event: &info.Event{
				EventType:        "pull_request",
				Organization:     "OpenShift-Pipelines",
				Repository:       "Pipelines-As-Code",
				BaseBranch:       "main",
				HeadBranch:       "feature-branch",
				Sender:           "TestUser",
				SHA:              "abc123",
				URL:              "https://github.com/openshift-pipelines/pipelines-as-code",
				BaseURL:          "https://github.com/openshift-pipelines/pipelines-as-code",
				HeadURL:          "https://github.com/user/pipelines-as-code",
				PullRequestTitle: "Add new feature",
				TriggerTarget:    triggertype.PullRequest,
				TriggerComment:   "test comment\nwith newlines",
				PullRequestLabel: []string{"bug", "enhancement"},
			},
			want: map[string]string{
				"revision":            "abc123",
				"repo_url":            "https://github.com/openshift-pipelines/pipelines-as-code",
				"repo_owner":          "openshift-pipelines",
				"repo_name":           "pipelines-as-code",
				"target_branch":       "main",
				"source_branch":       "feature-branch",
				"git_tag":             "",
				"source_url":          "https://github.com/user/pipelines-as-code",
				"target_url":          "https://github.com/openshift-pipelines/pipelines-as-code",
				"sender":              "testuser",
				"target_namespace":    "",
				"event_type":          "pull_request",
				"event":               "pull_request",
				"event_title":         "Add new feature",
				"trigger_comment":     "test comment\\nwith newlines",
				"pull_request_labels": "bug\nenhancement",
			},
		},
		{
			name: "push event with tag",
			event: &info.Event{
				EventType:     "push",
				Organization:  "test-org",
				Repository:    "test-repo",
				BaseBranch:    "refs/tags/v1.0.0",
				HeadBranch:    "refs/tags/v1.0.0",
				Sender:        "test-user",
				SHA:           "def456",
				URL:           "https://github.com/test-org/test-repo",
				BaseURL:       "https://github.com/test-org/test-repo",
				HeadURL:       "https://github.com/test-org/test-repo",
				SHATitle:      "Release v1.0.0",
				TriggerTarget: triggertype.Push,
			},
			want: map[string]string{
				"revision":            "def456",
				"repo_url":            "https://github.com/test-org/test-repo",
				"repo_owner":          "test-org",
				"repo_name":           "test-repo",
				"target_branch":       "refs/tags/v1.0.0",
				"source_branch":       "refs/tags/v1.0.0",
				"git_tag":             "v1.0.0",
				"source_url":          "https://github.com/test-org/test-repo",
				"target_url":          "https://github.com/test-org/test-repo",
				"sender":              "test-user",
				"target_namespace":    "",
				"event_type":          "push",
				"event":               "push",
				"event_title":         "Release v1.0.0",
				"trigger_comment":     "",
				"pull_request_labels": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pacParamsFromEvent(tt.event)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestPacParamsFromEventEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		event *info.Event
		want  map[string]string
	}{
		{
			name: "event with clone URL preference",
			event: &info.Event{
				EventType:     "push",
				Organization:  "test-org",
				Repository:    "test-repo",
				URL:           "https://github.com/test-org/test-repo",
				CloneURL:      "git@github.com:test-org/test-repo.git",
				TriggerTarget: triggertype.Push,
			},
			want: map[string]string{
				"revision":            "",
				"repo_url":            "git@github.com:test-org/test-repo.git", // CloneURL takes precedence
				"repo_owner":          "test-org",
				"repo_name":           "test-repo",
				"target_branch":       "",
				"source_branch":       "",
				"git_tag":             "",
				"source_url":          "",
				"target_url":          "",
				"sender":              "",
				"target_namespace":    "",
				"event_type":          "push",
				"event":               "push",
				"event_title":         "",
				"trigger_comment":     "",
				"pull_request_labels": "",
			},
		},
		{
			name: "event with carriage return in comment",
			event: &info.Event{
				TriggerTarget:  triggertype.PullRequest,
				TriggerComment: "line1\r\nline2\nline3",
			},
			want: map[string]string{
				"revision":            "",
				"repo_url":            "",
				"repo_owner":          "",
				"repo_name":           "",
				"target_branch":       "",
				"source_branch":       "",
				"git_tag":             "",
				"source_url":          "",
				"target_url":          "",
				"sender":              "",
				"target_namespace":    "",
				"event_type":          "",
				"event":               "pull_request",
				"event_title":         "",
				"trigger_comment":     "line1\\nline2\\nline3", // newlines escaped
				"pull_request_labels": "",
			},
		},
		{
			name: "empty event",
			event: &info.Event{
				TriggerTarget: triggertype.Push,
			},
			want: map[string]string{
				"revision":            "",
				"repo_url":            "",
				"repo_owner":          "",
				"repo_name":           "",
				"target_branch":       "",
				"source_branch":       "",
				"git_tag":             "",
				"source_url":          "",
				"target_url":          "",
				"sender":              "",
				"target_namespace":    "",
				"event_type":          "",
				"event":               "push",
				"event_title":         "",
				"trigger_comment":     "",
				"pull_request_labels": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pacParamsFromEvent(tt.event)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestCommandExecution(t *testing.T) {
	pullRequestPayload := `{
  "action": "synchronize",
  "number": 1234,
  "pull_request": {
    "title": "fix something somewhere out there",
    "number": 1234,
    "user": {
      "login": "pachito"
    },
    "head": {
      "ref": "parsepayload",
      "sha": "178fd7ac8826595cffaa23e574eb0c02c3e76dcf",
      "repo": {
        "html_url": "https://github.com/pachito/pipelines-as-code"
      }
    },
    "base": {
      "ref": "main",
      "repo": {
        "html_url": "https://github.com/openshift-pipelines/pipelines-as-code"
      }
    },
    "labels": [],
    "draft": false
  },
  "repository": {
    "name": "pipelines-as-code",
    "full_name": "openshift-pipelines/pipelines-as-code",
    "owner": {
      "login": "openshift-pipelines"
    },
    "html_url": "https://github.com/openshift-pipelines/pipelines-as-code",
    "default_branch": "main"
  },
  "sender": {
    "login": "pachito"
  }
}`

	headers := `Accept: */*
Content-Type: application/json
User-Agent: GitHub-Hookshot/2d5e4d4
X-GitHub-Event: pull_request`

	tests := []struct {
		name            string
		bodyContent     string
		headersContent  string
		provider        string
		wantErr         bool
		wantErrContains string
		wantOutContains []string
	}{
		{
			name:           "valid pull request payload",
			bodyContent:    pullRequestPayload,
			headersContent: headers,
			provider:       "github",
			wantErr:        false,
			wantOutContains: []string{
				"Important Notice",
				"Provider: github",
			},
		},
		{
			name:            "no files provided",
			provider:        "github",
			wantErr:         true,
			wantErrContains: "required flag(s) \"body\", \"headers\" not set",
		},
		{
			name:            "no body file",
			headersContent:  headers,
			provider:        "github",
			wantErr:         true,
			wantErrContains: "required flag(s) \"body\" not set",
		},
		{
			name:            "unsupported provider",
			bodyContent:     pullRequestPayload,
			headersContent:  headers,
			provider:        "invalid-provider",
			wantErr:         true,
			wantErrContains: "unsupported provider invalid-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := fs.NewDir(t, "cel-test")
			defer tempDir.Remove()

			var bodyFile, headersFile string

			if tt.bodyContent != "" {
				bodyFile = tempDir.Join("payload.json")
				err := os.WriteFile(bodyFile, []byte(tt.bodyContent), 0o600)
				assert.NilError(t, err)
			}

			if tt.headersContent != "" {
				headersFile = tempDir.Join("headers.txt")
				err := os.WriteFile(headersFile, []byte(tt.headersContent), 0o600)
				assert.NilError(t, err)
			}

			ioStreams, out, errOut := newIOStream()

			// Write empty input to stdin to exit the interactive loop immediately
			ioStreams.In = io.NopCloser(strings.NewReader("\n"))

			cmd := Command(ioStreams)
			cmd.SetArgs([]string{
				"--provider", tt.provider,
			})

			if bodyFile != "" {
				if err := cmd.Flags().Set("body", bodyFile); err != nil {
					t.Fatalf("failed to set body flag: %v", err)
				}
			}
			if headersFile != "" {
				if err := cmd.Flags().Set("headers", headersFile); err != nil {
					t.Fatalf("failed to set headers flag: %v", err)
				}
			}

			err := cmd.Execute()

			if tt.wantErr {
				assert.Assert(t, err != nil)
				if tt.wantErrContains != "" {
					assert.Assert(t, strings.Contains(err.Error(), tt.wantErrContains),
						"error %q should contain %q", err.Error(), tt.wantErrContains)
				}
				// For EOF error (successful processing but empty input), check output still
				if tt.wantErrContains == "EOF" && len(tt.wantOutContains) > 0 {
					outStr := out.String()
					errStr := errOut.String()
					for _, want := range tt.wantOutContains {
						assert.Assert(t, strings.Contains(outStr, want) || strings.Contains(errStr, want),
							"output should contain %q, got out: %q, err: %q", want, outStr, errStr)
					}
				}
				return
			}

			assert.NilError(t, err)

			outStr := out.String()
			errStr := errOut.String()

			for _, want := range tt.wantOutContains {
				assert.Assert(t, strings.Contains(outStr, want) || strings.Contains(errStr, want),
					"output should contain %q, got out: %q, err: %q", want, outStr, errStr)
			}
		})
	}
}

func TestHeaderFormats(t *testing.T) {
	tests := []struct {
		name            string
		bodyContent     string
		headersContent  string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:        "plain text headers",
			bodyContent: minimalGitHubPRPayload,
			headersContent: `Accept: */*
Content-Type: application/json
X-GitHub-Event: pull_request`,
			wantErr: false,
		},
		{
			name:        "json headers",
			bodyContent: minimalGitHubPRPayload,
			headersContent: `{
  "Accept": "*/*",
  "Content-Type": "application/json",
  "X-GitHub-Event": "pull_request"
}`,
			wantErr: false,
		},
		{
			name:            "invalid json headers",
			bodyContent:     minimalGitHubPRPayload,
			headersContent:  `{"invalid": json}`,
			wantErr:         true,
			wantErrContains: "invalid character",
		},
		{
			name:            "empty headers file",
			bodyContent:     minimalGitHubPRPayload,
			headersContent:  "",
			wantErr:         true,
			wantErrContains: "unknown X-Github-Event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setupCELTest(t, tt.bodyContent, tt.headersContent, "github")

			if tt.wantErr {
				assert.Assert(t, err != nil)
				if tt.wantErrContains != "" {
					assert.Assert(t, strings.Contains(err.Error(), tt.wantErrContains),
						"error %q should contain %q", err.Error(), tt.wantErrContains)
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestCommandFlags(t *testing.T) {
	ioStreams, _, _ := newIOStream()
	cmd := Command(ioStreams)

	// Test that flags are properly defined
	bodyFlag := cmd.Flags().Lookup("body")
	assert.Assert(t, bodyFlag != nil)
	assert.Equal(t, bodyFlag.Shorthand, "b")
	assert.Equal(t, bodyFlag.Usage, "path to JSON body file (required)")

	headersFlag := cmd.Flags().Lookup("headers")
	assert.Assert(t, headersFlag != nil)
	assert.Equal(t, headersFlag.Shorthand, "H")
	assert.Equal(t, headersFlag.Usage, "path to headers file (required, JSON, HTTP format, or gosmee-generated shell script)")

	providerFlag := cmd.Flags().Lookup("provider")
	assert.Assert(t, providerFlag != nil)
	assert.Equal(t, providerFlag.Shorthand, "p")
	assert.Equal(t, providerFlag.Usage, "payload provider (auto, github, gitlab, bitbucket-cloud, bitbucket-datacenter, gitea, forgejo)")
	assert.Equal(t, providerFlag.DefValue, "auto")
}

func TestInvalidFiles(t *testing.T) {
	tests := []struct {
		name            string
		createFile      bool
		fileContent     string
		fileFlag        string
		wantErrContains string
	}{
		{
			name:            "non-existent body file",
			createFile:      false,
			fileFlag:        "body",
			wantErrContains: "no such file or directory",
		},
		{
			name:            "non-existent headers file",
			createFile:      false,
			fileFlag:        "headers",
			wantErrContains: "no such file or directory",
		},
		{
			name:            "invalid json in body file",
			createFile:      true,
			fileContent:     `{"invalid": json}`,
			fileFlag:        "body",
			wantErrContains: "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := fs.NewDir(t, "cel-invalid-test")
			defer tempDir.Remove()

			// Create valid body file (unless we're testing body file errors)
			validBodyFile := tempDir.Join("valid-body.json")
			err := os.WriteFile(validBodyFile, []byte(minimalGitHubPRPayload), 0o600)
			assert.NilError(t, err)

			// Create valid headers file (unless we're testing headers file errors)
			validHeadersFile := tempDir.Join("valid-headers.txt")
			validHeadersContent := `X-GitHub-Event: pull_request
Content-Type: application/json`
			err = os.WriteFile(validHeadersFile, []byte(validHeadersContent), 0o600)
			assert.NilError(t, err)

			var filePath string
			if tt.createFile {
				filePath = tempDir.Join("test-file")
				err = os.WriteFile(filePath, []byte(tt.fileContent), 0o600)
				assert.NilError(t, err)
			} else {
				filePath = filepath.Join(tempDir.Path(), "non-existent-file")
			}

			ioStreams, _, _ := newIOStream()
			ioStreams.In = io.NopCloser(strings.NewReader("\n"))

			cmd := Command(ioStreams)
			args := []string{"--provider", "github"}

			// Add both body and headers flags, using the invalid one for the test target
			if tt.fileFlag == "body" {
				args = append(args, "--body", filePath)
				args = append(args, "--headers", validHeadersFile)
			} else {
				args = append(args, "--body", validBodyFile)
				args = append(args, "--headers", filePath)
			}

			cmd.SetArgs(args)

			err = cmd.Execute()
			assert.Assert(t, err != nil)
			assert.Assert(t, strings.Contains(err.Error(), tt.wantErrContains),
				"error %q should contain %q", err.Error(), tt.wantErrContains)
		})
	}
}

// testCommandHelper runs the CEL command with given body and headers content and verifies basic output.
func testCommandHelper(t *testing.T, testName, bodyContent, headersContent string, filePerms os.FileMode) {
	t.Helper()

	tempDir := fs.NewDir(t, testName)
	defer tempDir.Remove()

	// Create body file
	bodyFile := tempDir.Join("payload.json")
	err := os.WriteFile(bodyFile, []byte(bodyContent), 0o600)
	assert.NilError(t, err)

	// Create headers file
	headersFile := tempDir.Join("headers")
	err = os.WriteFile(headersFile, []byte(headersContent), filePerms)
	assert.NilError(t, err)

	ioStreams, out, _ := newIOStream()
	// Write empty input to stdin to exit the interactive loop immediately
	ioStreams.In = io.NopCloser(strings.NewReader("\n"))

	cmd := Command(ioStreams)
	cmd.SetArgs([]string{
		"--provider", "github",
		"--body", bodyFile,
		"--headers", headersFile,
	})

	err = cmd.Execute()
	// Interactive mode should exit gracefully on EOF
	assert.NilError(t, err)

	outStr := out.String()
	// Verify the banner was printed
	assert.Assert(t, strings.Contains(outStr, "Important Notice"))
	assert.Assert(t, strings.Contains(outStr, "Provider: github"))
}

func TestCommandWithGosmeeScript(t *testing.T) {
	// Gosmee script content
	gosmeeScript := `#!/usr/bin/env bash
# Copyright 2023 Chmouel Boudjnah <chmouel@chmouel.com>
# Replay script with headers and JSON payload to the target controller.
#
set -euxfo pipefail
cd $(dirname $(readlink -f $0))

targetURL="http://localhost:8082"
if [[ ${1:-""} == -l ]]; then
  targetURL="http://localhost:8082"
elif [[ -n ${1:-""} ]]; then
  targetURL=${1}
elif [[ -n ${GOSMEE_DEBUG_SERVICE:-""} ]]; then
  targetURL=${GOSMEE_DEBUG_SERVICE}
fi

curl -sSi -H "Content-Type: application/json" -H 'X-Forwarded-Proto: https' -H 'Accept-Encoding: gzip' -H 'Content-Length: 17' -H 'User-Agent: curl/8.7.1' -H 'X-Forwarded-Host: hook.pipelinesascode.com' -H 'X-Forwarded-For: 82.66.174.128' -H 'Accept: */*' -H 'X-GitHub-Event: pull_request' -H 'Via: 2.0 Caddy' -X POST -d @./payload.json ${targetURL}`

	testCommandHelper(t, "cel-gosmee-test", testPullRequestPayload, gosmeeScript, 0o700)
}

func TestCommandWithRealWorldPayloads(t *testing.T) {
	realHeaders := `Accept: */*
Accept-Encoding: gzip, deflate, br
Content-Type: application/json
User-Agent: GitHub-Hookshot/044aadd
X-GitHub-Delivery: 12345678-1234-1234-1234-123456789012
X-GitHub-Event: pull_request
X-GitHub-Hook-ID: 123456789
X-GitHub-Hook-Installation-Target-ID: 987654321
X-GitHub-Hook-Installation-Target-Type: repository`

	testCommandHelper(t, "cel-realworld-test", testPullRequestPayload, realHeaders, 0o600)
}

func TestCELExpressionEvaluation(t *testing.T) {
	// This test simulates the CEL evaluation by using the same components
	// the command uses, but without the interactive prompt
	pullRequestPayload := `{
  "action": "synchronize",
  "number": 1234,
  "pull_request": {
    "title": "fix something somewhere out there",
    "number": 1234,
    "user": {
      "login": "pachito"
    },
    "head": {
      "ref": "parsepayload",
      "sha": "178fd7ac8826595cffaa23e574eb0c02c3e76dcf",
      "repo": {
        "html_url": "https://github.com/pachito/pipelines-as-code"
      }
    },
    "base": {
      "ref": "main",
      "repo": {
        "html_url": "https://github.com/openshift-pipelines/pipelines-as-code"
      }
    },
    "labels": [],
    "draft": false
  },
  "repository": {
    "name": "pipelines-as-code",
    "full_name": "openshift-pipelines/pipelines-as-code",
    "owner": {
      "login": "openshift-pipelines"
    },
    "html_url": "https://github.com/openshift-pipelines/pipelines-as-code",
    "default_branch": "main"
  },
  "sender": {
    "login": "pachito"
  }
}`

	headers := map[string]string{
		"X-GitHub-Event": "pull_request",
		"Content-Type":   "application/json",
	}

	// Parse the event
	event, err := eventFromGitHub([]byte(pullRequestPayload), headers)
	assert.NilError(t, err)

	// Generate PAC parameters
	pacParams := pacParamsFromEvent(event)

	// Parse the body for CEL access
	var body map[string]any
	err = json.Unmarshal([]byte(pullRequestPayload), &body)
	assert.NilError(t, err)

	// Create files data structure (empty for CLI)
	filesData := map[string]any{
		"all":      []string{},
		"added":    []string{},
		"deleted":  []string{},
		"modified": []string{},
		"renamed":  []string{},
	}

	// Test various CEL expressions that would be commonly used
	tests := []struct {
		name       string
		expression string
		wantResult string // Using string to represent the expected result
		wantErr    bool
	}{
		{
			name:       "pac event type",
			expression: `pac.event == "pull_request"`,
			wantResult: "true",
		},
		{
			name:       "pac target branch",
			expression: `pac.target_branch == "main"`,
			wantResult: "true",
		},
		{
			name:       "pac sender",
			expression: `pac.sender == "pachito"`,
			wantResult: "true",
		},
		{
			name:       "body action",
			expression: `body.action == "synchronize"`,
			wantResult: "true",
		},
		{
			name:       "body PR number",
			expression: `body.number == 1234`,
			wantResult: "true",
		},
		{
			name:       "body PR not draft",
			expression: `!body.pull_request.draft`,
			wantResult: "true",
		},
		{
			name:       "headers check",
			expression: `headers['X-GitHub-Event'] == "pull_request"`,
			wantResult: "true",
		},
		{
			name:       "complex expression",
			expression: `pac.event == "pull_request" && pac.target_branch == "main" && body.action == "synchronize"`,
			wantResult: "true",
		},
		{
			name:       "false condition",
			expression: `pac.target_branch == "develop"`,
			wantResult: "false",
		},
		{
			name:       "invalid expression",
			expression: `invalid.syntax...`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pkgcel.Value(tt.expression, body, headers, pacParams, filesData)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			resultStr := fmt.Sprintf("%v", result)
			assert.Equal(t, resultStr, tt.wantResult)
		})
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name             string
		headers          map[string]string
		body             []byte
		expectedProvider string
		expectedError    bool
	}{
		{
			name: "GitHub provider",
			headers: map[string]string{
				"X-GitHub-Event": "pull_request",
			},
			body:             []byte(`{"repository": {"html_url": "https://github.com/owner/repo"}}`),
			expectedProvider: "github",
		},
		{
			name: "Gitea provider",
			headers: map[string]string{
				"X-GitHub-Event":     "pull_request",
				"X-Gitea-Event-Type": "pull_request",
			},
			body:             []byte(`{"repository": {"html_url": "https://gitea.com/owner/repo"}}`),
			expectedProvider: "gitea",
		},
		{
			name: "Forgejo provider with all headers",
			headers: map[string]string{
				"X-GitHub-Event":       "pull_request",
				"X-Gitea-Event-Type":   "pull_request",
				"X-Forgejo-Event-Type": "pull_request",
			},
			body:             []byte(`{"repository": {"html_url": "https://forgejo.example.com/owner/repo"}}`),
			expectedProvider: "forgejo",
		},
		{
			name: "Forgejo provider with only Forgejo header",
			headers: map[string]string{
				"X-Forgejo-Event-Type": "pull_request",
			},
			body:             []byte(`{"repository": {"html_url": "https://forgejo.example.com/owner/repo"}}`),
			expectedProvider: "forgejo",
		},
		{
			name: "Forgejo provider with GitHub and Forgejo headers",
			headers: map[string]string{
				"X-GitHub-Event":       "pull_request",
				"X-Forgejo-Event-Type": "pull_request",
			},
			body:             []byte(`{"repository": {"html_url": "https://forgejo.example.com/owner/repo"}}`),
			expectedProvider: "forgejo",
		},
		{
			name: "GitLab provider",
			headers: map[string]string{
				"X-Gitlab-Event": "Merge Request Hook",
			},
			body:             []byte(`{"project": {"web_url": "https://gitlab.com/owner/repo"}}`),
			expectedProvider: "gitlab",
		},
		{
			name: "Bitbucket Cloud provider",
			headers: map[string]string{
				"User-Agent": "Bitbucket-Webhooks/2.0",
			},
			body:             []byte(`{"actor": {"account_id": "123"}}`),
			expectedProvider: "bitbucket-cloud",
		},
		{
			name: "Bitbucket Data Center provider",
			headers: map[string]string{
				"User-Agent": "Bitbucket-Webhooks/2.0",
			},
			body:             []byte(`{"actor": {"id": 123}}`),
			expectedProvider: "bitbucket-datacenter",
		},
		{
			name: "Unknown provider",
			headers: map[string]string{
				"X-Unknown-Header": "value",
			},
			body:          []byte(`{}`),
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := detectProvider(tt.headers, tt.body)
			if tt.expectedError {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, provider, tt.expectedProvider)
			}
		})
	}
}

func TestEventFromGitLab(t *testing.T) {
	tests := []struct {
		name            string
		headers         map[string]string
		body            []byte
		expectedError   bool
		expectedOrg     string
		expectedRepo    string
		expectedSender  string
		expectedTrigger string
	}{
		{
			name: "GitLab merge request event",
			headers: map[string]string{
				"X-Gitlab-Event": "Merge Request Hook",
			},
			body: []byte(`{
				"object_kind": "merge_request",
				"user": {"username": "testuser"},
				"project": {
					"path_with_namespace": "testorg/testrepo",
					"web_url": "https://gitlab.com/testorg/testrepo"
				},
				"object_attributes": {
					"iid": 1,
					"target_branch": "main",
					"source_branch": "feature",
					"title": "Test MR"
				}
			}`),
			expectedOrg:     "testorg",
			expectedRepo:    "testrepo",
			expectedSender:  "testuser",
			expectedTrigger: triggertype.PullRequest.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := eventFromGitLab(tt.body, tt.headers)
			if tt.expectedError {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, event.Organization, tt.expectedOrg)
			assert.Equal(t, event.Repository, tt.expectedRepo)
			assert.Equal(t, event.Sender, tt.expectedSender)
			assert.Equal(t, event.TriggerTarget.String(), tt.expectedTrigger)
		})
	}
}

func TestDirectCELVariables(t *testing.T) {
	// Test that the direct CEL variables (as per PAC documentation) work correctly
	pullRequestPayload := `{
  "action": "opened",
  "number": 123,
  "pull_request": {
    "title": "Add feature",
    "user": {
      "login": "testuser"
    },
    "head": {
      "ref": "feature-branch",
      "sha": "abc123",
      "repo": {
        "html_url": "https://github.com/testuser/test-repo"
      }
    },
    "base": {
      "ref": "main",
      "repo": {
        "html_url": "https://github.com/testorg/test-repo"
      }
    },
    "draft": false
  },
  "repository": {
    "name": "test-repo",
    "full_name": "testorg/test-repo",
    "owner": {
      "login": "testorg"
    },
    "html_url": "https://github.com/testorg/test-repo",
    "default_branch": "main"
  },
  "sender": {
    "login": "testuser"
  }
}`

	headers := map[string]string{
		"X-GitHub-Event": "pull_request",
		"Content-Type":   "application/json",
	}

	// Parse the event
	event, err := eventFromGitHub([]byte(pullRequestPayload), headers)
	assert.NilError(t, err)

	// Generate PAC parameters
	pacParams := pacParamsFromEvent(event)

	// Parse the body for CEL access
	var body map[string]any
	err = json.Unmarshal([]byte(pullRequestPayload), &body)
	assert.NilError(t, err)

	// Create files data structure (empty for CLI)
	filesData := map[string]any{
		"all":      []string{},
		"added":    []string{},
		"deleted":  []string{},
		"modified": []string{},
		"renamed":  []string{},
	}

	// Test direct CEL variables as per PAC documentation
	tests := []struct {
		name       string
		expression string
		wantResult string
		wantErr    bool
	}{
		{
			name:       "direct event variable",
			expression: `event == "pull_request"`,
			wantResult: "true",
		},
		{
			name:       "direct target_branch variable",
			expression: `target_branch == "main"`,
			wantResult: "true",
		},
		{
			name:       "direct source_branch variable",
			expression: `source_branch == "feature-branch"`,
			wantResult: "true",
		},
		{
			name:       "direct event_title variable",
			expression: `event_title == "Add feature"`,
			wantResult: "true",
		},
		{
			name:       "direct target_url variable",
			expression: `target_url.contains("testorg/test-repo")`,
			wantResult: "true",
		},
		{
			name:       "direct source_url variable",
			expression: `source_url.contains("testuser/test-repo")`,
			wantResult: "true",
		},
		{
			name:       "combined expression like PAC docs",
			expression: `event == "pull_request" && target_branch == "main"`,
			wantResult: "true",
		},
		{
			name:       "regex matching like PAC docs",
			expression: `source_branch.matches(".*feature.*")`,
			wantResult: "true",
		},
		{
			name:       "negative condition like PAC docs",
			expression: `event == "pull_request" && target_branch != "experimental"`,
			wantResult: "true",
		},
		{
			name:       "backward compatibility - pac variables still work",
			expression: `pac.event == "pull_request"`,
			wantResult: "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pkgcel.Value(tt.expression, body, headers, pacParams, filesData)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			resultStr := fmt.Sprintf("%v", result)
			assert.Equal(t, resultStr, tt.wantResult)
		})
	}
}

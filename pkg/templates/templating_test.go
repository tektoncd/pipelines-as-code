package templates

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gotest.tools/v3/assert"
)

func TestReplacePlaceHoldersVariables(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		expected     string
		dicto        map[string]string
		headers      http.Header
		changedFiles map[string]any
		rawEvent     any
	}{
		{
			name:     "Test Replace standard",
			template: `revision: {{ revision }}} url: {{ url }} bar: {{ bar}} tag: {{ git_tag }}`,
			expected: `revision: master} url: https://chmouel.com bar: {{ bar}} tag: v1.0`,
			dicto: map[string]string{
				"revision": "master",
				"url":      "https://chmouel.com",
				"git_tag":  "v1.0",
			},
			changedFiles: map[string]any{},
		},
		{
			name:         "Test Replace with CEL body",
			template:     `hello: {{ body.hello }}`,
			expected:     `hello: world`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]string{
				"hello": "world",
			},
		},
		{
			name:         "Test Replace with CEL body expression",
			template:     `Is this {{ body.hello == 'world' }}`,
			expected:     `Is this true`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]string{
				"hello": "world",
			},
		},
		{
			name:         "Test Replace with headers",
			template:     `header: {{ headers["X-Hello"] }}`,
			expected:     `header: World`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers: http.Header{
				"X-Hello": []string{"World"},
			},
			rawEvent: map[string]string{},
		},
		{
			name:     "Changed files - changed",
			template: `changed: {{ files.all[0] }}`,
			expected: `changed: changed.txt`,
			dicto:    map[string]string{},
			changedFiles: map[string]any{
				"all": []string{"changed.txt"},
			},
			headers:  http.Header{},
			rawEvent: map[string]string{},
		},
		{
			name:         "Test with nil rawEvent and headers",
			template:     `body: {{ body.hello }}`,
			expected:     `body: {{ body.hello }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      nil,
			rawEvent:     nil,
		},
		{
			name:         "Test with nil headers only",
			template:     `body: {{ body.hello }}`,
			expected:     `body: world`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      nil,
			rawEvent: map[string]string{
				"hello": "world",
			},
		},
		{
			name:         "Test CEL with numeric value",
			template:     `count: {{ body.count }}`,
			expected:     `count: 42`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"count": 42,
			},
		},
		{
			name:         "Test CEL with boolean value",
			template:     `enabled: {{ body.enabled }}`,
			expected:     `enabled: true`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"enabled": true,
			},
		},
		{
			name:         "Test CEL with invalid key",
			template:     `invalid: {{ body.nonexistent }}`,
			expected:     `invalid: {{ body.nonexistent }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"existing": "value",
			},
		},
		{
			name:     "Test with file prefix but nil rawEvent",
			template: `file: {{ files.modified }}`,
			expected: `file: true`,
			dicto:    map[string]string{},
			changedFiles: map[string]any{
				"modified": "true",
			},
			headers:  http.Header{},
			rawEvent: nil,
		},
		{
			name:         "Test with header prefix but nil rawEvent",
			template:     `header: {{ headers.Accept }}`,
			expected:     `header: application/json`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers: http.Header{
				"Accept": []string{"application/json"},
			},
			rawEvent: nil,
		},
		{
			name:     "Test placeholder not found in dico",
			template: `missing: {{ missing_key }}`,
			expected: `missing: {{ missing_key }}`,
			dicto: map[string]string{
				"existing_key": "value",
			},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     nil,
		},
		{
			name:     "Test multiple placeholders mixed",
			template: `dico: {{ revision }}, body: {{ body.hello }}, header: {{ headers.Test }}`,
			expected: `dico: main, body: world, header: value`,
			dicto: map[string]string{
				"revision": "main",
			},
			changedFiles: map[string]any{},
			headers: http.Header{
				"Test": []string{"value"},
			},
			rawEvent: map[string]any{
				"hello": "world",
			},
		},
		{
			name:     "Test CEL with string join",
			template: `all files: {{ files.all.join(", ") }}`,
			expected: `all files: file1.txt, file2.txt, file3.txt`,
			dicto: map[string]string{
				"revision": "main",
			},
			changedFiles: map[string]any{
				"all": []string{"file1.txt", "file2.txt", "file3.txt"},
			},
			headers: http.Header{
				"Test": []string{"value"},
			},
			rawEvent: map[string]any{
				"hello": "world",
			},
		},
		{
			name:     "Test CEL with string replace",
			template: `{{ cel: trigger_comment.replace("bad", "good") }}`,
			expected: `a good comment!`,
			dicto: map[string]string{
				"trigger_comment": "a bad comment!",
			},
			changedFiles: map[string]any{},
			headers: http.Header{
				"Test": []string{"value"},
			},
			rawEvent: map[string]any{
				"hello": "world",
			},
		},
		{
			name:     "Test CEL with string substring",
			template: `{{ cel: "HELLOWORLD".substring(0, 5) }}`,
			expected: `HELLO`,
			dicto: map[string]string{
				"trigger_comment": "a bad comment!",
			},
			changedFiles: map[string]any{},
			headers: http.Header{
				"Test": []string{"value"},
			},
			rawEvent: map[string]any{
				"hello": "world",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplacePlaceHoldersVariables(tt.template, tt.dicto, tt.rawEvent, tt.headers, tt.changedFiles)
			if d := cmp.Diff(got, tt.expected); d != "" {
				t.Fatalf("-got, +want: %v", d)
			}
		})
	}
}

func TestReplacePlaceHoldersVariablesJSONOutput(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		dicto        map[string]string
		headers      http.Header
		changedFiles map[string]any
		rawEvent     any
		checkFunc    func(t *testing.T, result string)
	}{
		{
			name:         "CEL array serialization",
			template:     `items: {{ body.items }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"items": []string{"item1", "item2"},
			},
			checkFunc: func(t *testing.T, result string) {
				assert.Assert(t, strings.Contains(result, `"item1"`), "should contain item1")
				assert.Assert(t, strings.Contains(result, `"item2"`), "should contain item2")
				assert.Assert(t, strings.HasPrefix(result, "items: ["), "should start with 'items: ['")
				assert.Assert(t, strings.HasSuffix(result, "]"), "should end with ']'")
			},
		},
		{
			name:         "CEL object serialization",
			template:     `config: {{ body.config }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"config": map[string]any{
					"name":  "test",
					"value": "123",
				},
			},
			checkFunc: func(t *testing.T, result string) {
				assert.Assert(t, strings.Contains(result, `"name"`), "should contain name key")
				assert.Assert(t, strings.Contains(result, `"test"`), "should contain test value")
				assert.Assert(t, strings.Contains(result, `"value"`), "should contain value key")
				assert.Assert(t, strings.Contains(result, `"123"`), "should contain 123 value")
				assert.Assert(t, strings.HasPrefix(result, "config: {"), "should start with 'config: {'")
				assert.Assert(t, strings.HasSuffix(result, "}"), "should end with '}'")
			},
		},
		{
			name:         "CEL double value",
			template:     `price: {{ body.price }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"price": 42.5,
			},
			checkFunc: func(t *testing.T, result string) {
				assert.Assert(t, strings.Contains(result, "42.5"), "should contain double value")
				assert.Assert(t, strings.HasPrefix(result, "price: "), "should start with 'price: '")
			},
		},
		{
			name:         "CEL int value",
			template:     `age: {{ body.age }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"age": int64(25),
			},
			checkFunc: func(t *testing.T, result string) {
				assert.Assert(t, strings.Contains(result, "25"), "should contain int value")
				assert.Assert(t, strings.HasPrefix(result, "age: "), "should start with 'age: '")
			},
		},
		{
			name:         "CEL bytes value",
			template:     `data: {{ body.data }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"data": []byte("hello"),
			},
			checkFunc: func(t *testing.T, result string) {
				assert.Assert(t, strings.HasPrefix(result, "data: "), "should start with 'data: '")
				// Bytes might be base64 encoded or handled differently
				assert.Assert(t, len(result) > len("data: "), "should have some data content")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplacePlaceHoldersVariables(tt.template, tt.dicto, tt.rawEvent, tt.headers, tt.changedFiles)
			tt.checkFunc(t, got)
		})
	}
}

func TestReplacePlaceHoldersVariablesCelPrefix(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		expected     string
		dicto        map[string]string
		headers      http.Header
		changedFiles map[string]any
		rawEvent     any
	}{
		{
			name:         "cel: prefix with simple body access",
			template:     `result: {{ cel: body.hello }}`,
			expected:     `result: world`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]string{
				"hello": "world",
			},
		},
		{
			name:         "cel: prefix with ternary expression",
			template:     `status: {{ cel: body.action == "opened" ? "new-pr" : "updated-pr" }}`,
			expected:     `status: new-pr`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"action": "opened",
			},
		},
		{
			name:         "cel: prefix with ternary expression - else branch",
			template:     `status: {{ cel: body.action == "opened" ? "new-pr" : "updated-pr" }}`,
			expected:     `status: updated-pr`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"action": "synchronize",
			},
		},
		{
			name:     "cel: prefix with pac namespace access",
			template: `branch: {{ cel: pac.target_branch }}`,
			expected: `branch: main`,
			dicto: map[string]string{
				"target_branch": "main",
			},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:     "cel: prefix with pac namespace conditional",
			template: `env: {{ cel: pac.target_branch == "main" ? "production" : "staging" }}`,
			expected: `env: production`,
			dicto: map[string]string{
				"target_branch": "main",
			},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:     "cel: prefix with pac namespace - staging branch",
			template: `env: {{ cel: pac.target_branch == "main" ? "production" : "staging" }}`,
			expected: `env: staging`,
			dicto: map[string]string{
				"target_branch": "develop",
			},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:         "cel: prefix with has() function",
			template:     `has_field: {{ cel: has(body.optional_field) ? body.optional_field : "default" }}`,
			expected:     `has_field: custom_value`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"optional_field": "custom_value",
			},
		},
		{
			name:         "cel: prefix with has() function - field missing",
			template:     `has_field: {{ cel: has(body.optional_field) ? body.optional_field : "default" }}`,
			expected:     `has_field: default`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:     "cel: prefix with files access",
			template: `go_files: {{ cel: files.all.exists(f, f.endsWith(".go")) ? "yes" : "no" }}`,
			expected: `go_files: yes`,
			dicto:    map[string]string{},
			changedFiles: map[string]any{
				"all": []string{"main.go", "README.md"},
			},
			headers:  http.Header{},
			rawEvent: map[string]any{},
		},
		{
			name:     "cel: prefix with files access - no go files",
			template: `go_files: {{ cel: files.all.exists(f, f.endsWith(".go")) ? "yes" : "no" }}`,
			expected: `go_files: no`,
			dicto:    map[string]string{},
			changedFiles: map[string]any{
				"all": []string{"README.md", "config.yaml"},
			},
			headers:  http.Header{},
			rawEvent: map[string]any{},
		},
		{
			name:         "cel: prefix with headers access",
			template:     `event: {{ cel: headers["X-GitHub-Event"] }}`,
			expected:     `event: push`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers: http.Header{
				"X-GitHub-Event": []string{"push"},
			},
			rawEvent: map[string]any{},
		},
		{
			name:         "cel: prefix with boolean result",
			template:     `is_draft: {{ cel: body.draft == true }}`,
			expected:     `is_draft: false`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"draft": false,
			},
		},
		{
			name:         "cel: prefix with invalid expression returns empty string",
			template:     `invalid: {{ cel: invalid.syntax[ }}`,
			expected:     `invalid: `,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:         "cel: prefix with evaluation error returns empty string",
			template:     `error: {{ cel: body.nonexistent.deep.field }}`,
			expected:     `error: `,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:         "cel: prefix with extra whitespace",
			template:     `result: {{ cel:    body.hello   }}`,
			expected:     `result: world`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]string{
				"hello": "world",
			},
		},
		{
			name:         "cel: prefix with string concatenation",
			template:     `greeting: {{ cel: "Hello, " + body.name + "!" }}`,
			expected:     `greeting: Hello, World!`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"name": "World",
			},
		},
		{
			name:         "cel: prefix with size function",
			template:     `count: {{ cel: body.items.size() }}`,
			expected:     `count: 3`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"items": []string{"a", "b", "c"},
			},
		},
		{
			name:         "cel: prefix with complex nested conditional (merge commit detection)",
			template:     `commit_type: {{ cel: has(body.head_commit) && body.head_commit.message.startsWith("Merge") ? "merge" : "regular" }}`,
			expected:     `commit_type: merge`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"head_commit": map[string]any{
					"message": "Merge pull request #123",
				},
			},
		},
		{
			name:         "cel: prefix with complex nested conditional - regular commit",
			template:     `commit_type: {{ cel: has(body.head_commit) && body.head_commit.message.startsWith("Merge") ? "merge" : "regular" }}`,
			expected:     `commit_type: regular`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"head_commit": map[string]any{
					"message": "Fix bug in parser",
				},
			},
		},
		{
			name:     "cel: prefix mixed with regular placeholders",
			template: `branch: {{ target_branch }}, check: {{ cel: pac.target_branch == "main" ? "prod" : "dev" }}`,
			expected: `branch: main, check: prod`,
			dicto: map[string]string{
				"target_branch": "main",
			},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     map[string]any{},
		},
		{
			name:         "cel: prefix with nil rawEvent still works",
			template:     `result: {{ cel: pac.revision }}`,
			expected:     `result: abc123`,
			dicto:        map[string]string{"revision": "abc123"},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplacePlaceHoldersVariables(tt.template, tt.dicto, tt.rawEvent, tt.headers, tt.changedFiles)
			if d := cmp.Diff(got, tt.expected); d != "" {
				t.Fatalf("-got, +want: %v", d)
			}
		})
	}
}

func TestReplacePlaceHoldersVariablesEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		dicto        map[string]string
		headers      http.Header
		changedFiles map[string]any
		rawEvent     any
		expected     string
	}{
		{
			name:         "CEL expression with complex nested access",
			template:     `nested: {{ body.deep.nested.value }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"deep": map[string]any{
					"nested": map[string]any{
						"value": "found",
					},
				},
			},
			expected: `nested: found`,
		},
		{
			name:         "CEL with nil in nested structure",
			template:     `null: {{ body.value }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers:      http.Header{},
			rawEvent: map[string]any{
				"value": nil,
			},
			expected: `null: `,
		},
		{
			name:         "Multi-level headers access",
			template:     `user-agent: {{ headers["User-Agent"] }}`,
			dicto:        map[string]string{},
			changedFiles: map[string]any{},
			headers: http.Header{
				"User-Agent": []string{"test-agent/1.0"},
			},
			rawEvent: map[string]any{},
			expected: `user-agent: test-agent/1.0`,
		},
		{
			name:     "Files with nested structure",
			template: `file-info: {{ files.metadata.size }}`,
			dicto:    map[string]string{},
			changedFiles: map[string]any{
				"metadata": map[string]any{
					"size": 1024,
				},
			},
			headers:  http.Header{},
			rawEvent: nil,
			expected: `file-info: 1024`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplacePlaceHoldersVariables(tt.template, tt.dicto, tt.rawEvent, tt.headers, tt.changedFiles)
			if d := cmp.Diff(got, tt.expected); d != "" {
				t.Fatalf("-got, +want: %v", d)
			}
		})
	}
}

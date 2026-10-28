package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/git"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name                string
		gitinfo             git.Info
		language            string
		addExtraFilesInRepo map[string]string
		expectedLanguage    string
		expectError         bool
	}{
		{
			name: "detect golang",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			addExtraFilesInRepo: map[string]string{
				"go.mod": "module github.com/test/repo",
			},
			expectedLanguage: "go",
		},
		{
			name: "detect python",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			addExtraFilesInRepo: map[string]string{
				"setup.py": "from setuptools import setup",
			},
			expectedLanguage: "python",
		},
		{
			name: "detect nodejs",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			addExtraFilesInRepo: map[string]string{
				"package.json": "{}",
			},
			expectedLanguage: "nodejs",
		},
		{
			name: "detect java",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			addExtraFilesInRepo: map[string]string{
				"pom.xml": "<project></project>",
			},
			expectedLanguage: "java",
		},
		{
			name: "detect generic",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			addExtraFilesInRepo: map[string]string{},
			expectedLanguage:    "generic",
		},
		{
			name: "explicit language set",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			language:         "go",
			expectedLanguage: "go",
		},
		{
			name: "explicit language set with no template",
			gitinfo: git.Info{
				TopLevelPath: "/tmp/test-repo",
			},
			language:    "unknown",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newdir := fs.NewDir(t, "TestDetectLanguage")
			defer newdir.Remove()
			tt.gitinfo.TopLevelPath = newdir.Path()

			for key, value := range tt.addExtraFilesInRepo {
				err := os.MkdirAll(filepath.Dir(newdir.Join(key)), os.ModePerm)
				assert.NilError(t, err, "failed to create dir: %s", filepath.Dir(newdir.Join(key)))

				err = os.WriteFile(newdir.Join(key), []byte(value), 0o600)
				assert.NilError(t, err, "failed to create file", key)
			}

			io, _, _, _ := cli.IOTest()
			opts := &Opts{
				GitInfo:   &tt.gitinfo,
				IOStreams: io,
				language:  tt.language,
			}

			lang, err := opts.detectLanguage()
			if tt.expectError {
				assert.ErrorContains(t, err, "no template available for")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, lang, tt.expectedLanguage)
			}
		})
	}
}

func TestGenTmpl(t *testing.T) {
	tests := []struct {
		name                string
		gitinfo             git.Info
		event               info.Event
		addExtraFilesInRepo map[string]string
		expectedName        string
		expectedEvent       string
		expectedBranch      string
		useClusterTask      bool
	}{
		{
			name: "generate golang template",
			gitinfo: git.Info{
				URL: "https://hello/golang",
			},
			event: info.Event{
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{
				"go.mod": "module github.com/test/repo",
			},
			expectedName:   "golang-pull-request",
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "generate python template",
			gitinfo: git.Info{
				URL: "https://hello/python",
			},
			event: info.Event{
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{
				"setup.py": "from setuptools import setup",
			},
			expectedName:   "python-pull-request",
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "generate nodejs template",
			gitinfo: git.Info{
				URL: "https://hello/nodejs",
			},
			event: info.Event{
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{
				"package.json": "{}",
			},
			expectedName:   "nodejs-pull-request",
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "generate java template",
			gitinfo: git.Info{
				URL: "https://hello/java",
			},
			event: info.Event{
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{
				"pom.xml": "<project></project>",
			},
			expectedName:   "java-pull-request",
			expectedEvent:  "pull_request",
			expectedBranch: "main",
		},
		{
			name: "generate generic template",
			gitinfo: git.Info{
				URL: "https://hello/generic",
			},
			event: info.Event{
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{},
			expectedName:        "generic-pull-request",
			expectedEvent:       "pull_request",
			expectedBranch:      "main",
		},
		{
			name: "fallback to event URL",
			gitinfo: git.Info{
				URL: ".",
			},
			event: info.Event{
				URL:        "https://hello/fallback",
				EventType:  "pull_request",
				BaseBranch: "main",
			},
			addExtraFilesInRepo: map[string]string{},
			expectedName:        "fallback-pull-request",
			expectedEvent:       "pull_request",
			expectedBranch:      "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newdir := fs.NewDir(t, "TestGenTmpl")
			defer newdir.Remove()
			tt.gitinfo.TopLevelPath = newdir.Path()

			for key, value := range tt.addExtraFilesInRepo {
				err := os.MkdirAll(filepath.Dir(newdir.Join(key)), os.ModePerm)
				assert.NilError(t, err, "failed to create dir: %s", filepath.Dir(newdir.Join(key)))

				err = os.WriteFile(newdir.Join(key), []byte(value), 0o600)
				assert.NilError(t, err, "failed to create file", key)
			}

			io, _, _, _ := cli.IOTest()
			opts := &Opts{
				GitInfo:                 &tt.gitinfo,
				Event:                   &tt.event,
				IOStreams:               io,
				generateWithClusterTask: tt.useClusterTask,
			}

			buf, err := opts.genTmpl()
			assert.NilError(t, err)

			output := buf.String()
			assert.Assert(t, strings.Contains(output, fmt.Sprintf("name: %s", tt.expectedName)))
			assert.Assert(t, strings.Contains(output, fmt.Sprintf("pipelinesascode.tekton.dev/on-event: \"[%s]\"", tt.expectedEvent)))
			assert.Assert(t, strings.Contains(output, fmt.Sprintf("pipelinesascode.tekton.dev/on-target-branch: \"[%s]\"", tt.expectedBranch)))

			if tt.useClusterTask {
				assert.Assert(t, strings.Contains(output, "kind: ClusterTask"))
			}
		})
	}
}

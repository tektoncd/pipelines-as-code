package generate

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type langOpts struct {
	detectionFiles []string
}

// I hate this part of the code so much.. but we are waiting for UBI images
// having >1.6 golang for integrated templates.
var languageDetection = map[string]langOpts{
	"go": {
		detectionFiles: []string{"go.mod"},
	},
	"python": {
		detectionFiles: []string{"setup.py", "pyproject.toml", "poetry.lock"},
	},
	"nodejs": {
		detectionFiles: []string{"package.json"},
	},
	"java": {
		detectionFiles: []string{"pom.xml"},
	},
	"generic": {},
}

//go:embed templates
var resource embed.FS

// detectLanguage determines the programming language used in the repository.
// It first checks if a language has been explicitly set in the options. If so,
// it verifies that a template is available for that language. If not, it returns an error.
// If no language is set, it iterates over the known languages and checks if a
// characteristic file for each language exists in the repository (go.mod >
// go). If it finds a match, it outputs a success message and returns the
// detected language. If no language can be detected, it defaults to "generic".
// Returns the detected language as a string, or an error if a problem
// occurred.
func (o *Opts) detectLanguage() (string, error) {
	if o.language != "" {
		if _, ok := languageDetection[o.language]; !ok {
			return "", fmt.Errorf("no template available for %s", o.language)
		}
		return o.language, nil
	}

	cs := o.IOStreams.ColorScheme()
	for t, v := range languageDetection {
		if v.detectionFiles == nil {
			continue
		}
		for _, f := range v.detectionFiles {
			fpath := filepath.Join(o.GitInfo.TopLevelPath, f)
			if _, err := os.Stat(fpath); !os.IsNotExist(err) {
				fmt.Fprintf(
					o.IOStreams.Out, "%s We detected your repository uses the %s programming language.\n",
					cs.SuccessIcon(),
					cs.Bold(cases.Title(language.Und, cases.NoLower).String(t)),
				)
				return t, nil
			}
		}
	}
	return "generic", nil
}

func (o *Opts) genTmpl() (*bytes.Buffer, error) {
	lang, err := o.detectLanguage()
	if err != nil {
		return nil, err
	}

	embedfile, err := resource.Open(fmt.Sprintf("templates/%s.yaml", lang))
	if err != nil {
		log.Fatal(err)
	}
	defer embedfile.Close()
	tmplB, _ := io.ReadAll(embedfile)

	prName := filepath.Base(o.GitInfo.URL)
	if prName == "." {
		prName = filepath.Base(o.Event.URL)
	}

	// if eventType has both the events [push, pull_request] then skip
	// adding it to pipelinerun name
	if !strings.Contains(o.Event.EventType, ",") {
		prName = prName + "-" + strings.ReplaceAll(o.Event.EventType, "_", "-")
	}

	tmplB = bytes.ReplaceAll(tmplB, []byte("pipelinesascode.tekton.dev/on-event: \"pull_request\""),
		[]byte(fmt.Sprintf("pipelinesascode.tekton.dev/on-event: \"[%s]\"", o.Event.EventType)))

	tmplB = bytes.ReplaceAll(tmplB, []byte("pipelinesascode.tekton.dev/on-target-branch: \"main\""),
		[]byte(fmt.Sprintf("pipelinesascode.tekton.dev/on-target-branch: \"[%s]\"", o.Event.BaseBranch)))

	tmplB = bytes.ReplaceAll(tmplB, []byte(fmt.Sprintf("name: pipelinerun-%s", lang)),
		[]byte(fmt.Sprintf("name: %s", prName)))

	if o.generateWithClusterTask {
		tmplB = bytes.ReplaceAll(tmplB, []byte(fmt.Sprintf("name: %s", gitCloneClusterTaskName)),
			[]byte(fmt.Sprintf("name: %s\n          kind: ClusterTask", gitCloneClusterTaskName)))
	}

	return bytes.NewBuffer(tmplB), nil
}

package test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
)

var (
	defaultAPIURL   = "/api/v1"
	giteaMinVersion = "1.17.0"
)

func Setup(t *testing.T) (*forgejo.Client, *http.ServeMux, func()) {
	mux := http.NewServeMux()
	apiHandler := http.NewServeMux()
	apiHandler.Handle(defaultAPIURL+"/", http.StripPrefix(defaultAPIURL, mux))
	apiHandler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(os.Stderr, "FAIL: Client.BaseURL path prefix is not preserved in the request URL:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\t"+req.URL.String())
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\tDid you accidentally use an absolute endpoint URL rather than relative?")
		http.Error(w, "Client.BaseURL path prefix is not preserved in the request URL.", http.StatusInternalServerError)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version": "%s"}`, giteaMinVersion)
	})
	//
	// server is a test HTTP server used to provide mock API responses.
	server := httptest.NewServer(apiHandler)

	tearDown := func() {
		server.Close()
	}

	client, err := forgejo.NewClient(server.URL)
	assert.NilError(t, err)
	return client, mux, tearDown
}

// SetupGitTree Take a dir and fake a full GitTree Gitea api calls reply recursively over a muxer.
func SetupGitTree(t *testing.T, mux *http.ServeMux, dir string, event *info.Event, recursive bool) {
	type file struct {
		sha, name string
		isdir     bool
	}
	files := []file{}
	if recursive {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			sha := fmt.Sprintf("%x", sha256.Sum256([]byte(path)))
			if err == nil && path != dir {
				files = append(files, file{name: path, isdir: info.IsDir(), sha: sha})
			}
			return nil
		})
		assert.NilError(t, err)
	} else {
		dfiles, err := os.ReadDir(dir)
		assert.NilError(t, err)

		for _, f := range dfiles {
			sha := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Name())))
			files = append(files, file{name: filepath.Join(dir, f.Name()), sha: sha, isdir: f.IsDir()})
		}
	}
	entries := make([]forgejo.GitEntry, 0, len(files))
	for _, f := range files {
		etype := "blob"
		mode := "100644"
		if f.isdir {
			etype = "tree"
			mode = "040000"
			if !recursive {
				SetupGitTree(t, mux, f.name,
					&info.Event{
						Organization: event.Organization,
						Repository:   event.Repository,
						SHA:          f.sha,
					},
					true)
			}
		} else {
			mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/git/blobs/%v", event.Organization, event.Repository, f.sha),
				func(w http.ResponseWriter, r *http.Request) {
					// go over all files and match the sha to the name we want
					sha := filepath.Base(r.URL.Path)
					chosenf := file{}
					for _, f := range files {
						if f.sha == sha {
							chosenf = f
							break
						}
					}
					assert.Assert(t, chosenf.name != "", "sha %s not found", sha)

					s, err := os.ReadFile(chosenf.name)
					assert.NilError(t, err)
					// encode content as base64
					blob := &forgejo.GitBlobResponse{
						SHA:     chosenf.sha,
						Content: base64.StdEncoding.EncodeToString(s),
					}
					b, err := json.Marshal(blob)
					assert.NilError(t, err)
					fmt.Fprint(w, string(b))
				})
		}
		entries = append(entries, forgejo.GitEntry{
			Path: strings.TrimPrefix(f.name, dir+"/"),
			Mode: mode,
			Type: etype,
			SHA:  f.sha,
		})
	}
	u := fmt.Sprintf("/repos/%v/%v/git/trees/%v", event.Organization, event.Repository, event.SHA)
	mux.HandleFunc(u, func(rw http.ResponseWriter, _ *http.Request) {
		tree := &forgejo.GitTreeResponse{
			SHA:     event.SHA,
			Entries: entries,
		}
		// encode tree as json
		b, err := json.Marshal(tree)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

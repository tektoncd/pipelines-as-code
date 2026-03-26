package github

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
)

const (
	// baseURLPath is a non-empty Client.BaseURL path to use during tests,
	// to ensure relative URLs are used for all endpoints. See issue #752.
	githubBaseURLPath = "/api/v3"
)

// SetupGH Setup a GitHUB httptest connection, from go-github test-suit.
func SetupGH() (client *github.Client, mux *http.ServeMux, serverURL string, teardown func()) {
	// mux is the HTTP request multiplexer used with the test server.
	mux = http.NewServeMux()

	// We want to ensure that tests catch mistakes where the endpoint URL is
	// specified as absolute rather than relative. It only makes a difference
	// when there's a non-empty base URL path. So, use that. See issue #752.
	apiHandler := http.NewServeMux()
	apiHandler.Handle(githubBaseURLPath+"/", http.StripPrefix(githubBaseURLPath, mux))
	apiHandler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(os.Stderr, "FAIL: Client.BaseURL path prefix is not preserved in the request URL:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\t"+req.URL.String())
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\tDid you accidentally use an absolute endpoint URL rather than relative?")
		fmt.Fprintln(os.Stderr, "\tSee https://github.com/google/go-github/issues/752 for information.")
		http.Error(w, "Client.BaseURL path prefix is not preserved in the request URL.", http.StatusInternalServerError)
	})

	// server is a test HTTP server used to provide mock API responses.
	server := httptest.NewServer(apiHandler)

	// client is the GitHub client being tested and is
	// configured to use test server.
	client = github.NewClient(nil)
	url, _ := url.Parse(server.URL + githubBaseURLPath + "/")
	client.BaseURL = url
	client.UploadURL = url

	return client, mux, server.URL, server.Close
}

// SetupGitTree Take a dir and fake a full GitTree GitHub api calls reply recursively over a muxer.
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
	entries := make([]*github.TreeEntry, 0, len(files))
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
					blob := &github.Blob{
						SHA:     github.Ptr(chosenf.sha),
						Content: github.Ptr(base64.StdEncoding.EncodeToString(s)),
					}
					b, err := json.Marshal(blob)
					assert.NilError(t, err)
					fmt.Fprint(w, string(b))
				})
		}
		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(strings.TrimPrefix(f.name, dir+"/")),
			Mode: github.Ptr(mode),
			Type: github.Ptr(etype),
			SHA:  github.Ptr(f.sha),
		})
	}
	u := fmt.Sprintf("/repos/%v/%v/git/trees/%v", event.Organization, event.Repository, event.SHA)
	mux.HandleFunc(u, func(rw http.ResponseWriter, _ *http.Request) {
		tree := &github.Tree{
			SHA:     &event.SHA,
			Entries: entries,
		}
		// encode tree as json
		b, err := json.Marshal(tree)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

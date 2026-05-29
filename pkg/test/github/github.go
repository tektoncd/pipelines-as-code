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

	"github.com/google/go-github/v85/github"
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
	// GraphQL endpoint is at /api/graphql (not under /api/v3)
	apiHandler.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
		// Forward to mux for GraphQL handling
		mux.ServeHTTP(w, r)
	})
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

// graphQLFileMapType is used to store files for GraphQL handler lookup.
type graphQLFileMapType map[string]struct {
	sha, name string
	isdir     bool
}

// graphQLRequest represents a GraphQL request structure.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// handleTektonTreeQuery handles the new GraphQL query format for fetching tekton tree with inline blobs.
func handleTektonTreeQuery(w http.ResponseWriter, graphQLReq graphQLRequest, allFiles graphQLFileMapType) {
	// Extract tektonExpr from variables (e.g., "sha123:.tekton")
	tektonExpr, ok := graphQLReq.Variables["tektonExpr"].(string)
	if !ok {
		http.Error(w, "Missing tektonExpr variable", http.StatusBadRequest)
		return
	}

	// Parse tektonExpr: "ref:path" -> extract path
	parts := strings.SplitN(tektonExpr, ":", 2)
	if len(parts) != 2 {
		http.Error(w, "Invalid tektonExpr format", http.StatusBadRequest)
		return
	}
	tektonPath := parts[1]

	// Check if .tekton itself is a file in allFiles
	if fileInfo, exists := allFiles[tektonPath]; exists {
		// .tekton is a file, not a directory - return Blob response (no entries field)
		content, err := os.ReadFile(fileInfo.name)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusInternalServerError)
			return
		}

		// Return Blob object response (this will cause the implementation to see no Tree)
		responseData := map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"tektonTree": map[string]any{
						"text": string(content), // Blob has text field, not entries
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responseData)
		return
	}

	// Build tree entries for all files under the tekton path
	entries := []map[string]any{}
	for relPath, fileInfo := range allFiles {
		// Check if file is under the tekton path
		// For .tekton directory, files should be like "pipeline.yaml", "task.yaml", "subdir/file.yaml"
		if !strings.HasPrefix(relPath, tektonPath+"/") && relPath != tektonPath {
			continue
		}

		// Extract path relative to tekton directory
		pathInTekton := strings.TrimPrefix(relPath, tektonPath+"/")
		if pathInTekton == "" {
			continue
		}

		// Read file content
		content, err := os.ReadFile(fileInfo.name)
		if err != nil {
			continue
		}

		// Only include .yaml and .yml files (matching implementation behavior)
		if !strings.HasSuffix(relPath, ".yaml") && !strings.HasSuffix(relPath, ".yml") {
			continue
		}

		entry := map[string]any{
			"name": filepath.Base(pathInTekton),
			"type": "blob",
			"path": pathInTekton,
			"oid":  fileInfo.sha,
			"object": map[string]any{
				"text": string(content),
			},
		}
		entries = append(entries, entry)
	}

	// Build response
	responseData := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"tektonTree": map[string]any{
					"entries": entries,
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responseData)
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

	// Setup GraphQL endpoint handler for tekton directory fetching (only once per mux)
	// Only register GraphQL handler once (at the root level, when recursive=false)
	if !recursive {
		// Walk the entire directory tree to collect all files for the GraphQL handler
		allFiles := make(graphQLFileMapType)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && path != dir {
				relPath := strings.TrimPrefix(path, dir+"/")
				allFiles[relPath] = struct {
					sha, name string
					isdir     bool
				}{
					sha:   fmt.Sprintf("%x", sha256.Sum256([]byte(path))),
					name:  path,
					isdir: false,
				}
			}
			return nil
		})
		assert.NilError(t, err)

		// Register handler once with all collected files (only if we have files)
		if len(allFiles) > 0 {
			mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}

				var graphQLReq graphQLRequest
				if err := json.NewDecoder(r.Body).Decode(&graphQLReq); err != nil {
					http.Error(w, fmt.Sprintf("Invalid GraphQL request: %v", err), http.StatusBadRequest)
					return
				}

				handleTektonTreeQuery(w, graphQLReq, allFiles)
			})
		}
	}
}

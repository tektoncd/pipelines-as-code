package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
)

const bbBaseURLPath = "/2.0"

func SetupBBCloudClient(t *testing.T) (*bitbucket.Client, *http.ServeMux, func()) {
	t.Helper()
	mux := http.NewServeMux()
	apiHandler := http.NewServeMux()
	apiHandler.Handle(bbBaseURLPath+"/", http.StripPrefix(bbBaseURLPath, mux))
	apiHandler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(os.Stderr, "FAIL: Client.BaseURL path prefix is not preserved in the request URL:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\t"+req.URL.String())
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "\tDid you accidentally use an absolute endpoint URL rather than relative?")
		http.Error(w, "Client.BaseURL path prefix is not preserved in the request URL.", http.StatusInternalServerError)
	})

	// server is a test HTTP server used to provide mock API responses.
	server := httptest.NewServer(apiHandler)

	restoreEnv := env.PatchAll(t, map[string]string{
		"BITBUCKET_API_BASE_URL": server.URL + bbBaseURLPath,
	})

	tearDown := func() {
		server.Close()
		restoreEnv()
	}

	client, _ := bitbucket.NewBasicAuth("", "")
	client.HttpClient = server.Client()
	return client, mux, tearDown
}

func MuxComments(t *testing.T, mux *http.ServeMux, event *info.Event, comments []types.Comment) {
	t.Helper()

	assert.Assert(t, event.Event != nil)

	pr, ok := event.Event.(*types.PullRequestEvent)
	assert.Assert(t, ok)
	prID := fmt.Sprintf("%d", pr.PullRequest.ID)
	mux.HandleFunc("/repositories/"+event.Organization+"/"+event.Repository+"/pullrequests/"+prID+"/comments/",
		func(rw http.ResponseWriter, _ *http.Request) {
			members := &types.Comments{
				Values: comments,
			}
			b, err := json.Marshal(members)
			assert.NilError(t, err)
			fmt.Fprint(rw, string(b))
		})
}

func MuxOrgMember(t *testing.T, mux *http.ServeMux, event *info.Event, members []types.Member) {
	t.Helper()
	mux.HandleFunc("/workspaces/"+event.Organization+"/members",
		func(rw http.ResponseWriter, _ *http.Request) {
			members := &types.Members{
				Values: members,
			}
			b, err := json.Marshal(members)
			assert.NilError(t, err)
			fmt.Fprint(rw, string(b))
		})
}

func MuxFiles(t *testing.T, mux *http.ServeMux, event *info.Event, filescontents map[string]string, provenance string) {
	t.Helper()

	sha := event.SHA
	if provenance == "default_branch" {
		sha = event.DefaultBranch
	}
	for key := range filescontents {
		target := fmt.Sprintf("/repositories/%s/%s/src/%s", event.Organization, event.Repository, sha)
		mux.HandleFunc(target+"/"+key, func(rw http.ResponseWriter, r *http.Request) {
			s := strings.ReplaceAll(r.URL.String(), target, "")
			s = strings.TrimPrefix(s, "/")
			fmt.Fprint(rw, filescontents[s])
		})
	}
}

func MuxListDirFiles(t *testing.T, mux *http.ServeMux, event *info.Event, dirs map[string][]bitbucket.RepositoryFile, provenance string) {
	t.Helper()
	sha := event.SHA
	if provenance == "default_branch" {
		sha = event.DefaultBranch
	}

	for key, value := range dirs {
		urlp := "/repositories/" + event.Organization + "/" + event.Repository + "/src/" + sha + "/" + key
		mux.HandleFunc(urlp, func(rw http.ResponseWriter, _ *http.Request) {
			dircontents := map[string][]bitbucket.RepositoryFile{
				"values": value,
			}
			b, err := json.Marshal(dircontents)
			assert.NilError(t, err)
			fmt.Fprint(rw, string(b))
		})
	}
}

// MuxCommit mocks the GetCommit API (single commit, not paginated).
func MuxCommit(t *testing.T, mux *http.ServeMux, event *info.Event, commit types.Commit) {
	t.Helper()

	shas := []string{}
	if event.SHA != "" {
		shas = append(shas, event.SHA)
	}
	if commit.Hash != "" && commit.Hash != event.SHA {
		shas = append(shas, commit.Hash)
	}
	if len(shas) == 0 {
		shas = append(shas, "HEAD")
	}

	for _, sha := range shas {
		path := fmt.Sprintf("/repositories/%s/%s/commit/%s", event.Organization, event.Repository, sha)
		mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
			// GetCommit returns a single commit object, not {values: [...]}
			b, _ := json.Marshal(commit)
			fmt.Fprint(rw, string(b))
		})
	}
}

func MuxBranch(t *testing.T, mux *http.ServeMux, event *info.Event, commit types.Commit) {
	t.Helper()

	if event.HeadBranch == "" {
		return
	}

	path := fmt.Sprintf("/repositories/%s/%s/refs/branches/%s", event.Organization, event.Repository, event.HeadBranch)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		// Return the commit hash that this branch points to
		branch := map[string]interface{}{
			"name": event.HeadBranch,
			"target": map[string]interface{}{
				"hash": commit.Hash,
			},
		}
		b, _ := json.Marshal(branch)
		fmt.Fprint(rw, string(b))
	})
}

func MuxRepoInfo(t *testing.T, mux *http.ServeMux, event *info.Event, repo *bitbucket.Repository) {
	t.Helper()

	path := fmt.Sprintf("/repositories/%s/%s", event.Organization, event.Repository)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(repo)
		fmt.Fprint(rw, string(b))
	})
}

func MuxCreateCommitstatus(t *testing.T, mux *http.ServeMux, event *info.Event, expectedDescSubstr, applicationName string, expStatus status.StatusOpts) {
	t.Helper()

	path := fmt.Sprintf("/repositories/%s/%s/commit/%s/statuses/build", event.Organization, event.Repository, event.SHA)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		cso := &bitbucket.CommitStatusOptions{}
		bit, _ := io.ReadAll(r.Body)
		err := json.Unmarshal(bit, cso)
		assert.NilError(t, err)
		pacOpts := &info.PacOpts{Settings: settings.Settings{ApplicationName: applicationName}}
		assert.Equal(t, provider.GetBBCloudStatusKey(expStatus, pacOpts), cso.Key)

		if expStatus.DetailsURL != "" {
			assert.Equal(t, expStatus.DetailsURL, cso.Url)
		}

		if expectedDescSubstr != "" {
			assert.Assert(t, strings.Contains(cso.Description, expectedDescSubstr), "description: %s doesn't have: %s", cso.Description, expectedDescSubstr)
		}

		fmt.Fprintf(rw, "{}")
	})
}

func MuxCreateComment(t *testing.T, mux *http.ServeMux, event *info.Event, expectedCommentSubstr string) {
	t.Helper()

	assert.Assert(t, event.Event != nil)
	prev, ok := event.Event.(*types.PullRequestEvent)
	assert.Assert(t, ok)
	prID := fmt.Sprintf("%d", prev.PullRequest.ID)

	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%s/comments", event.Organization, event.Repository, prID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		cso := &types.Comment{}
		bit, _ := io.ReadAll(r.Body)
		err := json.Unmarshal(bit, cso)
		assert.NilError(t, err)
		if expectedCommentSubstr != "" {
			assert.Assert(t, strings.Contains(cso.Content.Raw, expectedCommentSubstr), "comment: %s doesn't have: %s",
				cso.Content.Raw, expectedCommentSubstr)
		}

		fmt.Fprintf(rw, "{}")
	})
}

func MuxDirContent(t *testing.T, mux *http.ServeMux, event *info.Event, testdir, provenance string) {
	t.Helper()
	files, err := os.ReadDir(testdir)
	assert.NilError(t, err)
	lastindex := strings.LastIndex(testdir, ".tekton")

	relativenamedir := testdir[lastindex:]
	filecontents := map[string]string{}
	brfiles := map[string][]bitbucket.RepositoryFile{}

	for _, file := range files {
		relativename := filepath.Join(relativenamedir, file.Name())
		fpath := filepath.Join(testdir, file.Name())
		if file.IsDir() {
			btype := "commit_directory"
			brfiles[relativenamedir] = append(brfiles[relativenamedir], bitbucket.RepositoryFile{
				Path: relativename,
				Type: btype,
			})

			MuxDirContent(t, mux, event, fpath, provenance)
		} else {
			btype := "file"
			brfiles[relativenamedir] = append(brfiles[relativenamedir], bitbucket.RepositoryFile{
				Path: relativename,
				Type: btype,
			})
			content, err := os.ReadFile(fpath)
			assert.NilError(t, err)
			filecontents[relativename] = string(content)
		}
	}
	MuxListDirFiles(t, mux, event, brfiles, provenance)
	MuxFiles(t, mux, event, filecontents, provenance)
}

func MakePREvent(accountid, nickname, sha, comment string) types.PullRequestEvent {
	if accountid == "" {
		accountid = "prid"
	}
	if nickname == "" {
		nickname = "prnickname"
	}
	if sha == "" {
		sha = "prchat"
	}
	pr := types.PullRequestEvent{
		Repository: types.Repository{
			Workspace: types.Workspace{
				Slug: "organization",
			},
			Name:     "repo",
			FullName: "organization/repo",
			Links: types.Links{
				HTML: types.HTMLLink{
					HRef: "https://notgh.org/organization/repo",
				},
			},
		},
		PullRequest: types.PullRequest{
			Author: types.Author{
				AccountID: accountid,
				Nickname:  nickname,
			},
			Destination: types.Destination{
				Branch: types.Branch{
					Name: "main",
				},
			},
			Source: types.Source{
				Branch: types.Branch{
					Name: "branch",
				},
				Commit: types.Commit{
					Hash:    sha,
					Message: "First Draft",
				},
			},
		},
	}

	if comment != "" {
		pr.Comment = types.Comment{
			Content: types.Content{
				Raw: comment,
			},
			User: types.User{},
		}
	}
	return pr
}

func MakePushEvent(accountid, nickname, sha, changeType string) types.PushRequestEvent {
	if accountid == "" {
		accountid = "countlady"
	}
	if nickname == "" {
		nickname = "Fonzie"
	}
	if sha == "" {
		sha = "chatchien"
	}
	return types.PushRequestEvent{
		Actor: types.User{
			AccountID: accountid,
			Nickname:  nickname,
		},
		Push: types.Push{
			Changes: []types.Change{
				{
					New: types.ChangeType{
						Name: "mychange",
						Target: types.Commit{
							Hash: sha,
						},
						Type: changeType,
					},
					Old: types.ChangeType{
						Target: types.Commit{
							Hash: "veryold",
						},
					},
				},
			},
		},
		Repository: types.Repository{
			Workspace: types.Workspace{
				Slug: "org",
			},
			Name:     "repo",
			FullName: "org/repo",
			Links: types.Links{
				HTML: types.HTMLLink{
					HRef: "https://vavar/repo/org",
				},
			},
		},
	}
}

// MakeEvent should we try to reflect? or json.Marshall? may be better ways, right?
func MakeEvent(event *info.Event) *info.Event {
	if event == nil {
		event = info.NewEvent()
	}
	rev := event
	if event.Provider == nil {
		event.Provider = &info.Provider{}
	}
	if rev.HeadBranch == "" {
		rev.HeadBranch = "pr"
	}
	if rev.BaseBranch == "" {
		rev.BaseBranch = "main"
	}
	if rev.SHA == "" {
		rev.SHA = "1234"
	}
	if rev.SHA == "none" {
		rev.SHA = ""
	}
	if rev.Organization == "" {
		rev.Organization = "owner"
	}
	if rev.Repository == "" {
		rev.Repository = "repo"
	}
	if rev.AccountID == "" {
		rev.AccountID = "accountid"
	}
	if rev.DefaultBranch == "" {
		rev.DefaultBranch = "main"
	}
	if rev.Sender == "" {
		rev.Sender = "sender"
	}
	if rev.Event == nil {
		rev.Event = &types.PullRequestEvent{PullRequest: types.PullRequest{ID: 666}}
	}
	rev.PullRequestNumber = 666
	return rev
}

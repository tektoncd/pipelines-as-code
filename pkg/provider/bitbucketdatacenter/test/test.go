package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/go-scm/scm/driver/stash"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"gotest.tools/v3/assert"
)

var (
	defaultAPIURL      = "/rest/api/1.0"
	buildAPIURL        = "/rest/build-status/1.0"
	defaultApplinksURL = "/plugins/servlet/applinks"
)

func SetupBBDataCenterClient() (*scm.Client, *http.ServeMux, func(), string) {
	mux := http.NewServeMux()
	apiHandler := http.NewServeMux()
	apiHandler.Handle(defaultAPIURL+"/", http.StripPrefix(defaultAPIURL, mux))
	apiHandler.Handle(buildAPIURL+"/", http.StripPrefix(buildAPIURL, mux))
	apiHandler.Handle(defaultApplinksURL+"/", http.StripPrefix(defaultApplinksURL, mux))
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

	tearDown := func() {
		server.Close()
	}

	scmClient, err := stash.New(server.URL)
	if err != nil {
		panic(fmt.Sprintf("test setup: stash.New(%q): %v", server.URL, err))
	}
	scmClient.Client = server.Client()
	return scmClient, mux, tearDown, server.URL
}

func MuxCreateComment(t *testing.T, mux *http.ServeMux, event *info.Event, expectedCommentSubstr string, prID int) {
	assert.Assert(t, event.Event != nil)

	path := fmt.Sprintf("/projects/%s/repos/%s/pull-requests/%d/comments", event.Organization, event.Repository, prID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		cso := &Comment{}
		bit, _ := io.ReadAll(r.Body)
		err := json.Unmarshal(bit, cso)
		assert.NilError(t, err)
		if expectedCommentSubstr != "" {
			assert.Assert(t, strings.Contains(cso.Text, expectedCommentSubstr), "comment: %s doesn't have: %s",
				cso.Text, expectedCommentSubstr)
		}

		fmt.Fprintf(rw, "{}")
	})
}

// MakeEvent should we try to reflect? or json.Marshall? may be better ways, right?
func MakeEvent(event *info.Event) *info.Event {
	if event == nil {
		event = info.NewEvent()
	}
	rev := event
	if event.Provider == nil {
		rev.Provider = &info.Provider{}
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
		rev.Event = &types.PullRequestEvent{
			PullRequest: types.PullRequest{ID: 666},
		}
	}
	return rev
}

func MuxDirContent(t *testing.T, mux *http.ServeMux, event *info.Event, testDir, targetDirName string, wantDirErr, wantFilesErr bool) {
	files, err := os.ReadDir(testDir)
	if err != nil {
		// no error just disappointed
		return
	}
	filenames := make([]string, 0, len(files))
	filecontents := map[string]string{}
	for _, value := range files {
		path := filepath.Join(targetDirName, value.Name())
		filenames = append(filenames, value.Name())

		fpath := filepath.Join(testDir, value.Name())
		if info, err := os.Stat(fpath); err == nil && info.IsDir() {
			continue
		}
		content, err := os.ReadFile(fpath)
		assert.NilError(t, err)
		filecontents[path] = string(content)
	}

	MuxListDir(t, mux, event, targetDirName, filenames, wantDirErr)
	MuxFiles(t, mux, event, event.HeadBranch, targetDirName, filecontents, wantFilesErr)
}

func MuxCommitInfo(t *testing.T, mux *http.ServeMux, event *info.Event, commit scm.Commit) {
	path := fmt.Sprintf("/projects/%s/repos/%s/commits/%s", event.Organization, event.Repository, event.SHA)

	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		b, err := json.Marshal(commit)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

func MuxDefaultBranch(t *testing.T, mux *http.ServeMux, event *info.Event, defaultBranch, latestCommit string) {
	path := fmt.Sprintf("/projects/%s/repos/%s/branches/default", event.Organization, event.Repository)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		resp := &Branch{
			LatestCommit: latestCommit,
			DisplayID:    defaultBranch,
		}
		b, err := json.Marshal(resp)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

func MuxFiles(_ *testing.T, mux *http.ServeMux, event *info.Event, _, targetDirName string, filescontents map[string]string, wantErr bool) {
	for filename := range filescontents {
		path := fmt.Sprintf("/projects/%s/repos/%s/raw/%s", event.Organization, event.Repository, filename)
		mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
			if wantErr {
				rw.WriteHeader(http.StatusUnauthorized)
			}
			// delete everything until the targetDirName in string this is
			// fragile, so we do the filepath.Base if we can't mark by targetDirName
			s := r.URL.Path[strings.LastIndex(r.URL.Path, targetDirName):]
			if s == "" {
				s = filepath.Base(r.URL.Path)
			}
			fmt.Fprint(rw, filescontents[s])
		})
	}
}

func MuxListDir(t *testing.T, mux *http.ServeMux, event *info.Event, path string, files []string, wantErr bool) {
	url := fmt.Sprintf("/projects/%s/repos/%s/files/%s", event.Organization, event.Repository, path)
	mux.HandleFunc(url, func(rw http.ResponseWriter, _ *http.Request) {
		if wantErr {
			rw.WriteHeader(http.StatusUnauthorized)
		}

		// as pagination of jenkins-x/go-scm is not like previous one
		// it doesn't work as it did with previous lib.
		resp := map[string]any{
			"start":         0,
			"isLastPage":    true,
			"values":        files,
			"size":          len(files),
			"nextPageStart": 0,
		}
		b, err := json.Marshal(resp)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

func MuxCreateAndTestCommitStatus(t *testing.T, mux *http.ServeMux, event *info.Event, expectedDescSubstr string, expStatus status.StatusOpts) {
	path := fmt.Sprintf("/commits/%s", event.SHA)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		cso := &BuildStatus{}
		bit, _ := io.ReadAll(r.Body)
		err := json.Unmarshal(bit, cso)
		assert.NilError(t, err)

		if expStatus.DetailsURL != "" {
			assert.Equal(t, expStatus.DetailsURL, cso.URL)
		}
		if expectedDescSubstr != "" {
			assert.Assert(t, strings.Contains(cso.Description, expectedDescSubstr),
				"description: %s doesn't have: %s", cso.Description, expectedDescSubstr)
		}

		fmt.Fprintf(rw, "{}")
	})
}

func muxPermissions(t *testing.T, mux *http.ServeMux, path string, values any) {
	t.Helper()
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"values":     values,
			"isLastPage": true,
		}
		b, err := json.Marshal(resp)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

func MuxProjectMemberShip(t *testing.T, mux *http.ServeMux, event *info.Event, userperms []*UserPermission) {
	path := fmt.Sprintf("/projects/%s/permissions/users", event.Organization)
	muxPermissions(t, mux, path, userperms)
}

func MuxProjectGroupMembership(t *testing.T, mux *http.ServeMux, event *info.Event, groups []*ProjGroup) {
	path := fmt.Sprintf("/projects/%s/permissions/groups", event.Organization)
	muxPermissions(t, mux, path, groups)
}

func MuxRepoGroupMembership(t *testing.T, mux *http.ServeMux, event *info.Event, groups []*ProjGroup) {
	path := fmt.Sprintf("/projects/%s/repos/%s/permissions/groups", event.Organization, event.Repository)
	muxPermissions(t, mux, path, groups)
}

func MuxRepoMemberShip(t *testing.T, mux *http.ServeMux, event *info.Event, userperms []*UserPermission) {
	path := fmt.Sprintf("/projects/%s/repos/%s/permissions/users", event.Organization, event.Repository)
	muxPermissions(t, mux, path, userperms)
}

// MuxGroupMembers mocks the /admin/groups/more-members endpoint used by go-scm
// to resolve group names to individual users. groupMembers maps group name to
// the list of members in that group.
func MuxGroupMembers(t *testing.T, mux *http.ServeMux, groupMembers map[string][]GroupMember) {
	t.Helper()
	mux.HandleFunc("/admin/groups/more-members", func(rw http.ResponseWriter, r *http.Request) {
		groupName := r.URL.Query().Get("context")
		members, ok := groupMembers[groupName]
		if !ok {
			members = []GroupMember{}
		}
		resp := map[string]any{
			"values":     members,
			"isLastPage": true,
		}
		b, err := json.Marshal(resp)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}

func MuxPullRequestActivities(t *testing.T, mux *http.ServeMux, event *info.Event, prNumber int, activities []*Activity) {
	path := fmt.Sprintf("/projects/%s/repos/%s/pull-requests/%d/activities", event.Organization, event.Repository, prNumber)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"values": activities,
		}
		b, err := json.Marshal(resp)
		assert.NilError(t, err)

		fmt.Fprint(rw, string(b))
	})
}

func MakePREvent(event *info.Event, comment string) *types.PullRequestEvent {
	iii, _ := strconv.Atoi(event.AccountID)

	pr := &types.PullRequestEvent{
		Actor: types.UserWithLinks{ID: iii, Name: event.Sender},
		PullRequest: types.PullRequest{
			ID: 1,
			ToRef: types.PullRequestRef{
				Repository: types.Repository{
					Project: &types.Project{Key: event.Organization},
					Name:    event.Repository,
					Links: &struct {
						Clone []types.CloneLink `json:"clone,omitempty"`
						Self  []types.SelfLink  `json:"self,omitempty"`
					}{
						Self: []types.SelfLink{
							{
								Href: event.URL,
							},
						},
						Clone: []types.CloneLink{{Href: event.URL}},
					},
				},
				DisplayID:    "base",
				LatestCommit: "abcd",
			},
			FromRef: types.PullRequestRef{
				DisplayID:    "head",
				LatestCommit: event.SHA,
				Repository: types.Repository{
					Project: &types.Project{Key: event.Organization},
					Name:    event.Repository,
					Links: &struct {
						Clone []types.CloneLink `json:"clone,omitempty"`
						Self  []types.SelfLink  `json:"self,omitempty"`
					}{
						Self: []types.SelfLink{
							{
								Href: event.HeadURL,
							},
						},
						Clone: []types.CloneLink{
							{
								Name: "http",
								Href: event.CloneURL,
							},
						},
					},
				},
			},
		},
	}
	if comment != "" {
		pr.Comment = types.ActivityComment{
			Text: comment,
		}
	}
	return pr
}

func MakePushEvent(event *info.Event, changes []types.PushRequestEventChange, commits []types.Commit) *types.PushRequestEvent {
	iii, _ := strconv.Atoi(event.AccountID)

	return &types.PushRequestEvent{
		Actor: types.UserWithLinks{ID: iii, Name: event.Sender},
		Repository: types.Repository{
			Project: &types.Project{
				Key: event.Organization,
			},
			Slug: event.Repository,
			Links: &struct {
				Clone []types.CloneLink `json:"clone,omitempty"`
				Self  []types.SelfLink  `json:"self,omitempty"`
			}{
				Clone: []types.CloneLink{
					{
						Name: "http",
						Href: event.CloneURL,
					},
				},
				Self: []types.SelfLink{
					{
						Href: event.URL,
					},
				},
			},
		},
		Changes: changes,
		Commits: commits,
	}
}

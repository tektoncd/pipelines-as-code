package test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

var defaultAPIURL = "/api/v4"

func Setup(t *testing.T) (*gitlab.Client, *http.ServeMux, func()) {
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

	// server is a test HTTP server used to provide mock API responses.
	server := httptest.NewServer(apiHandler)

	tearDown := func() {
		server.Close()
	}

	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())
	assert.NilError(t, err)
	return client, mux, tearDown
}

func MuxNotePost(t *testing.T, mux *http.ServeMux, projectNumber, mrID int, catchStr string) {
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectNumber, mrID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		bit, _ := io.ReadAll(r.Body)
		s := string(bit)
		if catchStr != "" {
			assert.Assert(t, strings.Contains(s, catchStr), "%s is not in %s", catchStr, s)
		}
		fmt.Fprintf(rw, "{}")
	})
}

func MuxAllowUserID(mux *http.ServeMux, projectID, userID int) {
	path := fmt.Sprintf("/projects/%d/members/all/%d", projectID, userID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"id": %d}`, userID)
	})
}

func MuxDisallowUserID(mux *http.ServeMux, projectID, userID int) {
	path := fmt.Sprintf("/projects/%d/members/all/%d", projectID, userID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{}`)
	})
}

// MuxAllowUserIDCounting registers a handler that returns an allowed member and increments
// the provided counter each time it is called. Useful to assert caching behavior.
func MuxAllowUserIDCounting(mux *http.ServeMux, projectID, userID int, counter *int) {
	path := fmt.Sprintf("/projects/%d/members/all/%d", projectID, userID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		if counter != nil {
			*counter++
		}
		fmt.Fprintf(rw, `{"id": %d}`, userID)
	})
}

func MuxListTektonDir(_ *testing.T, mux *http.ServeMux, pid int, ref, prs string, wantTreeAPIErr, wantFilesAPIErr bool) {
	mux.HandleFunc(fmt.Sprintf("/projects/%d/repository/tree", pid), func(rw http.ResponseWriter, r *http.Request) {
		if wantTreeAPIErr {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("pagination") == "keyset" {
			if r.URL.Query().Get("ref") == ref {
				if r.URL.Query().Get("page_token") != "page2" {
					// Provide a response header pointing to page 2
					rw.Header().Set("Link", fmt.Sprintf("<%s/projects/%d/repository/tree?ref=%s&page_token=page2>; rel=\"next\"", defaultAPIURL, pid, ref))
					// .. and serve page 1
					fmt.Fprintf(rw, `[
					{"name": "random.yaml", "path": "random.yaml"}
					]`)
				} else {
					// Serve page 2
					fmt.Fprintf(rw, `[
					{"name": "pac.yaml", "path": "pr.yaml"}
					]`)
				}
			}
		}
	})

	MuxGetFile(mux, pid, "pr.yaml", prs, wantFilesAPIErr)
	MuxGetFile(mux, pid, "random.yaml", `foo:bar`, wantTreeAPIErr)
}

func MuxDiscussionsNoteEmpty(mux *http.ServeMux, pid, mrID int) {
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/discussions", pid, mrID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `[]`)
	})
}

func MuxMergeRequestNote(mux *http.ServeMux, pid, mrID, noteID, userID int, commentContent, username string) {
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/notes/%d", pid, mrID, noteID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"body": "%s", "author": {"username": "%s", "id": %d}}`, commentContent, username, userID)
	})
}

func MuxDiscussionsNote(mux *http.ServeMux, pid, mrID int, author string, authorID int, notecontent string) {
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/discussions", pid, mrID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, r *http.Request) {
		page, ok := r.URL.Query()["page"]
		if ok && page[0] == "1" {
			rw.Header().Set("X-Next-Page", "2")
			fmt.Fprintf(rw, `[{"notes": [{}]}]`)
			return
		}
		fmt.Fprintf(rw, `[{
            "notes": [{
                "body": "%s",
                "author": {
                    "username": "%s",
                    "id": %d
                }
            }]
        }]
        `, notecontent, author, authorID)
	})
}

// MuxDiscussionsNoteWithReply returns a single discussion where the first note
// does not contain the trigger but a subsequent reply does. This simulates
// /ok-to-test being posted in a thread reply.
func MuxDiscussionsNoteWithReply(mux *http.ServeMux, pid, mrID int, firstAuthor string, firstAuthorID int, firstContent, okAuthor string, okAuthorID int, okContent string) {
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/discussions", pid, mrID)
	mux.HandleFunc(path, func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `[
            {
                "notes": [
                    {
                        "body": %q,
                        "author": {"username": %q, "id": %d}
                    },
                    {
                        "body": %q,
                        "author": {"username": %q, "id": %d}
                    }
                ]
            }
        ]`, firstContent, firstAuthor, firstAuthorID, okContent, okAuthor, okAuthorID)
	})
}

func MuxGetFile(mux *http.ServeMux, pid int, fname, content string, wantErr bool) {
	mux.HandleFunc(fmt.Sprintf("/projects/%d/repository/files/%s/raw", pid, fname), func(rw http.ResponseWriter, _ *http.Request) {
		if wantErr {
			rw.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(rw, content)
	})
}

// SetPagingHeader sets the gitlab paging header "link" to the next page. If the requested page
// is equal to or higher than maxPage, no next page header is set. The requested page number
// is returned.
func SetPagingHeader(t *testing.T, rw http.ResponseWriter, req *http.Request, maxPage int) int {
	t.Helper()
	url := req.URL
	query := url.Query()
	lastPageStr := query.Get("page")
	if lastPageStr == "" {
		lastPageStr = "1"
	}
	lastPage, err := strconv.Atoi(lastPageStr)
	if err != nil {
		t.Errorf("unable to parse page number %s: %v", lastPageStr, err)
		return 0
	}

	if lastPage < maxPage {
		query.Set("page", strconv.Itoa(lastPage+1))
		url.RawQuery = query.Encode()
		header := fmt.Sprintf("<%s>; rel=\"next\",", url.String())
		rw.Header().Add("link", header)
	}

	return lastPage
}

type TEvent struct {
	Username, DefaultBranch, URL, SHA, SHAurl, SHAtitle, Headbranch, Basebranch, HeadURL, BaseURL string
	UserID, MRID, TargetProjectID, SourceProjectID, NoteID                                        int
	PathWithNameSpace, Comment                                                                    string
}

func (t TEvent) PushEventAsJSON(withcommits bool) string {
	jeez := fmt.Sprintf(`{
    "user_username": "%s",
    "project_id": %d,
    "user_id": %d,
    "ref": "%s",
    "project": {
        "default_branch": "%s",
        "web_url": "%s",
        "path_with_namespace": "%s"
    }
`, t.Username, t.TargetProjectID, t.UserID, t.Basebranch, t.DefaultBranch, t.URL, t.PathWithNameSpace)

	if withcommits {
		jeez += fmt.Sprintf(`,
"commits": [
        {
            "id": "%s",
            "url": "%s",
            "title": "%s"
        }
    ]`, t.SHA, t.SHAurl, t.SHAtitle)
	}
	jeez += "}"
	return jeez
}

func (t TEvent) PushEventWithoutCommitsAsJSON(before, after, checkoutSHA, ref string) string {
	return fmt.Sprintf(`{
    "object_kind": "push",
    "event_name": "push",
    "before": "%s",
    "after": "%s",
    "ref": "%s",
    "checkout_sha": "%s",
    "user_username": "%s",
    "project_id": %d,
    "user_id": %d,
    "project": {
        "default_branch": "%s",
        "web_url": "%s",
        "path_with_namespace": "%s"
    },
    "commits": [],
    "total_commits_count": 0
}`, before, after, ref, checkoutSHA, t.Username, t.TargetProjectID, t.UserID, t.DefaultBranch, t.URL, t.PathWithNameSpace)
}

func (t TEvent) NoteEventAsJSON(comment string) string {
	//nolint:misspell
	return fmt.Sprintf(`{
	"object_kind": "note",
	"event_type": "note",
    "object_attributes": {
        "noteable_type": "MergeRequest",
	    "note": "%s"
    },
    "user": {
        "username": "%s"
    },
    "project": {
        "default_branch": "%s",
        "web_url": "%s",
        "path_with_namespace": "%s"
    },
    "merge_request": {
	    "state": "opened",
        "iid": %d,
        "target_project_id": %d,
        "source_project_id": %d,
        "target_branch": "%s",
        "source_branch": "%s",
        "last_commit": {
            "id": "%s",
            "url": "%s://gitlab.com/pac-chmou/pac-group/-/commit/e29c24f4b6000cf0c2eae3b8bc588cb855b67756",
            "title": "%s",
            "message": "%s"
        },
		"target": {
			"web_url": "%s"
		},
		"source": {
			"web_url": "%s"
		}
    }
}`, comment, t.Username, t.DefaultBranch, t.URL, t.PathWithNameSpace, t.MRID, t.TargetProjectID, t.SourceProjectID, t.Basebranch, t.Headbranch, t.SHA, t.SHAurl, t.SHAtitle, t.SHAtitle, t.BaseURL, t.HeadURL)
}

// MREventAsJSON returns a JSON string representing the Merge Request event.
// It includes information about the user, project, and object attributes such as action, iid, source project id, title, source branch, target branch, last commit, target path with namespace, target web url, and source web url.
func (t TEvent) MREventAsJSON(action, extraStuff string) string {
	if extraStuff != "" {
		extraStuff = "," + extraStuff
	}
	return fmt.Sprintf(`{
    "user": {
        "id": %d,
        "username": "%s"
    },
    "project": {
        "id": %d,
        "web_url": "%s",
        "default_branch": "%s"
    },
    "object_attributes": {
		"action": "%s",
        "iid": %d,
        "source_project_id": %d,
        "title": "%s",
        "source_branch": "%s",
        "target_branch": "%s",
        "last_commit": {
            "id": "%s",
            "url": "%s"
        },
		"target": {
			"path_with_namespace": "%s",
			"web_url": "%s"
		},
		"source": {
			"web_url": "%s"
		}%s
    }
}`, t.UserID, t.Username, t.TargetProjectID, t.URL, t.DefaultBranch, action, t.MRID,
		t.SourceProjectID, t.SHAtitle, t.Headbranch, t.Basebranch, t.SHA, t.SHAurl, t.PathWithNameSpace,
		t.BaseURL,
		t.HeadURL, extraStuff)
}

func (t TEvent) CommitNoteEventAsJSON(comment, action, repository string) string {
	//nolint:misspell
	return fmt.Sprintf(`{
	"object_kind": "note",
	"event_type": "note",
    "object_attributes": {
	    "commit_id": "%s",
        "noteable_type": "Commit",
	    "note": "%s",
	    "action": "%s"
    },
    "user": {
	    "id": %d,
        "username": "%s"
    },
	"project_id": %d,
    "project": {
        "default_branch": "%s",
        "web_url": "%s",
        "path_with_namespace": "%s"
    },
    "repository": %s,
    "commit": {
        "title": "test title"
    }
}`, t.SHA, comment, action, t.UserID, t.Username, t.SourceProjectID, t.DefaultBranch, t.URL, t.PathWithNameSpace, repository)
}

func (t TEvent) MergeCommentEventAsJSON(comment string) string {
	return fmt.Sprintf(`{
	"object_kind": "note",
	"event_type": "note",
	"object_attributes": {
		"id": %d,
		"note": "%s"
	},
	"user": {
		"id": %d,
		"username": "%s"
	},
	"project": {
		"id": %d,
		"web_url": "%s"
	},
	"merge_request": {
		"iid": %d,
		"target_project_id": %d,
		"source_project_id": %d,
		"target_branch": "%s",
		"source_branch": "%s"
	}
}`, t.NoteID, comment, t.UserID, t.Username, t.TargetProjectID, t.URL, t.MRID, t.TargetProjectID, t.SourceProjectID, t.Basebranch, t.Headbranch)
}

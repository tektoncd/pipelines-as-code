package test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/xanzy/go-gitlab"
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

	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(server.URL))
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

func MuxListTektonDir(_ *testing.T, mux *http.ServeMux, pid int, ref, prs string) {
	mux.HandleFunc(fmt.Sprintf("/projects/%d/repository/tree", pid), func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") == ref {
			fmt.Fprintf(rw, `[
			{"name": "pac.yaml", "path": ".tekton/subtree/pr.yaml"},
			{"name": "random.yaml", "path": ".tekton/random.yaml"}
			]`)
		}
	})

	MuxGetFile(mux, pid, ".tekton/subtree/pr.yaml", prs)
	MuxGetFile(mux, pid, ".tekton/random.yaml", `foo:bar`)
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

func MuxGetFile(mux *http.ServeMux, pid int, fname, content string) {
	escapedFname := strings.ReplaceAll(url.PathEscape(fname), ".", "%2E")
	mux.HandleFunc(fmt.Sprintf("/projects/%d/repository/files/%s/raw", pid, escapedFname), func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, content)
	})
}

type TEvent struct {
	Username, DefaultBranch, URL, SHA, SHAurl, SHAtitle, Headbranch, Basebranch, HeadURL, BaseURL string
	UserID, MRID, TargetProjectID, SourceProjectID                                                int
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

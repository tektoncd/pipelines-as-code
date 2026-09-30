package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func computeHMACSHA256(payload, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestProviderValidate(t *testing.T) {
	testPayload := []byte(`{"ref":"refs/heads/main"}`)
	testSecret := "mysecret"
	validSignature := computeHMACSHA256(testPayload, []byte(testSecret))

	tests := []struct {
		name            string
		signatureHeader string
		signature       string
		secret          string
		payload         []byte
		wantErr         string
	}{
		{
			name:            "valid forgejo signature",
			signatureHeader: ForgejoSignatureHeader,
			signature:       validSignature,
			secret:          testSecret,
			payload:         testPayload,
		},
		{
			name:            "valid gitea signature",
			signatureHeader: GiteaSignatureHeader,
			signature:       validSignature,
			secret:          testSecret,
			payload:         testPayload,
		},
		{
			name:            "invalid signature mismatch",
			signatureHeader: ForgejoSignatureHeader,
			signature:       computeHMACSHA256([]byte("wrong payload"), []byte(testSecret)),
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "gitea/forgejo webhook signature validation failed",
		},
		{
			name:            "invalid hex in signature",
			signatureHeader: ForgejoSignatureHeader,
			signature:       "not-valid-hex!@#$",
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "gitea/forgejo webhook signature is not valid hex",
		},
		{
			name:            "signature present but no secret configured",
			signatureHeader: ForgejoSignatureHeader,
			signature:       validSignature,
			secret:          "",
			payload:         testPayload,
			wantErr:         "no webhook secret has been set, in repository CR or secret",
		},
		{
			name:            "secret configured but no signature",
			signatureHeader: "",
			signature:       "",
			secret:          testSecret,
			payload:         testPayload,
			wantErr:         "no signature has been detected, for security reason we are not allowing webhooks without a secret",
		},
		{
			name:            "no secret and no signature",
			signatureHeader: "",
			signature:       "",
			secret:          "",
			payload:         testPayload,
			wantErr:         "no signature has been detected, for security reason we are not allowing webhooks without a secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			header := http.Header{}
			if tt.signatureHeader != "" && tt.signature != "" {
				header.Set(tt.signatureHeader, tt.signature)
			}

			event := &info.Event{
				Provider: &info.Provider{
					WebhookSecret: tt.secret,
				},
				Request: &info.Request{
					Header:  header,
					Payload: tt.payload,
				},
			}

			p := &Provider{Logger: logger}
			err := p.Validate(context.Background(), nil, event)

			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error but got nil")
				assert.Assert(t, strings.Contains(err.Error(), tt.wantErr),
					"expected error to contain %q, got %q", tt.wantErr, err.Error())
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

// forgejo-sdk can return 200 with content:null (e.g. for non-file paths); error must contain "cannot find".
func TestProviderGetFileInsideRepo(t *testing.T) {
	const validContent = `{"name":"OWNERS","path":"OWNERS","type":"file","content":"YXBwcm92ZXJzOgogIC0gdXNlcgo="}`
	const wantOwners = "approvers:\n  - user\n"
	tests := []struct {
		name         string
		contentsResp string
		target       string
		provenance   string
		wantContent  string
		wantRef      string
		errContains  string
	}{
		{
			name:         "content field is null does not panic",
			contentsResp: `{"name":"OWNERS","path":"OWNERS","type":"dir","content":null}`,
			target:       "default-branch",
			errContains:  "cannot find",
		},
		{
			name:         "null response body yields content nil without panic",
			contentsResp: `null`,
			target:       "default-branch",
			errContains:  "cannot find",
		},
		{
			name:         "valid file content is decoded",
			contentsResp: validContent,
			target:       "default-branch",
			wantContent:  wantOwners,
			wantRef:      "default-branch",
		},
		{
			name:         "an explicit target wins over everything else",
			contentsResp: validContent,
			target:       "explicit-target",
			provenance:   "default_branch",
			wantContent:  wantOwners,
			wantRef:      "explicit-target",
		},
		{
			name:         "default_branch provenance resolves from the default branch",
			contentsResp: validContent,
			provenance:   "default_branch",
			wantContent:  wantOwners,
			wantRef:      "default-branch",
		},
		{
			name:         "without provenance it falls back to the event SHA",
			contentsResp: validContent,
			wantContent:  wantOwners,
			wantRef:      "sha123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			provider := &Provider{giteaClient: fakeclient, provenance: tt.provenance}

			event := info.NewEvent()
			event.Organization = "myorg"
			event.Repository = "myrepo"
			event.SHA = "sha123"
			event.DefaultBranch = "default-branch"
			// deliberately distinct from every expected ref so a regression to
			// the old `ref = runevent.BaseBranch` cannot pass
			event.BaseBranch = "base-branch"

			gotRef := ""
			mux.HandleFunc("/repos/myorg/myrepo/contents/OWNERS",
				func(rw http.ResponseWriter, r *http.Request) {
					// forgejo puts the ref in the query string, not the path:
					// /repos/%s/%s/contents/%s?ref=%s
					gotRef = r.URL.Query().Get("ref")
					fmt.Fprint(rw, tt.contentsResp)
				})

			got, err := provider.GetFileInsideRepo(context.Background(), event, "OWNERS", tt.target)
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantContent, got)
			assert.Equal(t, tt.wantRef, gotRef, "unexpected ref sent to the contents API")
		})
	}
}

func TestCreateComment(t *testing.T) {
	tests := []struct {
		name          string
		event         *info.Event
		commit        string
		updateMarker  string
		mockResponses map[string]func(rw http.ResponseWriter, _ *http.Request)
		wantErr       string
		clientNil     bool
	}{
		{
			name:      "nil client error",
			clientNil: true,
			event:     &info.Event{PullRequestNumber: 123},
			wantErr:   "no gitea client has been initialized",
		},
		{
			name:    "not a pull request error",
			event:   &info.Event{PullRequestNumber: 0},
			wantErr: "create comment only works on pull requests",
		},
		{
			name:         "create new comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "New Comment",
			updateMarker: "",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPost, r.Method)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "update existing comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "MARKER", "user": {"id": 100}}]`)
						return
					}
				},
				"/repos/org/repo/issues/comments/555": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "PATCH", r.Method)
					rw.WriteHeader(http.StatusOK)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "no matching comment creates new",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "New Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "NO_MATCH", "user": {"id": 200}}]`)
						return
					}
					assert.Equal(t, http.MethodPost, r.Method)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
			},
		},
		{
			name:         "skip comment from different user and create new",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			commit:       "Updated Comment",
			updateMarker: "MARKER",
			mockResponses: map[string]func(rw http.ResponseWriter, _ *http.Request){
				"/user": func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				"/repos/org/repo/issues/123/comments": func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "Old MARKER", "user": {"id": 999}}]`)
						return
					}
					assert.Equal(t, http.MethodPost, r.Method)
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{}`)
				},
				"/repos/org/repo/issues/comments/555": func(rw http.ResponseWriter, _ *http.Request) {
					t.Error("edit endpoint should not be called for comment from different user")
					rw.WriteHeader(http.StatusOK)
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			if tt.clientNil {
				p := &Provider{}
				err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			for endpoint, handler := range tt.mockResponses {
				mux.HandleFunc(endpoint, handler)
			}

			p := &Provider{giteaClient: fakeclient, Logger: fakelogger}
			err := p.CreateComment(context.Background(), tt.event, tt.commit, tt.updateMarker)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

// TestProviderGetFilesPagination exercises the SDK and provider against HTTP
// fixtures. The request budget turns infinite pagination into a bounded failure.
func TestProviderGetFilesPagination(t *testing.T) {
	tests := []struct {
		name      string
		pageCount string
		perPage   string
		total     string
		bodies    []string
		wantPages []string
		want      changedfiles.ChangedFiles
	}{
		{
			name:      "empty response with zero pages",
			pageCount: "0",
			perPage:   "20",
			total:     "0",
			bodies:    []string{`[]`},
			wantPages: []string{"1"},
		},
		{
			name:      "three pages despite a server cap below the requested limit",
			pageCount: "3",
			perPage:   "1",
			total:     "3",
			bodies: []string{
				`[{"filename":"first.txt","status":"added"}]`,
				`[{"filename":"second.txt","status":"changed"}]`,
				`[{"filename":"third.txt","status":"deleted"}]`,
			},
			wantPages: []string{"1", "2", "3"},
			want: changedfiles.ChangedFiles{
				All:      []string{"first.txt", "second.txt", "third.txt"},
				Added:    []string{"first.txt"},
				Modified: []string{"second.txt"},
				Deleted:  []string{"third.txt"},
			},
		},
		{
			name:      "single last page stops",
			pageCount: "1",
			perPage:   "20",
			total:     "1",
			bodies:    []string{`[{"filename":"only.txt","status":"added"}]`},
			wantPages: []string{"1"},
			want: changedfiles.ChangedFiles{
				All:   []string{"only.txt"},
				Added: []string{"only.txt"},
			},
		},
	}
	for _, trigger := range []triggertype.Trigger{triggertype.PullRequest, triggertype.PullRequestClosed} {
		for _, tt := range tests {
			t.Run(trigger.String()+"/"+tt.name, func(t *testing.T) {
				client, mux, teardown := tgitea.Setup(t)
				defer teardown()

				var mu sync.Mutex
				var pages []string
				mux.HandleFunc("/repos/example/repo/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
					page := r.URL.Query().Get("page")
					mu.Lock()
					pages = append(pages, page)
					request := len(pages)
					mu.Unlock()
					if request > len(tt.wantPages) {
						http.Error(w, "pagination exceeded the expected request budget", http.StatusBadRequest)
						return
					}
					if r.Method != http.MethodGet || r.URL.Query().Get("limit") != "50" || page != tt.wantPages[request-1] {
						http.Error(w, "unexpected PR files request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Page", page)
					w.Header().Set("X-Pagecount", tt.pageCount)
					w.Header().Set("X-Perpage", tt.perPage)
					w.Header().Set("X-Total-Count", tt.total)
					w.Header().Set("X-Hasmore", "false")
					if request < len(tt.wantPages) {
						w.Header().Set("X-Hasmore", "true")
					}
					fmt.Fprint(w, tt.bodies[request-1])
				})

				p := Provider{
					giteaClient:      client,
					Logger:           zap.NewNop().Sugar(),
					repo:             &v1alpha1.Repository{},
					giteaInstanceURL: "https://example.test",
					triggerEvent:     trigger.String(),
				}
				got, err := p.GetFiles(context.Background(), &info.Event{
					Organization:      "example",
					Repository:        "repo",
					PullRequestNumber: 1,
					TriggerTarget:     trigger,
				})
				mu.Lock()
				gotPages := append([]string(nil), pages...)
				mu.Unlock()
				assert.DeepEqual(t, gotPages, tt.wantPages)
				assert.NilError(t, err)
				assert.DeepEqual(t, got, tt.want)
			})
		}
	}
}

func TestShouldGetNextPageBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		pageCount   string
		currentPage int
		wantMore    bool
		wantPage    int
	}{
		{name: "zero pages", pageCount: "0", currentPage: 1, wantMore: false, wantPage: 0},
		{name: "first of three pages", pageCount: "3", currentPage: 1, wantMore: true, wantPage: 2},
		{name: "middle of three pages", pageCount: "3", currentPage: 2, wantMore: true, wantPage: 3},
		{name: "last of three pages", pageCount: "3", currentPage: 3, wantMore: false, wantPage: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &forgejo.Response{Response: &http.Response{
				Header: http.Header{"X-Pagecount": []string{tt.pageCount}},
			}}
			more, page := ShouldGetNextPage(resp, tt.currentPage)
			assert.Equal(t, more, tt.wantMore)
			assert.Equal(t, page, tt.wantPage)
		})
	}
}

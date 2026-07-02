package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
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
					assert.Equal(t, r.Method, http.MethodPost)
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
					assert.Equal(t, r.Method, "PATCH")
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
					assert.Equal(t, r.Method, http.MethodPost)
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
					assert.Equal(t, r.Method, http.MethodPost)
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

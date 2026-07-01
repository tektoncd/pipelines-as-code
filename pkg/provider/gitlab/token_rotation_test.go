package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	thelp "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab/test"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testlogger "github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestIsTokenAutoRotationEnabled(t *testing.T) {
	repoWithSecret := func(settings *v1alpha1.Settings) *v1alpha1.Repository {
		return &v1alpha1.Repository{
			Spec: v1alpha1.RepositorySpec{
				GitProvider: &v1alpha1.GitProvider{
					Secret: &v1alpha1.Secret{Name: "gitlab-token"},
				},
				Settings: settings,
			},
		}
	}

	tests := []struct {
		name     string
		repo     *v1alpha1.Repository
		expected bool
	}{
		{
			name:     "nil repo",
			repo:     nil,
			expected: false,
		},
		{
			name: "nil git provider",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{},
			},
			expected: false,
		},
		{
			name: "nil git provider secret",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{},
				},
			},
			expected: false,
		},
		{
			name: "empty git provider secret name",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{
						Secret: &v1alpha1.Secret{},
					},
				},
			},
			expected: false,
		},
		{
			name:     "nil settings",
			repo:     repoWithSecret(nil),
			expected: false,
		},
		{
			name:     "nil gitlab settings",
			repo:     repoWithSecret(&v1alpha1.Settings{}),
			expected: false,
		},
		{
			name: "nil token auto rotation",
			repo: repoWithSecret(&v1alpha1.Settings{
				Gitlab: &v1alpha1.GitlabSettings{},
			}),
			expected: false,
		},
		{
			name: "explicitly true",
			repo: repoWithSecret(&v1alpha1.Settings{
				Gitlab: &v1alpha1.GitlabSettings{
					TokenAutoRotation: boolPtr(true),
				},
			}),
			expected: true,
		},
		{
			name: "explicitly false",
			repo: repoWithSecret(&v1alpha1.Settings{
				Gitlab: &v1alpha1.GitlabSettings{
					TokenAutoRotation: boolPtr(false),
				},
			}),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Provider{repo: tt.repo}
			assert.Equal(t, tt.expected, v.isTokenAutoRotationEnabled())
		})
	}
}

func TestGetRepositoryLock(t *testing.T) {
	first := getRepositoryLock(t.Name() + "/repo")
	second := getRepositoryLock(t.Name() + "/repo")
	other := getRepositoryLock(t.Name() + "/other-repo")

	assert.Assert(t, first == second, "same repository should use the same token rotation lock")
	assert.Assert(t, first != other, "different repositories should not share the same token rotation lock")
}

func TestNeedsRotation(t *testing.T) {
	expiringIn3Days := gitlab.ISOTime(time.Now().Add(3 * 24 * time.Hour))
	expiringIn30Days := gitlab.ISOTime(time.Now().Add(30 * 24 * time.Hour))
	expiredYesterday := gitlab.ISOTime(time.Now().Add(-24 * time.Hour))

	tests := []struct {
		name     string
		pat      *gitlab.PersonalAccessToken
		expected bool
	}{
		{
			name:     "no expiry",
			pat:      &gitlab.PersonalAccessToken{Active: true, ExpiresAt: nil},
			expected: false,
		},
		{
			name:     "not active",
			pat:      &gitlab.PersonalAccessToken{Active: false, ExpiresAt: &expiredYesterday},
			expected: false,
		},
		{
			name:     "expires within threshold",
			pat:      &gitlab.PersonalAccessToken{Active: true, ExpiresAt: &expiringIn3Days},
			expected: true,
		},
		{
			name:     "expires after threshold",
			pat:      &gitlab.PersonalAccessToken{Active: true, ExpiresAt: &expiringIn30Days},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, needsRotation(tt.pat))
		})
	}
}

func TestMaybeRotateToken(t *testing.T) {
	expiringIn3Days := time.Now().Add(3 * 24 * time.Hour).Format("2006-01-02")
	expiringIn30Days := time.Now().Add(30 * 24 * time.Hour).Format("2006-01-02")
	newExpiry := time.Now().Add(rotationNewExpiry).Format("2006-01-02")

	tests := []struct {
		name             string
		setup            func(t *testing.T, mux *http.ServeMux)
		wantNewToken     bool
		wantErr          bool
		wantSecretUpdate bool
		wantErrContains  string
		nilSecretData    bool
	}{
		{
			name: "token not expiring soon",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn30Days)
				})
			},
			wantNewToken: false,
		},
		{
			name: "token no expiry",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 1, "active": true, "expires_at": null}`)
				})
			},
			wantNewToken: false,
		},
		{
			name: "token expiring soon and rotated",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
						return
					}
					fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": %q}`, newExpiry)
				})
				mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": %q}`, newExpiry)
				})
			},
			wantNewToken:     true,
			wantSecretUpdate: true,
		},
		{
			name: "token expiring soon and rotated with nil secret data",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
						return
					}
					fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": %q}`, newExpiry)
				})
				mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": %q}`, newExpiry)
				})
			},
			wantNewToken:     true,
			wantSecretUpdate: true,
			nilSecretData:    true,
		},
		{
			name: "token expiring soon and rotated without expiry",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
						return
					}
					fmt.Fprint(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": null}`)
				})
				mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 2, "active": true, "token": "new-rotated-token", "expires_at": null}`)
				})
			},
			wantNewToken:     true,
			wantSecretUpdate: true,
		},
		{
			name: "introspection fails",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: true,
		},
		{
			name: "token expired and unauthorized",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusUnauthorized)
				})
			},
			wantErr: true,
		},
		{
			name: "rotation returns 403 missing scope",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
						return
					}
					rw.WriteHeader(http.StatusForbidden)
					fmt.Fprint(rw, `{"message": "403 Forbidden"}`)
				})
				mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusForbidden)
					fmt.Fprint(rw, `{"message": "403 Forbidden"}`)
				})
			},
			wantErr:         true,
			wantErrContains: "self_rotate",
		},
		{
			name: "PAT rotation fails with 405, fallback to project token",
			setup: func(_ *testing.T, mux *http.ServeMux) {
				mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
						return
					}
					rw.WriteHeader(http.StatusMethodNotAllowed)
				})
				mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusMethodNotAllowed)
				})
				mux.HandleFunc("/projects/123/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(rw, `{"id": 3, "active": true, "token": "new-project-token", "expires_at": %q}`, newExpiry)
				})
			},
			wantNewToken:     true,
			wantSecretUpdate: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenIntrospectionCache.Clear()
			ctx, _ := rtesting.SetupFakeContext(t)
			logger, _ := testlogger.GetLogger()
			client, mux, tearDown := thelp.Setup(t)
			defer tearDown()

			secretData := map[string][]byte{
				"provider.token": []byte("old-token"),
			}
			if tt.nilSecretData {
				secretData = nil
			}

			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "gitlab-token",
							Namespace: "default",
						},
						Data: secretData,
					},
				},
			})

			run := &params.Run{
				Clients: clients.Clients{
					Kube: stdata.Kube,
					Log:  logger,
				},
			}

			v := &Provider{
				Logger:          logger,
				run:             run,
				sourceProjectID: 123,
				targetProjectID: 123,
				eventEmitter:    events.NewEventEmitter(stdata.Kube, logger),
				repo: &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-repo",
						Namespace: "default",
					},
					Spec: v1alpha1.RepositorySpec{
						GitProvider: &v1alpha1.GitProvider{
							Secret: &v1alpha1.Secret{
								Name: "gitlab-token",
							},
						},
					},
				},
			}
			v.SetGitLabClient(client)
			tt.setup(t, mux)

			newToken, err := v.maybeRotateToken(ctx)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
				return
			}
			assert.NilError(t, err)

			if tt.wantNewToken {
				assert.Assert(t, newToken != "", "expected new token but got empty")
			} else {
				assert.Equal(t, "", newToken)
			}

			if tt.wantSecretUpdate {
				secret, err := stdata.Kube.CoreV1().Secrets("default").Get(ctx, "gitlab-token", metav1.GetOptions{})
				assert.NilError(t, err)
				actualToken := string(secret.Data["provider.token"])
				assert.Assert(t, actualToken != "old-token", "secret should have been updated")
				assert.Equal(t, newToken, actualToken)
			}
		})
	}
}

func TestMaybeRotateTokenSecretUpdateFails(t *testing.T) {
	tokenIntrospectionCache.Clear()
	expiringIn3Days := time.Now().Add(3 * 24 * time.Hour).Format("2006-01-02")
	newExpiry := time.Now().Add(rotationNewExpiry).Format("2006-01-02")

	ctx, _ := rtesting.SetupFakeContext(t)
	logger, observer := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	// No secret seeded — the update will fail because the secret doesn't exist
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})

	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  logger,
		},
	}

	v := &Provider{
		Logger:          logger,
		run:             run,
		sourceProjectID: 123,
		eventEmitter:    events.NewEventEmitter(stdata.Kube, logger),
		repo: &v1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-repo",
				Namespace: "default",
			},
			Spec: v1alpha1.RepositorySpec{
				GitProvider: &v1alpha1.GitProvider{
					Secret: &v1alpha1.Secret{
						Name: "gitlab-token",
					},
				},
			},
		},
	}
	v.SetGitLabClient(client)

	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
			return
		}
		fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-token", "expires_at": %q}`, newExpiry)
	})
	mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-token", "expires_at": %q}`, newExpiry)
	})

	_, err := v.maybeRotateToken(ctx)
	assert.Assert(t, err != nil, "expected error for failed secret update")
	assert.ErrorContains(t, err, "secret update failed")

	criticalLogs := observer.FilterMessageSnippet("CRITICAL")
	assert.Assert(t, criticalLogs.Len() > 0, "should have logged CRITICAL message")
}

func TestSetClientReturnsErrorWhenRotatedTokenCannotBeStored(t *testing.T) {
	expiringIn3Days := time.Now().Add(3 * 24 * time.Hour).Format("2006-01-02")
	newExpiry := time.Now().Add(rotationNewExpiry).Format("2006-01-02")

	ctx, _ := rtesting.SetupFakeContext(t)
	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  logger,
		},
	}

	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
			return
		}
		fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-token", "expires_at": %q}`, newExpiry)
	})
	mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"id": 2, "active": true, "token": "new-token", "expires_at": %q}`, newExpiry)
	})

	v := &Provider{Logger: logger}
	v.SetGitLabClient(client)
	event := &info.Event{
		Provider: &info.Provider{
			Token: "old-token",
		},
		TriggerTarget: triggertype.Push,
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "gitlab-token",
				},
			},
			Settings: &v1alpha1.Settings{
				Gitlab: &v1alpha1.GitlabSettings{
					TokenAutoRotation: boolPtr(true),
				},
			},
		},
	}

	err := v.SetClient(ctx, run, event, repo, events.NewEventEmitter(stdata.Kube, logger))
	assert.ErrorContains(t, err, "gitlab token auto-rotation failed")
	assert.ErrorContains(t, err, "secret update failed")
	assert.Equal(t, "old-token", event.Provider.Token)
}

func TestIntrospectToken(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantActive  bool
		wantTokenID int64
	}{
		{
			name:        "valid active token",
			statusCode:  http.StatusOK,
			body:        `{"id": 42, "active": true, "scopes": ["api"], "expires_at": "2025-12-31"}`,
			wantActive:  true,
			wantTokenID: 42,
		},
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			wantErr:    true,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := testlogger.GetLogger()
			client, mux, tearDown := thelp.Setup(t)
			defer tearDown()

			v := &Provider{Logger: logger}
			v.SetGitLabClient(client)

			mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
				if tt.statusCode != http.StatusOK {
					rw.WriteHeader(tt.statusCode)
					return
				}
				fmt.Fprint(rw, tt.body)
			})

			pat, err := v.introspectToken()
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantActive, pat.Active)
			assert.Equal(t, tt.wantTokenID, pat.ID)
		})
	}
}

func TestRotateTokenFallbackToProjectToken(t *testing.T) {
	newExpiry := time.Now().Add(rotationNewExpiry).Format("2006-01-02")

	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	v := &Provider{
		Logger:          logger,
		sourceProjectID: 123,
		targetProjectID: 456,
	}
	v.SetGitLabClient(client)

	// PAT rotation returns 405 (not a personal access token)
	mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(rw, `{"message": "405 Method Not Allowed"}`)
	})

	sourceProjectFallbackCalled := false
	mux.HandleFunc("/projects/123/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		sourceProjectFallbackCalled = true
		rw.WriteHeader(http.StatusInternalServerError)
	})

	// Project token rotation succeeds
	mux.HandleFunc("/projects/456/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"id":         3,
			"active":     true,
			"token":      "project-token-rotated",
			"expires_at": newExpiry,
		}
		b, _ := json.Marshal(resp)
		_, _ = rw.Write(b)
	})

	pat, err := v.rotateToken()
	assert.NilError(t, err)
	assert.Equal(t, "project-token-rotated", pat.Token)
	assert.Assert(t, !sourceProjectFallbackCalled, "source project should not be used for project token rotation")
}

func TestRotateTokenSkipsProjectFallbackWithoutTargetProjectID(t *testing.T) {
	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	v := &Provider{
		Logger:          logger,
		sourceProjectID: 456,
	}
	v.SetGitLabClient(client)

	mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(rw, `{"message": "405 Method Not Allowed"}`)
	})

	projectFallbackCalled := false
	mux.HandleFunc("/projects/456/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		projectFallbackCalled = true
		rw.WriteHeader(http.StatusInternalServerError)
	})

	_, err := v.rotateToken()
	assert.Assert(t, err != nil, "expected rotation error")
	assert.ErrorContains(t, err, "rotate token")
	assert.Assert(t, !projectFallbackCalled, "project token fallback should not run without target project id")
}

func TestSetClientSkipsTokenAutoRotationForGlobalRepositorySecret(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  logger,
		},
	}

	introspectionCalled := false
	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
		introspectionCalled = true
		fmt.Fprint(rw, `{"id": 1, "active": true, "expires_at": "2000-01-01"}`)
	})

	v := &Provider{Logger: logger}
	v.SetGitLabClient(client)

	event := &info.Event{
		Provider: &info.Provider{
			Token:                           "global-token",
			GitProviderSecretNamespace:      "global-ns",
			GitProviderSecretFromGlobalRepo: true,
		},
		TriggerTarget:   triggertype.Push,
		SourceProjectID: 123,
		TargetProjectID: 123,
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "global-token-secret",
				},
			},
		},
	}

	err := v.SetClient(ctx, run, event, repo, events.NewEventEmitter(stdata.Kube, logger))
	assert.NilError(t, err)
	assert.Assert(t, !introspectionCalled, "global Repository secret should not be introspected for auto-rotation")
	assert.Equal(t, "global-token", event.Provider.Token)
}

func TestSetClientSkipsTokenAutoRotationWithoutRepositorySecret(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  logger,
		},
	}

	introspectionCalled := false
	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
		introspectionCalled = true
		fmt.Fprint(rw, `{"id": 1, "active": true, "expires_at": "2000-01-01"}`)
	})

	v := &Provider{Logger: logger}
	v.SetGitLabClient(client)

	event := &info.Event{
		Provider: &info.Provider{
			Token: "gitlab-token",
		},
		TriggerTarget: triggertype.Push,
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
		Spec: v1alpha1.RepositorySpec{},
	}

	err := v.SetClient(ctx, run, event, repo, events.NewEventEmitter(stdata.Kube, logger))
	assert.NilError(t, err)
	assert.Assert(t, !introspectionCalled, "token auto-rotation should not run without a Repository git_provider.secret")
	assert.Equal(t, "gitlab-token", event.Provider.Token)
}

func TestSetClientProjectTokenFallbackUsesTargetProjectID(t *testing.T) {
	expiringIn3Days := time.Now().Add(3 * 24 * time.Hour).Format("2006-01-02")
	newExpiry := time.Now().Add(rotationNewExpiry).Format("2006-01-02")

	ctx, _ := rtesting.SetupFakeContext(t)
	logger, _ := testlogger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitlab-token",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"provider.token": []byte("old-token"),
				},
			},
		},
	})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: stdata.Kube,
			Log:  logger,
		},
	}

	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"id": 1, "active": true, "expires_at": %q}`, expiringIn3Days)
	})
	mux.HandleFunc("/personal_access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusMethodNotAllowed)
	})

	sourceProjectFallbackCalled := false
	mux.HandleFunc("/projects/111/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		sourceProjectFallbackCalled = true
		rw.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/projects/222/access_tokens/self/rotate", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"id": 3, "active": true, "token": "target-project-token", "expires_at": %q}`, newExpiry)
	})

	v := &Provider{Logger: logger}
	v.SetGitLabClient(client)
	event := &info.Event{
		Provider: &info.Provider{
			Token: "old-token",
		},
		TriggerTarget:   triggertype.Push,
		SourceProjectID: 111,
		TargetProjectID: 222,
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "gitlab-token",
				},
			},
			Settings: &v1alpha1.Settings{
				Gitlab: &v1alpha1.GitlabSettings{
					TokenAutoRotation: boolPtr(true),
				},
			},
		},
	}

	err := v.SetClient(ctx, run, event, repo, events.NewEventEmitter(stdata.Kube, logger))
	assert.NilError(t, err)
	assert.Assert(t, !sourceProjectFallbackCalled, "source project should not be used for project token rotation")
	assert.Equal(t, "target-project-token", event.Provider.Token)

	secret, err := stdata.Kube.CoreV1().Secrets("default").Get(ctx, "gitlab-token", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, "target-project-token", string(secret.Data["provider.token"]))
}

package matcher

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	mainBranch            = "mainBranch"
	targetNamespace       = "targetNamespace"
	targetOldestNamespace = "targetOldestNamespace"
	targetURL             = "https//nowhere.togo"
)

func TestGetRepoByCR(t *testing.T) {
	cw := clockwork.NewFakeClock()
	type args struct {
		data     testclient.Data
		runevent info.Event
	}
	tests := []struct {
		name         string
		args         args
		wantTargetNS string
		wantErr      bool
	}{
		{
			name: "test-match",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-1 * time.Minute)},
							},
						),
					},
				},
				runevent: info.Event{URL: targetURL, BaseBranch: mainBranch, EventType: "pull_request"},
			},
			wantTargetNS: targetNamespace,
			wantErr:      false,
		},
		{
			name: "test-match-url-slash-at-the-end",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              "https//nowhere.togo/",
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
				runevent: info.Event{URL: targetURL, BaseBranch: mainBranch, EventType: "pull_request"},
			},
			wantTargetNS: targetNamespace,
			wantErr:      false,
		},
		{
			name: "test-nomatch-url",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              "http://nottarget.url",
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
				runevent: info.Event{URL: targetURL, BaseBranch: mainBranch, EventType: "pull_request"},
			},
			wantTargetNS: "",
			wantErr:      false,
		},
		{
			name: "straightforward-branch",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
				runevent: info.Event{
					URL: targetURL, BaseBranch: "refs/heads/mainBranch",
					EventType: "pull_request",
				},
			},
			wantTargetNS: targetNamespace,
			wantErr:      false,
		},
		{
			name: "test-multiple-match-get-oldest",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-new",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-1 * time.Minute)},
							},
						),
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-old",
								URL:              targetURL,
								InstallNamespace: targetOldestNamespace,
								CreateTime:       metav1.Time{Time: cw.Now().Add(-5 * time.Minute)},
							},
						),
					},
				},
				runevent: info.Event{URL: targetURL, BaseBranch: mainBranch, EventType: "pull_request"},
			},
			wantTargetNS: targetOldestNamespace,
			wantErr:      false,
		},
		{
			name: "glob-branch",
			args: args{
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						testnewrepo.NewRepo(
							testnewrepo.RepoTestcreationOpts{
								Name:             "test-good",
								URL:              targetURL,
								InstallNamespace: targetNamespace,
							},
						),
					},
				},
				runevent: info.Event{
					URL:        targetURL,
					BaseBranch: "refs/tags/1.0",
					EventType:  "pull_request",
				},
			},
			wantTargetNS: targetNamespace,
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			cs, _ := testclient.SeedTestData(t, ctx, tt.args.data)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			client := &params.Run{
				Clients: clients.Clients{PipelineAsCode: cs.PipelineAsCode, Log: logger},
				Info:    info.Info{},
			}
			got, err := MatchEventURLRepo(ctx, client, &tt.args.runevent, "")

			if err == nil && tt.wantErr {
				assert.NilError(t, err, "GetRepoByCR() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantTargetNS == "" && got != nil {
				t.Errorf("GetRepoByCR() got = '%v', want '%v'", got.GetNamespace(), tt.wantTargetNS)
			}
			if tt.wantTargetNS != "" && got == nil {
				t.Errorf("GetRepoByCR() want nil got '%v'", tt.wantTargetNS)
			}

			if tt.wantTargetNS != "" && tt.wantTargetNS != got.GetNamespace() {
				t.Errorf("GetRepoByCR() got = '%v', want '%v'", got.GetNamespace(), tt.wantTargetNS)
			}
		})
	}
}

func TestIncomingWebhookRule(t *testing.T) {
	tests := []struct {
		name             string
		branch           string
		incomingWebhooks []v1alpha1.Incoming
		wantMatch        bool
		wantSecretName   string
	}{
		{
			name:   "exact match - backward compatibility",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "main-secret", Key: "token"},
					Targets: []string{"main", "develop"},
				},
			},
			wantMatch:      true,
			wantSecretName: "main-secret",
		},
		{
			name:   "exact match - second target",
			branch: "develop",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "dev-secret", Key: "token"},
					Targets: []string{"main", "develop"},
				},
			},
			wantMatch:      true,
			wantSecretName: "dev-secret",
		},
		{
			name:   "glob match - feature branch",
			branch: "feature/new-ui",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "feature-secret", Key: "token"},
					Targets: []string{"feature/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "feature-secret",
		},
		{
			name:   "glob match - release branch with semver",
			branch: "release/v1.2.3",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "release-secret", Key: "token"},
					Targets: []string{"release/v*.*.*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "release-secret",
		},
		{
			name:   "first match wins - exact before glob",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "main-secret", Key: "token"},
					Targets: []string{"main"},
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "catch-all-secret", Key: "token"},
					Targets: []string{"*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "main-secret",
		},
		{
			name:   "first match wins - first glob wins",
			branch: "feature/testing",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "feature-secret", Key: "token"},
					Targets: []string{"feature/*"},
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "catch-all-secret", Key: "token"},
					Targets: []string{"*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "feature-secret",
		},
		{
			name:   "first match wins - webhook order matters",
			branch: "hotfix/critical",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "catch-all-secret", Key: "token"},
					Targets: []string{"*"},
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "hotfix-secret", Key: "token"},
					Targets: []string{"hotfix/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "catch-all-secret", // First webhook matches, even though second is more specific
		},
		{
			name:   "no match - branch not in targets",
			branch: "unknown-branch",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "main-secret", Key: "token"},
					Targets: []string{"main", "develop"},
				},
			},
			wantMatch: false,
		},
		{
			name:   "no match - glob doesn't match",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "feature-secret", Key: "token"},
					Targets: []string{"feature/*"},
				},
			},
			wantMatch: false,
		},
		{
			name:   "invalid glob - skip and continue",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "bad-secret", Key: "token"},
					Targets: []string{"[invalid"}, // Invalid glob (unclosed bracket)
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "good-secret", Key: "token"},
					Targets: []string{"main"},
				},
			},
			wantMatch:      true,
			wantSecretName: "good-secret", // Should skip invalid glob and match next webhook
		},
		{
			name:   "mixed exact and glob in same webhook",
			branch: "staging",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "mixed-secret", Key: "token"},
					Targets: []string{"main", "feature/*", "staging"},
				},
			},
			wantMatch:      true,
			wantSecretName: "mixed-secret",
		},
		{
			name:   "glob with character class",
			branch: "JIRA-12345/bugfix",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "jira-secret", Key: "token"},
					Targets: []string{"[A-Z]*-[0-9]*/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "jira-secret",
		},
		{
			name:   "glob with alternation",
			branch: "dev/testing",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "env-secret", Key: "token"},
					Targets: []string{"{dev,staging,prod}/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "env-secret",
		},
		{
			name:             "empty webhooks",
			branch:           "main",
			incomingWebhooks: []v1alpha1.Incoming{},
			wantMatch:        false,
		},
		{
			name:   "empty targets",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "empty-secret", Key: "token"},
					Targets: []string{},
				},
			},
			wantMatch: false,
		},
		{
			name:   "glob pattern with wildcard",
			branch: "hotfix/urgent-fix",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "hotfix-secret", Key: "token"},
					Targets: []string{"hotfix/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "hotfix-secret",
		},
		{
			name:   "multiple webhooks - production first wins",
			branch: "main",
			incomingWebhooks: []v1alpha1.Incoming{
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "prod-secret", Key: "token"},
					Targets: []string{"main"},
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "dev-secret", Key: "token"},
					Targets: []string{"develop"},
				},
				{
					Type:    "webhook-url",
					Secret:  v1alpha1.Secret{Name: "feature-secret", Key: "token"},
					Targets: []string{"feature/*"},
				},
			},
			wantMatch:      true,
			wantSecretName: "prod-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IncomingWebhookRule(tt.branch, tt.incomingWebhooks)

			if tt.wantMatch {
				if got == nil {
					t.Errorf("IncomingWebhookRule() = nil, want match for branch %s", tt.branch)
					return
				}
				if got.Secret.Name != tt.wantSecretName {
					t.Errorf("IncomingWebhookRule() got secret name = %s, want %s", got.Secret.Name, tt.wantSecretName)
				}
			} else if got != nil {
				t.Errorf("IncomingWebhookRule() = %+v, want nil for branch %s", got, tt.branch)
			}
		})
	}
}

func TestMatchRepo(t *testing.T) {
	tests := []struct {
		name       string
		eventURL   string
		repoURL    string
		wantMatch  bool
		wantError  bool
		errorCheck string
	}{
		// Exact matching
		{
			name:      "exact match",
			eventURL:  "https://github.com/tektoncd/pipelines-as-code",
			repoURL:   "https://github.com/tektoncd/pipelines-as-code",
			wantMatch: true,
		},
		{
			name:      "no substring match",
			eventURL:  "https://github.com/forked/pipelines-as-code",
			repoURL:   "https://github.com/tektoncd/pipelines-as-code",
			wantMatch: false,
		},
		// Wildcard * - matches zero or more characters
		{
			name:      "glob * - prefix pattern",
			eventURL:  "https://github.com/tektoncd/pipelines-as-code",
			repoURL:   "https://github.com/tektoncd/*",
			wantMatch: true,
		},
		{
			name:      "glob * - must match from start",
			eventURL:  "https://gitlab.com/tektoncd/pipelines-as-code",
			repoURL:   "https://github.com/tektoncd/*",
			wantMatch: false,
		},
		{
			name:      "glob * - substring match with wildcards",
			eventURL:  "https://github.com/tektoncd/pipelines-as-code",
			repoURL:   "*/tektoncd/*",
			wantMatch: true,
		},
		{
			name:      "glob * - catch-all",
			eventURL:  "https://github.com/tektoncd/pipelines-as-code",
			repoURL:   "*",
			wantMatch: true,
		},
		// Wildcard ? - matches exactly one character
		{
			name:      "glob ? - single char match",
			eventURL:  "https://github.com/tektoncd/pipelines-as-code-v2",
			repoURL:   "https://github.com/tektoncd/pipelines-as-code-v?",
			wantMatch: true,
		},
		// Character classes [...]
		{
			name:      "glob [range] - character class",
			eventURL:  "https://gitlab.com/tektoncd/pipelines-as-code",
			repoURL:   "https://[a-z]*.com/tektoncd/pipelines-as-code",
			wantMatch: true,
		},
		// Error handling
		{
			name:       "invalid glob - unclosed bracket",
			eventURL:   "https://github.com/tektoncd/pipelines-as-code",
			repoURL:    "https://[github.com/tektoncd/pipelines-as-code",
			wantMatch:  false,
			wantError:  true,
			errorCheck: "unexpected end of input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatch, err := matchRepo(tt.eventURL, tt.repoURL)

			if tt.wantError {
				if err == nil {
					t.Errorf("matchRepo() expected error but got nil")
					return
				}
				if tt.errorCheck != "" {
					assert.ErrorContains(t, err, tt.errorCheck)
				}
			} else {
				if err != nil {
					t.Errorf("matchRepo() unexpected error = %v", err)
					return
				}
				if gotMatch != tt.wantMatch {
					t.Errorf("matchRepo() gotMatch = %v, want %v for eventURL=%q, repoURL=%q", gotMatch, tt.wantMatch, tt.eventURL, tt.repoURL)
				}
			}
		})
	}
}

func TestMatchTarget(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		target     string
		wantMatch  bool
		wantError  bool
		errorCheck string
	}{
		// Exact matching
		{
			name:      "exact match",
			branch:    "main",
			target:    "main",
			wantMatch: true,
		},
		{
			name:      "no substring match",
			branch:    "my-feature-branch",
			target:    "feature",
			wantMatch: false,
		},
		// Wildcard * - matches zero or more characters
		{
			name:      "glob * - prefix pattern",
			branch:    "feature/new-ui",
			target:    "feature/*",
			wantMatch: true,
		},
		{
			name:      "glob * - must match from start",
			branch:    "test/release/v1.0",
			target:    "release*",
			wantMatch: false,
		},
		{
			name:      "glob * - substring match with wildcards",
			branch:    "feature/my-branch",
			target:    "*feature*",
			wantMatch: true,
		},
		{
			name:      "glob * - catch-all",
			branch:    "any-branch-name",
			target:    "*",
			wantMatch: true,
		},
		// Wildcard ? - matches exactly one character
		{
			name:      "glob ? - single char match",
			branch:    "v1",
			target:    "v?",
			wantMatch: true,
		},
		// Character classes [...]
		{
			name:      "glob [range] - character class",
			branch:    "JIRA-123/feature",
			target:    "[A-Z]*-[0-9]*/*",
			wantMatch: true,
		},
		// Alternation {...}
		{
			name:      "glob {a,b,c} - alternation",
			branch:    "dev/testing",
			target:    "{dev,staging,prod}/*",
			wantMatch: true,
		},
		// Dots are literal
		{
			name:      "dots are literal - exact version match",
			branch:    "v1.0",
			target:    "v1.0",
			wantMatch: true,
		},
		{
			name:      "dots are literal - do not match any char",
			branch:    "v1x0",
			target:    "v1.0",
			wantMatch: false,
		},
		{
			name:      "semver with wildcards",
			branch:    "release-1.2.3",
			target:    "release-*.*.*",
			wantMatch: true,
		},
		// Real-world patterns
		{
			name:      "real-world - JIRA pattern",
			branch:    "feature/JIRA-123_fix-bug.v1",
			target:    "feature/JIRA-*_*",
			wantMatch: true,
		},
		{
			name:      "real-world - version tags",
			branch:    "tags/v1.2.3",
			target:    "tags/v*.*.*",
			wantMatch: true,
		},
		// Error handling
		{
			name:       "invalid glob - unclosed bracket",
			branch:     "main",
			target:     "[invalid",
			wantMatch:  false,
			wantError:  true,
			errorCheck: "unexpected end of input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatch, err := matchTarget(tt.branch, tt.target)

			if tt.wantError {
				if err == nil {
					t.Errorf("matchTarget() expected error but got nil")
					return
				}
				if tt.errorCheck != "" {
					assert.ErrorContains(t, err, tt.errorCheck)
				}
			} else {
				if err != nil {
					t.Errorf("matchTarget() unexpected error = %v", err)
					return
				}
				if gotMatch != tt.wantMatch {
					t.Errorf("matchTarget() gotMatch = %v, want %v for branch=%q, target=%q", gotMatch, tt.wantMatch, tt.branch, tt.target)
				}
			}
		})
	}
}

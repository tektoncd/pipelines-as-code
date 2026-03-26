package v1alpha1

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestMergeSpecs(t *testing.T) {
	two := 2
	incomings := &[]Incoming{{
		Type: "type",
		Secret: Secret{
			Name: "name",
		},
		Params: []string{"param1", "param2"},
	}}
	gp := &GitProvider{
		URL:  "url",
		User: "user",
		Secret: &Secret{
			Name: "name",
		},
		WebhookSecret: &Secret{
			Name: "webhook",
		},
		Type: "type1",
	}
	params := &[]Params{{Name: "name", Value: "value"}}
	tests := []struct {
		name     string
		local    *RepositorySpec
		global   RepositorySpec
		expected *RepositorySpec
	}{
		{
			name:  "global settings just params and concurrency",
			local: &RepositorySpec{},
			global: RepositorySpec{
				Params:           params,
				ConcurrencyLimit: &two,
			},
			expected: &RepositorySpec{
				Params:           params,
				ConcurrencyLimit: &two,
			},
		},
		{
			name: "global settings",
			local: &RepositorySpec{
				Settings:    &Settings{},
				GitProvider: &GitProvider{}, // Initialize as needed
			},
			global: RepositorySpec{
				Settings: &Settings{
					GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
					PipelineRunProvenance:    "provenance",
					Policy: &Policy{
						OkToTest: []string{"ok1", "ok2"},
					},
					GitOpsCommandPrefix: "pac",
				}, // Initialize as needed
				GitProvider:      gp, // Initialize as needed
				Incomings:        incomings,
				Params:           params,
				ConcurrencyLimit: &two,
			},
			expected: &RepositorySpec{
				Settings: &Settings{
					GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
					PipelineRunProvenance:    "provenance",
					Policy: &Policy{
						OkToTest: []string{"ok1", "ok2"},
					},
					GitOpsCommandPrefix: "pac",
				},
				Incomings:        incomings,
				GitProvider:      gp,
				Params:           params,
				ConcurrencyLimit: &two,
			},
		},
		{
			name: "local settings take precedence",
			local: &RepositorySpec{
				Settings: &Settings{
					GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
					PipelineRunProvenance:    "provenance",
					Policy: &Policy{
						OkToTest: []string{"ok1", "ok2"},
					},
					GitOpsCommandPrefix: "pac",
				}, // Initialize as needed
				GitProvider: &GitProvider{}, // Initialize as needed
			},
			global: RepositorySpec{
				Settings: &Settings{
					GithubAppTokenScopeRepos: []string{"hello", "moto"},
					PipelineRunProvenance:    "somewhere",
					Policy: &Policy{
						OkToTest: []string{"to", "be"},
					},
				}, // Initialize as needed
				GitProvider:      gp, // Initialize as needed
				Incomings:        incomings,
				Params:           params,
				ConcurrencyLimit: &two,
			},
			expected: &RepositorySpec{
				Settings: &Settings{
					GithubAppTokenScopeRepos: []string{"repo1", "repo2"},
					PipelineRunProvenance:    "provenance",
					Policy: &Policy{
						OkToTest: []string{"ok1", "ok2"},
					},
					GitOpsCommandPrefix: "pac",
				},
				Incomings:        incomings,
				GitProvider:      gp,
				Params:           params,
				ConcurrencyLimit: &two,
			},
		},
		{
			name: "forgejo settings from global",
			local: &RepositorySpec{
				Settings:    &Settings{},
				GitProvider: &GitProvider{},
			},
			global: RepositorySpec{
				Settings: &Settings{
					Forgejo: &ForgejoSettings{
						UserAgent: "my-custom-agent",
					},
				},
				GitProvider: &GitProvider{},
			},
			expected: &RepositorySpec{
				Settings: &Settings{
					Forgejo: &ForgejoSettings{
						UserAgent: "my-custom-agent",
					},
				},
				GitProvider: &GitProvider{},
			},
		},
		{
			name: "local forgejo settings take precedence",
			local: &RepositorySpec{
				Settings: &Settings{
					Forgejo: &ForgejoSettings{
						UserAgent: "local-agent",
					},
				},
				GitProvider: &GitProvider{},
			},
			global: RepositorySpec{
				Settings: &Settings{
					Forgejo: &ForgejoSettings{
						UserAgent: "global-agent",
					},
				},
				GitProvider: &GitProvider{},
			},
			expected: &RepositorySpec{
				Settings: &Settings{
					Forgejo: &ForgejoSettings{
						UserAgent: "local-agent",
					},
				},
				GitProvider: &GitProvider{},
			},
		},
		{
			name: "different git providers",
			local: &RepositorySpec{
				GitProvider: &GitProvider{
					Type: "local",
				}, // Initialize as needed
			},
			global: RepositorySpec{
				GitProvider: gp,
			},
			expected: &RepositorySpec{
				GitProvider: &GitProvider{
					Type: "local",
				},
			},
		},
		{
			name: "forgejo local merges with gitea global",
			local: &RepositorySpec{
				GitProvider: &GitProvider{
					Type: "forgejo",
				},
			},
			global: RepositorySpec{
				GitProvider: &GitProvider{
					Type:   "gitea",
					URL:    "https://gitea.example.com",
					User:   "user",
					Secret: &Secret{Name: "secret"},
				},
			},
			expected: &RepositorySpec{
				GitProvider: &GitProvider{
					Type:   "forgejo",
					URL:    "https://gitea.example.com",
					User:   "user",
					Secret: &Secret{Name: "secret"},
				},
			},
		},
		{
			name: "gitea local merges with forgejo global",
			local: &RepositorySpec{
				GitProvider: &GitProvider{
					Type: "gitea",
				},
			},
			global: RepositorySpec{
				GitProvider: &GitProvider{
					Type:   "forgejo",
					URL:    "https://forgejo.example.com",
					User:   "user",
					Secret: &Secret{Name: "secret"},
				},
			},
			expected: &RepositorySpec{
				GitProvider: &GitProvider{
					Type:   "gitea",
					URL:    "https://forgejo.example.com",
					User:   "user",
					Secret: &Secret{Name: "secret"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.local.Merge(tt.global)
			assert.DeepEqual(t, tt.expected, tt.local)
		})
	}
}

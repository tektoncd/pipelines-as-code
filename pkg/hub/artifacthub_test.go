// Copyright © 2022 The Tekton Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hub

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
)

func TestNewArtifactHubClient(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantedURL string
	}{
		{
			name:      "URL with api/v1 suffix",
			url:       "https://artifacthub.io/api/v1",
			wantedURL: "https://artifacthub.io/api/v1",
		},
		{
			name:      "URL without api/v1 suffix",
			url:       "https://artifacthub.io",
			wantedURL: "https://artifacthub.io/api/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newArtifactHubClient(&params.Run{}, tt.url, "test")
			ahClient, ok := client.(*artifactHubClient)
			assert.Assert(t, ok)
			assert.Equal(t, tt.wantedURL, ahClient.url)
		})
	}
}

func TestGetTypeByKind(t *testing.T) {
	tests := []struct {
		name        string
		catalogName string
		kind        string
		wantType    string
		wantCatalog string
	}{
		{
			name:        "task with default catalog",
			catalogName: "default",
			kind:        "task",
			wantType:    artifactHubTaskType,
			wantCatalog: defaultArtifactHubCatalogTaskName,
		},
		{
			name:        "task with empty catalog",
			catalogName: "",
			kind:        "task",
			wantType:    artifactHubTaskType,
			wantCatalog: defaultArtifactHubCatalogTaskName,
		},
		{
			name:        "task with custom catalog",
			catalogName: "custom",
			kind:        "task",
			wantType:    artifactHubTaskType,
			wantCatalog: "custom",
		},
		{
			name:        "pipeline with default catalog",
			catalogName: "default",
			kind:        "pipeline",
			wantType:    artifactHubPipelineType,
			wantCatalog: defaultArtifactHubCatalogPipelineName,
		},
		{
			name:        "pipeline with empty catalog",
			catalogName: "",
			kind:        "pipeline",
			wantType:    artifactHubPipelineType,
			wantCatalog: defaultArtifactHubCatalogPipelineName,
		},
		{
			name:        "pipeline with custom catalog",
			catalogName: "custom-pipeline",
			kind:        "pipeline",
			wantType:    artifactHubPipelineType,
			wantCatalog: "custom-pipeline",
		},
		{
			name:        "unknown kind with custom catalog",
			catalogName: "custom",
			kind:        "unknown",
			wantType:    "",
			wantCatalog: "custom",
		},
		{
			name:        "unknown kind with default catalog",
			catalogName: "default",
			kind:        "unknown",
			wantType:    "",
			wantCatalog: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotCatalog := getArtifactHubTypeByKind(tt.catalogName, tt.kind)
			assert.Equal(t, tt.wantType, gotType)
			assert.Equal(t, tt.wantCatalog, gotCatalog)
		})
	}
}

func TestArtifactHubClientGetResource(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name        string
		resource    string
		kind        string
		catalogName string
		want        string
		config      map[string]map[string]string
		wantErr     bool
	}{
		{
			name:        "get latest version task",
			resource:    "git-clone",
			kind:        "task",
			catalogName: "tekton",
			want:        "atask",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": "atask"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get latest version pipeline",
			resource:    "buildpacks",
			kind:        "pipeline",
			catalogName: "tekton",
			want:        "apipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/buildpacks", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": "apipeline"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get specific version task",
			resource:    "git-clone:0.9.0",
			kind:        "task",
			catalogName: "tekton",
			want:        "atask-v090",
			wantErr:     false,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/0.9.0", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": "atask-v090"}}`,
					"code": "200",
				},
			},
		},
		{
			name:        "get specific version pipeline",
			resource:    "buildpacks:1.0.0",
			kind:        "pipeline",
			catalogName: "tekton",
			want:        "apipeline-v100",
			wantErr:     false,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/buildpacks/1.0.0", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": "apipeline-v100"}}`,
					"code": "200",
				},
			},
		},
		{
			name:        "resource with multiple colons in version",
			resource:    "git-clone:v0.9.0:latest",
			kind:        "task",
			catalogName: "tekton",
			want:        "atask-complex",
			wantErr:     false,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/latest", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": "atask-complex"}}`,
					"code": "200",
				},
			},
		},
		{
			name:        "HTTP client error",
			resource:    "git-clone",
			kind:        "task",
			catalogName: "tekton",
			want:        "",
			wantErr:     true,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, testCatalogHubName): {
					"code": "500",
				},
			},
		},
		{
			name:        "invalid JSON response",
			resource:    "git-clone",
			kind:        "task",
			catalogName: "tekton",
			want:        "",
			wantErr:     true,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, testCatalogHubName): {
					"body": `{"invalid": json}`,
					"code": "200",
				},
			},
		},
		{
			name:        "empty manifest in response",
			resource:    "git-clone",
			kind:        "task",
			catalogName: "tekton",
			want:        "",
			wantErr:     true,
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, testCatalogHubName): {
					"body": `{"data": {"manifestRaw": ""}}`,
					"code": "200",
				},
			},
		},
		{
			name:        "no config provided (HTTP error)",
			resource:    "git-clone",
			kind:        "task",
			catalogName: "tekton",
			want:        "",
			wantErr:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tc.config)

			cs := &params.Run{
				Clients: clients.Clients{
					HTTP: *httpTestClient,
				},
			}
			client := newArtifactHubClient(cs, testHubURL, testCatalogHubName)
			got, err := client.GetResource(ctx, tc.catalogName, tc.resource, tc.kind)

			if tc.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestArtifactHubClientGetLatestVersion(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		catalogName string
		resource    string
		kind        string
		want        string
		config      map[string]map[string]string
		wantErr     bool
	}{
		{
			name:        "get latest task version with default catalog",
			catalogName: "default",
			resource:    "git-clone",
			kind:        "task",
			want:        "task-manifest-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": {"manifestRaw": "task-manifest-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get latest pipeline version with default catalog",
			catalogName: "default",
			resource:    "buildpacks",
			kind:        "pipeline",
			want:        "pipeline-manifest-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/buildpacks", testHubURL, defaultArtifactHubCatalogPipelineName): {
					"body": `{"data": {"manifestRaw": "pipeline-manifest-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get latest version with custom catalog",
			catalogName: "my-custom-catalog",
			resource:    "custom-task",
			kind:        "task",
			want:        "custom-task-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/my-custom-catalog/custom-task", testHubURL): {
					"body": `{"data": {"manifestRaw": "custom-task-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "network error",
			catalogName: "default",
			resource:    "git-clone",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, defaultArtifactHubCatalogTaskName): {
					"code": "404",
				},
			},
			wantErr: true,
		},
		{
			name:        "malformed JSON response",
			catalogName: "default",
			resource:    "git-clone",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": invalid json}`,
					"code": "200",
				},
			},
			wantErr: true,
		},
		{
			name:        "empty manifestRaw field",
			catalogName: "default",
			resource:    "git-clone",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": {"manifestRaw": ""}}`,
					"code": "200",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.config)
			client := &artifactHubClient{
				params: &params.Run{
					Clients: clients.Clients{
						HTTP: *httpTestClient,
					},
				},
				url:  testHubURL + "/api/v1",
				name: testCatalogHubName,
			}

			got, err := client.getLatestVersion(ctx, tt.catalogName, tt.resource, tt.kind)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				assert.Equal(t, "", got)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestArtifactHubClientGetSpecificVersion(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		catalogName string
		resource    string
		kind        string
		want        string
		config      map[string]map[string]string
		wantErr     bool
	}{
		{
			name:        "get specific task version",
			catalogName: "default",
			resource:    "git-clone:0.9.0",
			kind:        "task",
			want:        "task-v090-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/0.9.0", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": {"manifestRaw": "task-v090-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get specific pipeline version",
			catalogName: "default",
			resource:    "buildpacks:1.0.0",
			kind:        "pipeline",
			want:        "pipeline-v100-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/buildpacks/1.0.0", testHubURL, defaultArtifactHubCatalogPipelineName): {
					"body": `{"data": {"manifestRaw": "pipeline-v100-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "resource with multiple colons - use last part as version",
			catalogName: "default",
			resource:    "namespace:resource:v1.2.3",
			kind:        "task",
			want:        "complex-version-content",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/namespace/v1.2.3", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": {"manifestRaw": "complex-version-content"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "get specific version with custom catalog",
			catalogName: "my-custom-catalog",
			resource:    "custom-task:2.0.0",
			kind:        "task",
			want:        "custom-task-v200",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/my-custom-catalog/custom-task/2.0.0", testHubURL): {
					"body": `{"data": {"manifestRaw": "custom-task-v200"}}`,
					"code": "200",
				},
			},
			wantErr: false,
		},
		{
			name:        "version not found",
			catalogName: "default",
			resource:    "git-clone:99.99.99",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/99.99.99", testHubURL, defaultArtifactHubCatalogTaskName): {
					"code": "404",
				},
			},
			wantErr: true,
		},
		{
			name:        "malformed JSON response",
			catalogName: "default",
			resource:    "git-clone:0.9.0",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/0.9.0", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"invalid": "json"`,
					"code": "200",
				},
			},
			wantErr: true,
		},
		{
			name:        "empty manifestRaw field",
			catalogName: "default",
			resource:    "git-clone:0.9.0",
			kind:        "task",
			want:        "",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/git-clone/0.9.0", testHubURL, defaultArtifactHubCatalogTaskName): {
					"body": `{"data": {"manifestRaw": ""}}`,
					"code": "200",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.config)
			client := &artifactHubClient{
				params: &params.Run{
					Clients: clients.Clients{
						HTTP: *httpTestClient,
					},
				},
				url:  testHubURL + "/api/v1",
				name: testCatalogHubName,
			}

			got, err := client.getSpecificVersion(ctx, tt.catalogName, tt.resource, tt.kind)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				assert.Equal(t, "", got)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestNewArtifactHubClientURLHandling(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantedURL string
	}{
		{
			name:      "URL with trailing slash",
			url:       "https://artifacthub.io/",
			wantedURL: "https://artifacthub.io/api/v1",
		},
		{
			name:      "URL with api/v1 and trailing slash",
			url:       "https://artifacthub.io/api/v1/",
			wantedURL: "https://artifacthub.io/api/v1",
		},
		{
			name:      "URL with path and trailing slash",
			url:       "https://hub.example.com/custom/path/",
			wantedURL: "https://hub.example.com/custom/path/api/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newArtifactHubClient(&params.Run{}, tt.url, "test")
			ahClient, ok := client.(*artifactHubClient)
			assert.Assert(t, ok)
			assert.Equal(t, tt.wantedURL, ahClient.url)
			assert.Equal(t, "test", ahClient.name)
		})
	}
}

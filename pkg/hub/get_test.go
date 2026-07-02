package hub

import (
	"fmt"
	"sync"
	"testing"

	hubType "github.com/openshift-pipelines/pipelines-as-code/pkg/hub/vars"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	sampleArtifactHubManifest = `{"data": {"manifestRaw": "%s"}}`
	testHubURL                = "https://myprecioushub"
	testCatalogHubName        = "tekton"
	testHubCatalogName        = "tekton-catalog-tasks"
)

func TestGetTask(t *testing.T) {
	var hubCatalogs sync.Map
	hubCatalogs.Store(
		"default", settings.HubCatalog{
			Index: "default",
			URL:   testHubURL,
			Name:  "default",
			Type:  hubType.ArtifactHubType,
		},
	)
	hubCatalogs.Store(
		"anotherHub", settings.HubCatalog{
			Index: "1",
			URL:   testHubURL,
			Name:  testCatalogHubName,
			Type:  hubType.ArtifactHubType,
		},
	)
	hubCatalogs.Store(
		hubType.ArtifactHubType, settings.HubCatalog{
			Index: "2",
			URL:   testHubURL,
			Name:  "tekton-catalog-tasks",
			Type:  hubType.ArtifactHubType,
		},
	)
	tests := []struct {
		name        string
		resource    string
		want        string
		wantErr     bool
		config      map[string]map[string]string
		catalogName string
		kind        string
	}{
		{
			name:        "get-task-latest",
			resource:    "git-clone",
			want:        "sometask",
			wantErr:     false,
			catalogName: "default",
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "sometask"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-task-latest-custom",
			resource:    "task1",
			want:        "This is Task1",
			wantErr:     false,
			catalogName: "anotherHub",
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton/task1", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "This is Task1"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-latest-task-not-there",
			resource:    "task1",
			catalogName: "default",
			wantErr:     true,
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/resource/%s/task/task1", testHubURL, testCatalogHubName): {
					"code": "404",
				},
			},
		},
		{
			name:        "get-specific-task-not-there",
			resource:    "task1:1.1",
			wantErr:     true,
			catalogName: "default",
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/resource/%s/task/task1/1.1", testHubURL, testCatalogHubName): {
					"code": "404",
				},
			},
		},
		{
			name:        "get-specific-hub-not-there",
			resource:    "task1:1.1",
			wantErr:     true,
			catalogName: "notexist",
			kind:        "task",
		},
		{
			name:        "get-specific-hub-not-there-with-latest",
			resource:    "task1",
			wantErr:     true,
			catalogName: "notexist",
			kind:        "task",
		},
		{
			name:        "get-task-specific",
			resource:    "task2:1.1",
			want:        "This is Task2",
			wantErr:     false,
			catalogName: "default",
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/task2/1.1", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "This is Task2"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-pipeline-latest",
			resource:    "git-clone",
			want:        "sometask",
			wantErr:     false,
			catalogName: "default",
			kind:        "pipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/tekton-catalog-pipelines/git-clone", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "sometask"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-pipeline-latest-custom",
			resource:    "pipeline1",
			want:        "This is Pipeline1",
			wantErr:     false,
			catalogName: "anotherHub",
			kind:        "pipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/tekton/pipeline1", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "This is Pipeline1"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-latest-pipeline-not-there",
			resource:    "pipeline1",
			catalogName: "default",
			wantErr:     true,
			kind:        "pipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/resource/%s/pipeline/pipeline1", testHubURL, testCatalogHubName): {
					"code": "404",
				},
			},
		},
		{
			name:        "get-specific-pipeline-not-there",
			resource:    "pipeline1:1.1",
			wantErr:     true,
			catalogName: "default",
			kind:        "pipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/resource/%s/pipeline/pipeline1/1.1", testHubURL, testCatalogHubName): {
					"code": "404",
				},
			},
		},
		{
			name:        "get-pipeline-specific",
			resource:    "pipeline2:1.1",
			want:        "This is Pipeline2",
			wantErr:     false,
			catalogName: "default",
			kind:        "pipeline",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/tekton-catalog-pipelines/pipeline2/1.1", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "This is Pipeline2"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-task-latest-artifacthub",
			resource:    "git-clone",
			want:        "sometask",
			wantErr:     false,
			catalogName: hubType.ArtifactHubType,
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "sometask"),
					"code": "200",
				},
			},
		},
		{
			name:        "get-task-specific-version-artifacthub",
			resource:    "git-clone:0.9.0",
			want:        "aspecifictask",
			wantErr:     false,
			catalogName: hubType.ArtifactHubType,
			kind:        "task",
			config: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone/0.9.0", testHubURL): {
					"body": fmt.Sprintf(sampleArtifactHubManifest, "aspecifictask"),
					"code": "200",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.config)
			ctx, _ := rtesting.SetupFakeContext(t)
			cs := &params.Run{
				Clients: clients.Clients{
					HTTP: *httpTestClient,
				},
				Info: info.Info{Pac: &info.PacOpts{
					Settings: settings.Settings{
						HubCatalogs: &hubCatalogs,
					},
				}},
			}
			got, err := GetResource(ctx, cs, tt.catalogName, tt.resource, tt.kind)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTask() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("GetTask() got = %v, want %v", got, tt.want)
			}
		})
	}
}

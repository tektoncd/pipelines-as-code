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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
)

const (
	artifactHubTaskType                   = "tekton-task"
	artifactHubPipelineType               = "tekton-pipeline"
	defaultArtifactHubCatalogTaskName     = "tekton-catalog-tasks"
	defaultArtifactHubCatalogPipelineName = "tekton-catalog-pipelines"
)

// artifactHubClient is a client for the Artifact Hub.
type artifactHubClient struct {
	params *params.Run
	url    string
	name   string
}

// newArtifactHubClient returns a new Artifact Hub client.
func newArtifactHubClient(params *params.Run, url, name string) Client {
	url = strings.TrimSuffix(url, "/") // Trim any trailing slash
	if !strings.HasSuffix(url, "/api/v1") {
		url = fmt.Sprintf("%s/api/v1", url)
	}
	return &artifactHubClient{params: params, url: url, name: name}
}

// GetResource gets a resource from the Artifact Hub.
func (a *artifactHubClient) GetResource(ctx context.Context, _, resource, kind string) (string, error) {
	var data string
	var err error

	if strings.Contains(resource, ":") {
		data, err = a.getSpecificVersion(ctx, a.name, resource, kind)
	} else {
		data, err = a.getLatestVersion(ctx, a.name, resource, kind)
	}
	if err != nil {
		return "", fmt.Errorf("could not fetch remote %s %s, artifacthub API returned: %w", kind, resource, err)
	}

	return data, nil
}

func getArtifactHubTypeByKind(catalogName, kind string) (string, string) {
	var pkgType string
	switch kind {
	case "task":
		pkgType = artifactHubTaskType
		if catalogName == "default" || catalogName == "" {
			catalogName = defaultArtifactHubCatalogTaskName
		}
	case "pipeline":
		pkgType = artifactHubPipelineType
		if catalogName == "default" || catalogName == "" {
			catalogName = defaultArtifactHubCatalogPipelineName
		}
		// For other kinds, no changes are made.
	}

	return pkgType, catalogName
}

// getLatestVersion gets the latest version of a resource from the Artifact Hub.
// url is like:
// https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone
func (a *artifactHubClient) getLatestVersion(ctx context.Context, catalogName, resource, kind string) (string, error) {
	pkgType, catalogName := getArtifactHubTypeByKind(catalogName, kind)
	url := fmt.Sprintf("%s/packages/%s/%s/%s", a.url, pkgType, catalogName, resource)
	resp := new(artifactHubPkgResponse)
	data, err := a.params.Clients.GetURL(ctx, url)
	if err != nil {
		return "", fmt.Errorf("could not fetch %s %s from hub, url: %s: %w", kind, resource, url, err)
	}
	err = json.Unmarshal(data, &resp)
	if err != nil {
		return "", fmt.Errorf("could not unmarshal response from hub, url: %s: %w", url, err)
	}
	if resp.Data.ManifestRaw == "" {
		return "", fmt.Errorf("manifest is empty in hub response for, url: %s %s", url, resource)
	}
	return resp.Data.ManifestRaw, nil
}

// getSpecificVersion gets a specific version of a resource from the Artifact Hub.
// url is like:
// https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone/0.9.0
func (a *artifactHubClient) getSpecificVersion(ctx context.Context, catalogName, resource, kind string) (string, error) {
	pkgType, catalogName := getArtifactHubTypeByKind(catalogName, kind)

	split := strings.Split(resource, ":")
	version := split[len(split)-1]
	resourceName := split[0]

	url := fmt.Sprintf("%s/packages/%s/%s/%s/%s", a.url, pkgType, catalogName, resourceName, version)
	resp := new(artifactHubPkgResponse)
	data, err := a.params.Clients.GetURL(ctx, url)
	if err != nil {
		return "", fmt.Errorf("could not fetch %s %s from hub, url: %s: %w", kind, resource, url, err)
	}
	err = json.Unmarshal(data, &resp)
	if err != nil {
		return "", fmt.Errorf("could not unmarshal response from hub, url: %s: %w", url, err)
	}
	if resp.Data.ManifestRaw == "" {
		return "", fmt.Errorf("manifest is empty in hub response for, url: %s %s", url, resource)
	}
	return resp.Data.ManifestRaw, nil
}

// artifactHubPkgResponse is the response from the Artifact Hub API.
// It contains a `data` field, which holds the package data, including the raw manifest.
// The JSON structure is as follows:
//
//	{
//	  "data": {
//	    "manifestRaw": "<raw manifest content>"
//	  }
//	}
type artifactHubPkgResponse struct {
	Data artifactHubPkgData `json:"data,omitempty"`
}

// artifactHubPkgData represents the data field in the response from the Artifact Hub API.
// It contains the raw manifest of a Tekton resource (e.g., task or pipeline) as a string.
// The JSON structure it maps to is:
//
//	{
//	  "manifestRaw": "<raw manifest content>"
//	}
type artifactHubPkgData struct {
	ManifestRaw string `json:"manifestRaw"`
}

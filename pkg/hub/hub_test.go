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
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"gotest.tools/v3/assert"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		catalogName string
		wantErr     bool
	}{
		{
			name:        "artifacthub client",
			catalogName: "artifact",
			wantErr:     false,
		},
		{
			name:        "default to artifacthub client",
			catalogName: "default",
			wantErr:     false,
		},
		{
			name:        "error on invalid catalog name",
			catalogName: "invalid",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			catalogs := &sync.Map{}
			if tt.catalogName != "invalid" {
				catalogs.Store(tt.catalogName, settings.HubCatalog{
					Name: tt.catalogName,
					URL:  "https://test.com",
				})
			}

			pacOpts := info.NewPacOpts()
			pacOpts.HubCatalogs = catalogs

			cs := &params.Run{
				Info: info.Info{
					Pac: pacOpts,
				},
			}

			client, err := NewClient(ctx, cs, tt.catalogName)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
			} else {
				assert.NilError(t, err)
				_, ok := client.(*artifactHubClient)
				assert.Assert(t, ok, "expected *artifactHubClient but got different type")
			}
		})
	}
}
